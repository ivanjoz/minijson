package minijson

// Zero-value defaults for schemaless decoders.
//
// minijson omits every zero value from the wire regardless of whether the field's json tag carries
// `omitempty`. Go's typed Unmarshal hides that — it repopulates each field with its type's zero —
// but a decoder without types, like the browser one, cannot: the field is simply absent, so it
// reads back as `undefined` instead of `0`, `""` or `false`. On a codebase where most json tags
// have no `omitempty` that changes the shape of almost every response.
//
// The fix is to describe, once per type in the payload's keys header, what each non-omitempty
// field's zero value looks like. That block is what lets the frontend reconstruct the exact object
// encoding/json would have sent, while the wire still carries no zero values at all. The cost is
// one small JSON object per type per payload — not per record — and it is precomputed at type
// registration, so emitting it is a byte copy.
//
// Fields whose tag does carry `omitempty` are deliberately left out: encoding/json would have
// dropped them too, so a missing key is the correct result for those.

import (
	"os"
	"reflect"
	"strings"
	"sync/atomic"
)

// includeEmpty is read once per Marshal and captured on the Encoder, so a payload is never
// half-encoded under one setting and half under another. Atomic because tests and runtime config
// flip it while other goroutines are encoding.
var includeEmpty atomic.Bool

func init() {
	includeEmpty.Store(strings.TrimSpace(os.Getenv("MINIJSON_INCLUDE_EMPTY")) == "1")
}

// IncludeEmpty reports whether payloads carry the per-type defaults block. Defaults to the
// MINIJSON_INCLUDE_EMPTY=1 environment variable so it can be switched without a code change.
func IncludeEmpty() bool { return includeEmpty.Load() }

// SetIncludeEmpty overrides the environment default. Takes effect on the next Marshal.
func SetIncludeEmpty(enabled bool) { includeEmpty.Store(enabled) }

// buildDefaultsBlock renders the JSON object of zero-value defaults for a type, or nil when the
// type has no field that needs one.
func buildDefaultsBlock(t reflect.Type) []byte {
	block := appendZeroStruct(make([]byte, 0, 64), t)
	if len(block) == 2 { // "{}" — every field is ignored, unexported or omitempty.
		return nil
	}
	return block
}

// appendZeroStruct writes {"field":<zero>,...} for a struct type's exported, non-ignored,
// non-omitempty fields.
//
// Unexported fields are skipped even though the value path does emit them (see the note in
// appendZeroDefault): their names are implementation details that no consumer can use.
//
// A field whose default renders as an empty object is dropped rather than written. That is what
// keeps an embedded marker struct out of the block: bun.BaseModel is exported, anonymous and has
// no fields, so without this rule every table-backed type would grow a `"BaseModel":{}` key that
// encoding/json never emitted and no client has ever seen. The same rule covers any nested struct
// with nothing to reconstruct.
//
// Note that anonymous fields are treated as ordinary named fields rather than promoted the way
// encoding/json promotes them. That is deliberate: minijson's value path does not promote them
// either, so a non-empty embedded struct arrives nested under its type name when it carries a
// value, and its default has to be shaped the same way to match.
func appendZeroStruct(dst []byte, t reflect.Type) []byte {
	dst = append(dst, '{')

	wroteField := false
	for i := 0; i < t.NumField(); i++ {
		structField := t.Field(i)
		if structField.PkgPath != "" { // unexported
			continue
		}
		name, omitEmpty := parseFieldTag(structField)
		if name == "-" || omitEmpty {
			continue
		}

		fieldStart := len(dst)
		if wroteField {
			dst = append(dst, ',')
		}
		dst = appendJSONString(dst, name)
		dst = append(dst, ':')

		valueStart := len(dst)
		dst = appendZeroDefault(dst, structField.Type)
		if len(dst)-valueStart == 2 && dst[valueStart] == '{' {
			dst = dst[:fieldStart] // rolls back the separator too
			continue
		}

		wroteField = true
	}

	return append(dst, '}')
}

// appendZeroDefault writes the JSON form of a type's zero value.
//
// The reference is what encoding/json would have emitted for that zero value, because matching
// the pre-minijson response byte for byte is the whole point of the block:
//
//   - nil pointers, interfaces, slices and maps all marshal to `null`. A slice or map only reaches
//     this function when it is nil, since reflect's IsZero is IsNil for both — an empty but
//     non-nil one is not zero and is written to the wire normally.
//   - a zero struct marshals to the full object of its own zero fields, so it recurses. Recursion
//     always terminates: Go cannot declare a struct that contains itself by value, and a self
//     reference through a pointer stops at `null`.
//   - a zero array marshals to its elements, so it recurses too.
//
// Two known divergences, both inherited from the value path rather than introduced here:
// encoding/json honours json.Marshaler and skips unexported fields, and minijson's encoder does
// neither. A time.Time field is the case that matters in practice — see TestTimeTimeIsMangled.
func appendZeroDefault(dst []byte, t reflect.Type) []byte {
	switch t.Kind() {
	case reflect.Bool:
		return append(dst, "false"...)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return append(dst, '0')

	case reflect.String:
		return append(dst, '"', '"')

	case reflect.Struct:
		return appendZeroStruct(dst, t)

	case reflect.Array:
		dst = append(dst, '[')
		for i := 0; i < t.Len(); i++ {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendZeroDefault(dst, t.Elem())
		}
		return append(dst, ']')
	}

	// Ptr, Interface, Slice, Map, Chan, Func, UnsafePointer.
	return append(dst, "null"...)
}
