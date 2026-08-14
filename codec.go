package minijson

// json.Marshaler and encoding.TextMarshaler support.
//
// Without this, a type that defines its own JSON representation is taken apart structurally
// instead, and the representation is lost. time.Time is the case that matters: its fields are all
// unexported, so promotion leaves nothing to write and the value travels as `{}` — the date
// silently gone. Before promotion it was worse in a different way: minijson emitted `wall`, `ext`
// and the whole of `loc`, and typed decoding of it panicked inside reflect.
//
// So the marshaler takes precedence over the structural path on the way out, and the matching
// unmarshaler on the way back. That is what encoding/json does, and agreeing with encoding/json is
// the contract minijson has to hold: the same struct must produce the same JSON value either way.
//
// Known limitation: only value-receiver MarshalJSON/MarshalText are honoured. A pointer-receiver
// marshaler on a non-addressable value is invisible to reflection, and minijson receives its input
// through an `any`, so nothing is addressable at the top. This matches encoding/json's own
// allowAddr restriction closely enough for the types that occur in practice — time.Time,
// decimal.Decimal and the null.* family all use value receivers.

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"github.com/bytedance/sonic"
)

var (
	jsonMarshalerType   = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshalerType   = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	jsonUnmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

// codecFlags records which custom conversions a type supports.
type codecFlags struct {
	marshalJSON     bool
	marshalText     bool
	unmarshalJSON   bool
	unmarshalText   bool
	needsMarshaler  bool // marshalJSON || marshalText
	needsUnmarshler bool // unmarshalJSON || unmarshalText
}

// marshalerFor reports how a type renders itself on the way out, or the zero value when it renders
// structurally.
//
// The method count is checked first and prunes almost everything: implementing json.Marshaler or
// encoding.TextMarshaler requires an exported method, so a type with none cannot possibly, and every
// scalar and every plain struct lands there. Reading the count is a field access on the type
// descriptor, which is why this needs no cache — the Implements calls only ever run for the rare
// method-bearing type. An earlier version cached in a sync.Map instead and cost 69% on the encoder,
// because one atomic-plus-hash per value dwarfs appending a few bytes.
func marshalerFor(t reflect.Type) codecFlags {
	if t.NumMethod() == 0 {
		return codecFlags{}
	}

	var flags codecFlags
	flags.marshalJSON = t.Implements(jsonMarshalerType)
	// json.Marshaler wins, so TextMarshaler is only worth checking when it does not apply.
	flags.marshalText = !flags.marshalJSON && t.Implements(textMarshalerType)
	flags.needsMarshaler = flags.marshalJSON || flags.marshalText
	return flags
}

// unmarshalerCache memoises the decode-side checks. Unlike the encode side these cannot be pruned by
// the value type's method count, because unmarshalers take pointer receivers and so do not appear in
// it — and reflect.PointerTo can fall back to a locked global map, which is exactly what must not
// happen per value. So the answer is cached instead, and the pointer type is resolved once per type.
var unmarshalerCache sync.Map // reflect.Type -> codecFlags

func unmarshalerFor(t reflect.Type) codecFlags {
	if cached, found := unmarshalerCache.Load(t); found {
		return cached.(codecFlags)
	}

	pointerTo := reflect.PointerTo(t)
	var flags codecFlags
	if pointerTo.NumMethod() > 0 {
		flags.unmarshalJSON = pointerTo.Implements(jsonUnmarshalerType)
		flags.unmarshalText = !flags.unmarshalJSON && pointerTo.Implements(textUnmarshalerType)
		flags.needsUnmarshler = flags.unmarshalJSON || flags.unmarshalText
	}

	unmarshalerCache.Store(t, flags)
	return flags
}

// staticCodec is the encoding decision for a type known statically — a struct field's type, a
// slice's element type, a map's value type. Resolving it once at that level rather than per value is
// what keeps the marshaler support off the hot path: an earlier version resolved per value and cost
// 9% on the encoder.
type staticCodec struct {
	flags codecFlags
	// dynamic marks a static type that hides the real one behind an interface, so the decision can
	// only be made per value after all.
	dynamic bool
	// elem is the decision for a slice/array element or a map value, resolved here so encoding a
	// collection does not have to reach back into reflect for it. Nil when the type is not a
	// collection, or when the collection itself renders through a marshaler.
	elem *staticCodec
}

func staticCodecFor(t reflect.Type) staticCodec {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	codec := staticCodec{flags: marshalerFor(t), dynamic: t.Kind() == reflect.Interface}
	if codec.flags.needsMarshaler || codec.dynamic {
		return codec
	}

	switch t.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		// Terminates: each step strips one layer of collection or pointer off the type.
		elem := staticCodecFor(t.Elem())
		codec.elem = &elem
	}
	return codec
}

