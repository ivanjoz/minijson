package minijson

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Promotion is checked against encoding/json rather than against hand-written expectations: the
// contract is that the same struct produces the same JSON value through either path, so
// encoding/json is the oracle.
//
// assertMatchesEncodingJSON decodes the minijson payload with the schemaless rules the browser
// decoder uses and compares the result to json.Marshal's, field by field. It is the strongest
// statement available in Go about what the frontend will actually see.
func assertMatchesEncodingJSON(t *testing.T, value any) {
	t.Helper()

	SetIncludeEmpty(true)
	defer SetIncludeEmpty(false)

	payload, err := Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	referencia, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	obtenido := decodeSchemaless(t, payload)
	var esperado any
	if err := json.Unmarshal(referencia, &esperado); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if !reflect.DeepEqual(obtenido, esperado) {
		gotJSON, _ := json.MarshalIndent(obtenido, "", "  ")
		wantJSON, _ := json.MarshalIndent(esperado, "", "  ")
		t.Fatalf("minijson decoded differs from encoding/json:\n got:\n%s\n want:\n%s\n payload: %s",
			gotJSON, wantJSON, payload)
	}
}

type conBase struct {
	Base
	Codigo string `json:"codigo"`
}

type Base struct {
	ID       int32  `json:"id"`
	Empresa  int16  `json:"empresaID"`
	Contrato int32  `json:"contratoID"`
	Nota     string `json:"nota,omitempty"`
}

// The bug this file exists for: with an anonymous embedded struct, minijson used to nest the
// embedded fields under a literal "Base" key, so item.contratoID read back as undefined and any
// frontend filter on it silently dropped every row.
func TestEmbeddedFieldsArePromoted(t *testing.T) {
	registros := []conBase{
		{Base: Base{ID: 857, Empresa: 25, Contrato: 2}, Codigo: "E-001"},
		{Base: Base{ID: 858}, Codigo: "E-002"},
	}

	SetIncludeEmpty(false)
	payload, err := Marshal(registros)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// The embedded type's name must not appear anywhere in the payload.
	if entrada := keysEntry(t, payload); containsName(entrada, "Base") {
		t.Fatalf("the embedded type is still a field in the keys header: %s", payload)
	}
	for _, campo := range []string{"id", "empresaID", "contratoID", "codigo"} {
		if !containsName(keysEntry(t, payload), campo) {
			t.Fatalf("promoted field %q missing from the keys header: %s", campo, payload)
		}
	}

	assertMatchesEncodingJSON(t, registros)
}

func TestEmbeddedRoundTripsTyped(t *testing.T) {
	registros := []conBase{
		{Base: Base{ID: 857, Empresa: 25, Contrato: 2, Nota: "x"}, Codigo: "E-001"},
		{Base: Base{ID: 858}},
	}

	payload, err := Marshal(registros)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decodificado []conBase
	if err := Unmarshal(payload, &decodificado); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(decodificado, registros) {
		t.Fatalf("typed round trip differs:\n got: %+v\n want: %+v", decodificado, registros)
	}
}

type conBaseAnidada struct {
	Nivel2
	Propio string `json:"propio"`
}

type Nivel2 struct {
	Nivel3
	Medio string `json:"medio"`
}

type Nivel3 struct {
	Hondo string `json:"hondo"`
}

// Promotion is transitive: encoding/json flattens through any depth of embedding.
func TestPromotionIsTransitive(t *testing.T) {
	assertMatchesEncodingJSON(t, []conBaseAnidada{
		{Nivel2: Nivel2{Nivel3: Nivel3{Hondo: "h"}, Medio: "m"}, Propio: "p"},
	})
}

type conPunteroEmbebido struct {
	*Base
	Codigo string `json:"codigo"`
}

// An embedded pointer promotes its fields too, and a nil one has to be survivable: encoding/json
// omits the promoted fields entirely in that case.
func TestEmbeddedPointerNilIsSurvivable(t *testing.T) {
	registros := []conPunteroEmbebido{
		{Base: &Base{ID: 1, Contrato: 7}, Codigo: "A"},
		{Base: nil, Codigo: "B"},
	}

	payload, err := Marshal(registros)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	decodificado := decodeSchemaless(t, payload)
	filas, esArray := decodificado.([]any)
	if !esArray || len(filas) != 2 {
		t.Fatalf("expected 2 records, got %#v", decodificado)
	}
	primera, _ := filas[0].(map[string]any)
	if primera["contratoID"] != float64(7) || primera["codigo"] != "A" {
		t.Fatalf("the non-nil record lost its promoted fields: %#v", primera)
	}
	segunda, _ := filas[1].(map[string]any)
	if segunda["codigo"] != "B" {
		t.Fatalf("the nil-embedded record lost its own field: %#v", segunda)
	}

	// And typed decoding allocates the embedded pointer on the way back in.
	var tipado []conPunteroEmbebido
	if err := Unmarshal(payload, &tipado); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if tipado[0].Base == nil || tipado[0].Base.Contrato != 7 {
		t.Fatalf("the embedded pointer was not restored: %+v", tipado[0])
	}
}

