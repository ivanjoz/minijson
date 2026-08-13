package minijson

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Each test uses its own struct type: the registry freezes a field order per type for the whole
// process, so sharing a type across tests would make them order-dependent.

// keysEntry returns the keys-header entry of a payload's first type, decoded.
func keysEntry(t *testing.T, payload []byte) []any {
	t.Helper()

	var outer []any
	if err := json.Unmarshal(payload, &outer); err != nil {
		t.Fatalf("payload is not valid JSON: %v\n%s", err, payload)
	}
	if len(outer) != 2 {
		t.Fatalf("expected [keys, content], got %d elements", len(outer))
	}
	keys, isArray := outer[0].([]any)
	if !isArray || len(keys) == 0 {
		t.Fatalf("keys header is empty: %s", payload)
	}
	entry, isArray := keys[0].([]any)
	if !isArray {
		t.Fatalf("keys entry is not an array: %s", payload)
	}
	return entry
}

// defaultsOf returns the trailing defaults block of a payload's first type entry, or nil.
func defaultsOf(t *testing.T, payload []byte) map[string]any {
	t.Helper()

	entry := keysEntry(t, payload)
	if len(entry) < 2 {
		return nil
	}
	block, isObject := entry[len(entry)-1].(map[string]any)
	if !isObject {
		return nil
	}
	return block
}

type escalares struct {
	ID       int32   `json:"id"`
	Codigo   string  `json:"codigo"`
	Cantidad float64 `json:"cantidad"`
	Activo   bool    `json:"activo"`
}