// appendViaMarshaler writes a value through its own JSON representation. json.Marshaler wins over
// encoding.TextMarshaler, matching encoding/json's precedence.
func appendViaMarshaler(dst []byte, val reflect.Value, flags codecFlags) ([]byte, error) {
	// A nil pointer or interface writes null rather than calling through it, which is both what
	// encoding/json does and what stops a value-receiver marshaler from being invoked on nil.
	if (val.Kind() == reflect.Ptr || val.Kind() == reflect.Interface) && val.IsNil() {
		return append(dst, "null"...), nil
	}

	if flags.marshalJSON {
		raw, err := val.Interface().(json.Marshaler).MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("minijson: %s.MarshalJSON: %w", val.Type(), err)
		}
		// The bytes go into the payload verbatim, so invalid JSON here would corrupt the whole
		// response rather than just this field. Cheaper to refuse than to ship a broken payload.
		if !json.Valid(raw) {
			return nil, fmt.Errorf("minijson: %s.MarshalJSON returned invalid JSON: %s", val.Type(), raw)
		}
		return append(dst, raw...), nil
	}

	text, err := val.Interface().(encoding.TextMarshaler).MarshalText()
	if err != nil {
		return nil, fmt.Errorf("minijson: %s.MarshalText: %w", val.Type(), err)
	}
	// The conversion allocates, which is fine: this path runs once per TextMarshaler field, and
	// TextMarshaler is the rarer of the two interfaces to begin with.
	return appendJSONString(dst, string(text)), nil
}

// unmarshalViaCodec restores a value through its own JSON representation. val has to be
// addressable, since every unmarshaler takes a pointer receiver.
func unmarshalViaCodec(data any, val reflect.Value, flags codecFlags) error {
	target := val.Addr().Interface()

	if flags.unmarshalJSON {
		// The decoder works on the tree sonic produced, not on the original bytes, so the value has
		// to be rendered back to JSON for UnmarshalJSON to consume.
		raw, err := sonic.Marshal(data)
		if err != nil {
			return fmt.Errorf("minijson: re-encoding %T for %s.UnmarshalJSON: %w", data, val.Type(), err)
		}
		return target.(json.Unmarshaler).UnmarshalJSON(raw)
	}

	text, isString := data.(string)
	if !isString {
		return fmt.Errorf("minijson: cannot unmarshal %T into %s via UnmarshalText", data, val.Type())
	}
	return target.(encoding.TextUnmarshaler).UnmarshalText([]byte(text))
}

// zeroMarshalerDefault renders the zero value of a marshaler type the way encoding/json would, for
// the defaults block. Returns nil when the type cannot produce one.
//
// The recover is not paranoia: this runs at type-registration time, inside the registry lock, so a
// MarshalJSON that does not expect a zero receiver would otherwise take down the first Marshal of
// that type rather than just losing one default.
func zeroMarshalerDefault(t reflect.Type) (result []byte) {
	defer func() {
		if recover() != nil {
			result = nil
		}
	}()

	raw, err := json.Marshal(reflect.Zero(t).Interface())
	if err != nil || !json.Valid(raw) {
		return nil
	}
	return raw
}