type conEmbebidoNombrado struct {
	Base `json:"base"`
	Otro string `json:"otro"`
}

// An anonymous field with an explicit json name is NOT promoted — encoding/json nests it under that
// name, and minijson has to agree.
func TestEmbeddedWithExplicitNameIsNotPromoted(t *testing.T) {
	assertMatchesEncodingJSON(t, []conEmbebidoNombrado{
		{Base: Base{ID: 3, Contrato: 9}, Otro: "o"},
	})
}

type conEmbebidoIgnorado struct {
	Base `json:"-"`
	Otro string `json:"otro"`
}

func TestEmbeddedIgnoredIsDropped(t *testing.T) {
	assertMatchesEncodingJSON(t, []conEmbebidoIgnorado{
		{Base: Base{ID: 3}, Otro: "o"},
	})
}

type sombraExterna struct {
	Base
	// Declared at depth 0, so it shadows Base's "contratoID" at depth 1.
	Contrato string `json:"contratoID"`
}

// encoding/json's shadowing rule: the shallower declaration wins outright.
func TestShallowerFieldShadowsPromotedOne(t *testing.T) {
	assertMatchesEncodingJSON(t, []sombraExterna{
		{Base: Base{ID: 1, Contrato: 99}, Contrato: "gana el de arriba"},
	})
}

type ChoqueA struct {
	Choque string `json:"choque"`
	Solo1  int32  `json:"solo1"`
}

type ChoqueB struct {
	Choque string `json:"choque"`
	Solo2  int32  `json:"solo2"`
}

// Two embedded structs declaring the same name at the same depth: encoding/json drops the ambiguous
// name and keeps the unambiguous ones.
//
// The struct is built at run time because `go vet`'s structtag check rejects a source-level type
// that embeds two structs sharing a json tag — which is exactly the shape under test.
func TestSameDepthConflictDropsBothSides(t *testing.T) {
	tipo := reflect.StructOf([]reflect.StructField{
		{Name: "ChoqueA", Type: reflect.TypeOf(ChoqueA{}), Anonymous: true},
		{Name: "ChoqueB", Type: reflect.TypeOf(ChoqueB{}), Anonymous: true},
	})

	valor := reflect.New(tipo).Elem()
	valor.Field(0).Set(reflect.ValueOf(ChoqueA{Choque: "a", Solo1: 1}))
	valor.Field(1).Set(reflect.ValueOf(ChoqueB{Choque: "b", Solo2: 2}))

	referencia, err := json.Marshal(valor.Interface())
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var esperado map[string]any
	if err := json.Unmarshal(referencia, &esperado); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if _, found := esperado["choque"]; found {
		t.Fatalf("premise broken — encoding/json kept the ambiguous name: %s", referencia)
	}

	promovidos := map[string]bool{}
	for _, field := range promotedFields(tipo) {
		promovidos[field.Name] = true
	}
	if len(promovidos) != len(esperado) {
		t.Fatalf("promoted %v, encoding/json emits %v", promovidos, esperado)
	}
	for name := range esperado {
		if !promovidos[name] {
			t.Fatalf("encoding/json emits %q but promotion dropped it: %v", name, promovidos)
		}
	}
}

type conMarcadorVacio struct {
	marcador
	ID int32 `json:"id"`
}

type marcador struct{}

// The bun.BaseModel shape: an embedded struct with no fields contributes nothing at all, rather
// than a `"marcador":{}` key.
func TestEmptyEmbeddedStructContributesNothing(t *testing.T) {
	fields := promotedFields(reflect.TypeOf(conMarcadorVacio{}))
	if len(fields) != 1 || fields[0].Name != "id" {
		t.Fatalf("expected only \"id\", got %+v", fields)
	}
	assertMatchesEncodingJSON(t, []conMarcadorVacio{{ID: 5}})
}

type autoReferente struct {
	*autoReferente
	ID int32 `json:"id"`
}

// A type embedding a pointer to itself must not send promotion into an infinite loop.
func TestSelfEmbeddingTerminates(t *testing.T) {
	fields := promotedFields(reflect.TypeOf(autoReferente{}))
	if len(fields) != 1 || fields[0].Name != "id" {
		t.Fatalf("expected only \"id\", got %+v", fields)
	}
}

// containsName reports whether a keys-header entry names a field, looking only at its
// (position, name) pairs and not at the trailing defaults block.
func containsName(entry []any, name string) bool {
	for _, got := range pairNames(entry) {
		if got == name {
			return true
		}
	}
	return false
}