// Off by default, and off means the wire format is exactly what it was before this existed.
func TestDefaultsBlockAbsentByDefault(t *testing.T) {
	if IncludeEmpty() {
		t.Fatal("MINIJSON_INCLUDE_EMPTY must default to off")
	}

	payload, err := Marshal([]escalares{{ID: 1}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if block := defaultsOf(t, payload); block != nil {
		t.Fatalf("defaults block emitted with the flag off: %s", payload)
	}
	if strings.ContainsRune(string(payload), '{') {
		t.Fatalf("payload carries an object with the flag off: %s", payload)
	}
}

type escalaresConFlag struct {
	ID       int32   `json:"id"`
	Codigo   string  `json:"codigo"`
	Cantidad float64 `json:"cantidad"`
	Activo   bool    `json:"activo"`
}

func TestDefaultsBlockCarriesZeroValues(t *testing.T) {
	SetIncludeEmpty(true)
	defer SetIncludeEmpty(false)

	payload, err := Marshal([]escalaresConFlag{{ID: 1, Codigo: "A"}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	block := defaultsOf(t, payload)
	if block == nil {
		t.Fatalf("no defaults block: %s", payload)
	}

	esperado := map[string]any{"id": float64(0), "codigo": "", "cantidad": float64(0), "activo": false}
	if len(block) != len(esperado) {
		t.Fatalf("block has %d fields, expected %d: %v", len(block), len(esperado), block)
	}
	for name, want := range esperado {
		got, found := block[name]
		if !found {
			t.Fatalf("field %q missing from the block: %v", name, block)
		}
		if got != want {
			t.Fatalf("default for %q = %#v, expected %#v", name, got, want)
		}
	}
}

type conOmitEmpty struct {
	ID       int32  `json:"id"`
	Opcional string `json:"opcional,omitempty"`
	Vacio    int    `json:",omitempty"`
	Ignorado string `json:"-"`
	privado  string //nolint:unused // exercises the unexported-field skip
}

// omitempty fields must stay out of the block: encoding/json would have dropped them too, so an
// absent key is the correct result. Ignored and unexported fields likewise.
func TestDefaultsBlockSkipsOmitEmptyIgnoredAndUnexported(t *testing.T) {
	SetIncludeEmpty(true)
	defer SetIncludeEmpty(false)

	payload, err := Marshal([]conOmitEmpty{{ID: 1}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	block := defaultsOf(t, payload)
	if block == nil {
		t.Fatalf("no defaults block: %s", payload)
	}
	if len(block) != 1 {
		t.Fatalf("expected only \"id\" in the block, got %v", block)
	}
	if _, found := block["id"]; !found {
		t.Fatalf("expected \"id\" in the block, got %v", block)
	}
}

type sinDefaults struct {
	Opcional string `json:"opcional,omitempty"`
	Otro     int    `json:"otro,omitempty"`
}

// A type where every field is omitempty needs no block, and must not get an empty one.
func TestDefaultsBlockOmittedWhenEmpty(t *testing.T) {
	SetIncludeEmpty(true)
	defer SetIncludeEmpty(false)

	payload, err := Marshal([]sinDefaults{{Opcional: "x"}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if block := defaultsOf(t, payload); block != nil {
		t.Fatalf("empty block emitted: %s", payload)
	}
}

type compuestos struct {
	ID       int32          `json:"id"`
	Anidado  interno        `json:"anidado"`
	Puntero  *interno       `json:"puntero"`
	Lista    []int          `json:"lista"`
	Mapa     map[string]int `json:"mapa"`
	Fijo     [3]int         `json:"fijo"`
	Cualqui  any            `json:"cualquiera"`
	Opcional string         `json:"opcional,omitempty"`
}

type interno struct {
	Nombre string `json:"nombre"`
	Valor  int    `json:"valor"`
	Salta  string `json:"salta,omitempty"`
}

// The defaults have to match what encoding/json would have emitted for the zero value, since
// reproducing the pre-minijson response is the whole point.
func TestDefaultsBlockMatchesEncodingJSONForCompoundKinds(t *testing.T) {
	SetIncludeEmpty(true)
	defer SetIncludeEmpty(false)

	payload, err := Marshal([]compuestos{{ID: 1}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	block := defaultsOf(t, payload)
	if block == nil {
		t.Fatalf("no defaults block: %s", payload)
	}

	// A nil slice, map, pointer and interface all marshal to null. A slice or map only ever
	// reaches the block when nil, since reflect's IsZero is IsNil for both.
	for _, name := range []string{"puntero", "lista", "mapa", "cualquiera"} {
		got, found := block[name]
		if !found {
			t.Fatalf("field %q missing from the block: %v", name, block)
		}
		if got != nil {
			t.Fatalf("default for %q = %#v, expected null", name, got)
		}
	}

	// A zero struct marshals to the object of its own zero fields, minus its omitempty ones.
	anidado, isObject := block["anidado"].(map[string]any)
	if !isObject {
		t.Fatalf("default for \"anidado\" is not an object: %#v", block["anidado"])
	}
	if len(anidado) != 2 || anidado["nombre"] != "" || anidado["valor"] != float64(0) {
		t.Fatalf("default for \"anidado\" = %#v, expected {nombre:\"\", valor:0}", anidado)
	}

	// A zero array marshals to its elements.
	fijo, isArray := block["fijo"].([]any)
	if !isArray || len(fijo) != 3 {
		t.Fatalf("default for \"fijo\" = %#v, expected a 3-element array", block["fijo"])
	}

	if _, found := block["opcional"]; found {
		t.Fatalf("omitempty field leaked into the block: %v", block)
	}

	// Cross-check the scalar and compound defaults against the real thing.
	referencia, err := json.Marshal(compuestos{})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var esperado map[string]any
	if err := json.Unmarshal(referencia, &esperado); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	delete(esperado, "opcional") // encoding/json drops it too, but only because it is zero here.
	for name, want := range esperado {
		got := block[name]
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("default for %q = %s, encoding/json emits %s", name, gotJSON, wantJSON)
		}
	}
}

// BaseModelPrueba replicates bun.BaseModel: exported, embedded, no fields, tagged for something
// other than json. Every table-backed struct in a Bun codebase embeds one.
type BaseModelPrueba struct{}

type conBaseModel struct {
	BaseModelPrueba `bun:"table:productos,alias:p"`
	ID              int32  `json:"id"`
	Codigo          string `json:"codigo"`
}

// The regression this rule exists for: an embedded marker struct is exported, so it is not filtered
// as unexported, and it is always zero, so the value path never writes it. Left alone it would add
// a `"BaseModelPrueba":{}` key to every table-backed type's defaults — a field encoding/json never
// emitted and no client has ever seen.
func TestDefaultsBlockSkipsEmptyEmbeddedStructs(t *testing.T) {
	SetIncludeEmpty(true)
	defer SetIncludeEmpty(false)

	payload, err := Marshal([]conBaseModel{{ID: 1}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	block := defaultsOf(t, payload)
	if _, found := block["BaseModelPrueba"]; found {
		t.Fatalf("the embedded marker struct leaked into the block: %v", block)
	}

	// And the block still describes exactly the keys encoding/json would have written.
	referencia, err := json.Marshal(conBaseModel{})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var esperado map[string]any
	if err := json.Unmarshal(referencia, &esperado); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(block) != len(esperado) {
		t.Fatalf("block = %v, encoding/json emits %v", block, esperado)
	}
	for name := range esperado {
		if _, found := block[name]; !found {
			t.Fatalf("field %q missing from the block: %v", name, block)
		}
	}
}

type siempreCero struct {
	ID     int32  `json:"id"`
	Nunca  int64  `json:"nunca"`
	Tampoc string `json:"tampoco"`
}

// The trap this block exists to avoid: a field that is zero in *every* record of a payload gets no
// position in the keys header at all, because the header only names positions that carried a
// value. Deriving the block from that usage mask would drop exactly the fields most likely to need
// a default.
func TestDefaultsBlockCoversFieldsZeroInEveryRecord(t *testing.T) {
	SetIncludeEmpty(true)
	defer SetIncludeEmpty(false)

	payload, err := Marshal([]siempreCero{{ID: 1}, {ID: 2}, {ID: 3}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Sanity check on the premise: if the header did name the field, the test proves nothing.
	for _, name := range pairNames(keysEntry(t, payload)) {
		if name == "nunca" || name == "tampoco" {
			t.Fatalf("premise broken — the header named always-zero %q: %s", name, payload)
		}
	}

	block := defaultsOf(t, payload)
	for _, name := range []string{"nunca", "tampoco"} {
		if _, found := block[name]; !found {
			t.Fatalf("always-zero field %q has no default: %v", name, block)
		}
	}
}

// pairNames returns the field names a keys-header entry actually named through its
// (position, name) pairs, excluding the trailing defaults block.
func pairNames(entry []any) []string {
	names := []string{}
	for i := 1; i+1 < len(entry); i += 2 {
		if name, isString := entry[i+1].(string); isString {
			names = append(names, name)
		}
	}
	return names
}

type roundTrip struct {
	ID       int32   `json:"id"`
	Codigo   string  `json:"codigo"`
	Cantidad float64 `json:"cantidad"`
	Activo   bool    `json:"activo"`
	Opcional string  `json:"opcional,omitempty"`
}

// The Go decoder walks (position, name) pairs and stops at the last complete one, so the trailing
// block is never read as a pair. Typed decoding has to be unaffected by the flag.
func TestGoRoundTripUnaffectedByDefaultsBlock(t *testing.T) {
	registros := []roundTrip{
		{ID: 1, Codigo: "A", Cantidad: 1.5, Activo: true, Opcional: "x"},
		{ID: 2},
		{Codigo: "C", Activo: true},
	}

	for _, conFlag := range []bool{false, true} {
		SetIncludeEmpty(conFlag)

		payload, err := Marshal(registros)
		if err != nil {
			t.Fatalf("flag=%v Marshal: %v", conFlag, err)
		}
		if tieneBloque := defaultsOf(t, payload) != nil; tieneBloque != conFlag {
			t.Fatalf("flag=%v but block present = %v: %s", conFlag, tieneBloque, payload)
		}

		var decodificado []roundTrip
		if err := Unmarshal(payload, &decodificado); err != nil {
			t.Fatalf("flag=%v Unmarshal: %v\n%s", conFlag, err, payload)
		}
		if len(decodificado) != len(registros) {
			t.Fatalf("flag=%v decoded %d records, expected %d", conFlag, len(decodificado), len(registros))
		}
		for i := range registros {
			if decodificado[i] != registros[i] {
				t.Fatalf("flag=%v record %d differs:\n  got:  %+v\n  want: %+v",
					conFlag, i, decodificado[i], registros[i])
			}
		}
	}
	SetIncludeEmpty(false)
}

// The block only lengthens the keys header, never the content, so its cost does not scale with the
// record count.
func TestDefaultsBlockDoesNotGrowWithRecordCount(t *testing.T) {
	registros := make([]escalares, 500)
	for i := range registros {
		registros[i].ID = int32(i + 1)
	}

	SetIncludeEmpty(false)
	sinBloque, err := Marshal(registros)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	SetIncludeEmpty(true)
	defer SetIncludeEmpty(false)
	conBloque, err := Marshal(registros)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	crecimiento := len(conBloque) - len(sinBloque)
	t.Logf("500 records | without=%d bytes | with=%d bytes | +%d bytes",
		len(sinBloque), len(conBloque), crecimiento)
	if crecimiento > 128 {
		t.Fatalf("the block grew the payload by %d bytes, expected a per-type constant", crecimiento)
	}
}

type conFechaValor struct {
	ID    int32     `json:"id"`
	Fecha time.Time `json:"fecha"`
}

// KNOWN LIMITATION, pre-existing and independent of the defaults block.
//
// minijson's encoder does not honour json.Marshaler and does not skip unexported fields, so a
// time.Time field is written as its private internals instead of an RFC 3339 string, and typed
// decoding of it panics inside reflect. This test pins that behaviour so the day it is fixed the
// fix is deliberate rather than accidental — and so the divergence is discoverable from the tests
// rather than from a broken screen.
//
// Consequence for the defaults block: the default for a time.Time field is `{}`, since the type
// has no exported field. That is wrong in the same way the value path is wrong, not more.
func TestTimeTimeIsMangled(t *testing.T) {
	registros := []conFechaValor{{ID: 1, Fecha: time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)}}

	payload, err := Marshal(registros)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(payload), "2026-08-13T10:00:00Z") {
		t.Fatalf("time.Time now round-trips as RFC 3339 — update this test and the notes in "+
			"defaults.go, the limitation is fixed: %s", payload)
	}

	var decodificado []conFechaValor
	panico := func() (recovered any) {
		defer func() { recovered = recover() }()
		_ = Unmarshal(payload, &decodificado)
		return nil
	}()
	if panico == nil {
		t.Fatal("typed decoding of a time.Time field no longer panics — update this test")
	}
}
