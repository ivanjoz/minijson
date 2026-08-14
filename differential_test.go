package minijson

// The gate that should have caught every bug found so far.
//
// minijson's contract is that a value produces the same JSON through Marshal as it does through
// encoding/json. Three separate bugs shipped because nothing asserted that: embedded fields nested
// under the embedded type's name, time.Time travelled as its private internals, and zero values of
// non-omitempty fields vanished. All three were invisible to the typed Go tests, which decode with
// the destination struct in hand and repair whatever the payload got wrong.
//
// So this decodes with decodeSchemaless — no types, only what the payload describes, the way the
// browser does — and diffs against encoding/json over a table of shapes. Add a shape here whenever
// a new kind of field shows up in a response type.

import (
	"encoding/json"
	"testing"
	"time"
)

type difEscalares struct {
	ID       int32   `json:"id"`
	Codigo   string  `json:"codigo"`
	Cantidad float64 `json:"cantidad"`
	Activo   bool    `json:"activo"`
	Chico    int8    `json:"chico"`
	Sin      uint16  `json:"sin"`
}

type difOpcionales struct {
	ID       int32  `json:"id"`
	Opcional string `json:"opcional,omitempty"`
	Cero     int32  `json:"cero,omitempty"`
	Ignorado string `json:"-"`
	oculto   string //nolint:unused // exercises the unexported-field skip
}

type difPunteros struct {
	ID     int32   `json:"id"`
	Numero *int32  `json:"numero"`
	Texto  *string `json:"texto"`
}

type difColecciones struct {
	ID     int32          `json:"id"`
	Lista  []int32        `json:"lista"`
	Textos []string       `json:"textos"`
	Mapa   map[string]int `json:"mapa"`
	Nada   []int32        `json:"nada"`
}

type difAnidado struct {
	ID      int32        `json:"id"`
	Interno difInterno   `json:"interno"`
	Puntero *difInterno  `json:"puntero"`
	Lista   []difInterno `json:"listaInterna"`
}

type difInterno struct {
	Nombre string `json:"nombre"`
	Valor  int32  `json:"valor"`
}

type difBase struct {
	ID      int32 `json:"id"`
	Empresa int16 `json:"empresaID"`
}

type difEmbebido struct {
	difBase
	Codigo string `json:"codigo"`
}

type difEmbebidoProfundo struct {
	difEmbebido
	Extra string `json:"extra"`
}

type difFechas struct {
	ID     int32      `json:"id"`
	Fecha  time.Time  `json:"fecha"`
	Puntoy *time.Time `json:"fechaOpcional"`
}

func TestDifferentialAgainstEncodingJSON(t *testing.T) {
	unNumero := int32(42)
	unTexto := "hola"
	instante := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)

	casos := []struct {
		nombre string
		valor  any
	}{
		{"escalares poblados", []difEscalares{{1, "A", 1.5, true, 7, 9}}},
		{"escalares en cero", []difEscalares{{}}},
		{"escalares mixtos", []difEscalares{{ID: 1}, {Codigo: "B"}, {Activo: true}, {}}},

		{"omitempty poblado", []difOpcionales{{ID: 1, Opcional: "x", Cero: 3, Ignorado: "no viaja"}}},
		{"omitempty en cero", []difOpcionales{{ID: 1}}},

		{"punteros poblados", []difPunteros{{ID: 1, Numero: &unNumero, Texto: &unTexto}}},
		{"punteros nil", []difPunteros{{ID: 1}}},

		{"colecciones pobladas", []difColecciones{{
			ID: 1, Lista: []int32{1, 2, 3}, Textos: []string{"a", "b"}, Mapa: map[string]int{"k": 1},
		}}},
		{"colecciones vacias no nil", []difColecciones{{
			ID: 1, Lista: []int32{}, Textos: []string{}, Mapa: map[string]int{},
		}}},
		{"colecciones nil", []difColecciones{{ID: 1}}},

		{"anidado poblado", []difAnidado{{
			ID:      1,
			Interno: difInterno{Nombre: "n", Valor: 2},
			Puntero: &difInterno{Nombre: "p", Valor: 3},
			Lista:   []difInterno{{Nombre: "l", Valor: 4}},
		}}},
		{"anidado en cero", []difAnidado{{ID: 1}}},

		{"embebido poblado", []difEmbebido{{difBase: difBase{ID: 1, Empresa: 25}, Codigo: "A"}}},
		{"embebido en cero", []difEmbebido{{}}},
		{"embebido mixto", []difEmbebido{
			{difBase: difBase{ID: 1, Empresa: 25}, Codigo: "A"},
			{difBase: difBase{ID: 2}},
			{Codigo: "C"},
		}},
		{"embebido profundo", []difEmbebidoProfundo{{
			difEmbebido: difEmbebido{difBase: difBase{ID: 1, Empresa: 9}, Codigo: "A"}, Extra: "e",
		}}},
		{"embebido profundo en cero", []difEmbebidoProfundo{{}}},

		{"fechas pobladas", []difFechas{{ID: 1, Fecha: instante, Puntoy: &instante}}},
		{"fechas en cero", []difFechas{{ID: 1}}},

		{"struct suelto no slice", difEmbebido{difBase: difBase{ID: 7, Empresa: 3}, Codigo: "X"}},
		{"slice vacio", []difEscalares{}},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			assertMatchesEncodingJSON(t, caso.valor)
		})
	}
}

// Without the defaults block a zero non-omitempty field is genuinely absent, so the payload cannot
// match encoding/json — that is the documented trade-off, not a bug. What must still hold is that
// everything actually present agrees, i.e. the schemaless decode is a subset of encoding/json's.
func TestDifferentialWithoutDefaultsIsASubset(t *testing.T) {
	SetIncludeEmpty(false)

	valores := []any{
		[]difEscalares{{ID: 1}, {Codigo: "B"}},
		[]difEmbebido{{difBase: difBase{ID: 1}}, {Codigo: "C"}},
		[]difFechas{{ID: 1}},
	}

	for _, valor := range valores {
		payload, err := Marshal(valor)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		referencia, err := json.Marshal(valor)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}

		obtenido, _ := decodeSchemaless(t, payload).([]any)
		var esperado []map[string]any
		if err := json.Unmarshal(referencia, &esperado); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if len(obtenido) != len(esperado) {
			t.Fatalf("%d registros decodificados, se esperaban %d", len(obtenido), len(esperado))
		}

		for i, fila := range obtenido {
			objeto, esObjeto := fila.(map[string]any)
			if !esObjeto {
				t.Fatalf("registro %d no es un objeto: %#v", i, fila)
			}
			for campo, tengo := range objeto {
				quiero, existe := esperado[i][campo]
				if !existe {
					t.Fatalf("registro %d trae %q, que encoding/json no emite", i, campo)
				}
				gotJSON, _ := json.Marshal(tengo)
				wantJSON, _ := json.Marshal(quiero)
				if string(gotJSON) != string(wantJSON) {
					t.Fatalf("registro %d campo %q = %s, encoding/json emite %s", i, campo, gotJSON, wantJSON)
				}
			}
		}
	}
}
