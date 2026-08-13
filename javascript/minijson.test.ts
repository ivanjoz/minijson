import { describe, expect, it } from 'bun:test'
import { unmarshal } from './minijson.js'

// Payload shape produced by minijson for a record type whose first field
// in the optimized order is a slice — see skipblock_test.go.
describe('unmarshal', () => {
	it('reads position 1 of a header-0 record as the skip block', () => {
		const keys = [[1, 0, 'tags', 1, 'id', 2, 'name', 3, 'detail']]
		const content = [
			2,
			[1, [1], [2], 313, 'first', 'only the first has this'],
			// Empty skip block: without it `[2]` (an empty slice) would be read as
			// "skip field 2" and every later field would shift by one.
			[0, [], [2], 316, 'second'],
			[0, [3], [2, 2, 6], 317, 'third'],
		]

		expect(unmarshal([keys, content])).toEqual([
			{ tags: [], id: 313, name: 'first', detail: 'only the first has this' },
			{ tags: [], id: 316, name: 'second' },
			{ tags: [2, 6], id: 317, name: 'third' },
		])
	})

	it('reads position 1 as a value when it is not an array', () => {
		const keys = [[1, 0, 'id', 1, 'name']]
		const content = [2, [1, [1], 313, 'first'], [0, 316, 'second']]

		expect(unmarshal([keys, content])).toEqual([
			{ id: 313, name: 'first' },
			{ id: 316, name: 'second' },
		])
	})

	// A response struct whose fields are all zero-valued (e.g. a multi-table delta response with
	// no rows on either side) still emits a type entry with no field pairs — `[typeID]` — so the
	// decoder can tell "empty object of a known type" apart from "bare empty array". Without that
	// entry, `populate` has no typeDef to work from and falls back to returning the raw (empty)
	// values array, which the delta-cache layer then mistakes for an array-shaped route response.
	it('decodes an all-zero-fields struct as an empty object, not a bare array', () => {
		const keys = [[7]]
		const content = [1, [7]]

		expect(unmarshal([keys, content])).toEqual({})
	})

	it('decodes a slice of all-zero-fields structs as objects, not bare arrays', () => {
		const keys = [[7]]
		const content = [2, [1, [7]], [0]]

		expect(unmarshal([keys, content])).toEqual([{}, {}])
	})
})

/*
 * The defaults block, emitted when the Go side runs with MINIJSON_INCLUDE_EMPTY=1.
 *
 * Both payloads below are verbatim output of the Go encoder for the same three records, with the
 * flag off and on, so these tests pin the decoder against the real wire format rather than against
 * a hand-written approximation of it. The Go struct:
 *
 *   ID       int32   `json:"id"`
 *   Codigo   string  `json:"codigo"`
 *   Cantidad float64 `json:"cantidad"`
 *   Activo   bool    `json:"activo"`
 *   Nunca    int64   `json:"nunca"`      // zero in every record
 *   Anidado  Nested  `json:"anidado"`    // Nested{Nombre string `json:"nombre"`; Valor int `json:"valor"`}
 *   Lista    []int   `json:"lista"`
 *   Opcional string  `json:"opcional,omitempty"`
 */
const SIN_DEFAULTS = [
	[
		[1, 0, 'id', 1, 'codigo', 2, 'cantidad', 3, 'activo', 5, 'anidado', 6, 'lista', 7, 'opcional'],
		[2, 0, 'nombre', 1, 'valor'],
	],
	[
		2,
		[1, [1, 4, 5, 6], 1, 'A', 1.5, true, 'x'],
		[0, 2],
		[0, [0, 2, 4], 'C', true, [1, [2], 'n', 7], [2, 1, 2]],
	],
]

const CON_DEFAULTS = [
	[
		[
			1, 0, 'id', 1, 'codigo', 2, 'activo', 3, 'cantidad', 4, 'anidado', 5, 'lista', 6, 'opcional',
			{
				id: 0,
				codigo: '',
				cantidad: 0,
				activo: false,
				nunca: 0,
				anidado: { nombre: '', valor: 0 },
				lista: null,
			},
		],
		[2, 0, 'nombre', 1, 'valor', { nombre: '', valor: 0 }],
	],
	[
		2,
		[1, [1, 4, 5], 1, 'A', true, 1.5, 'x'],
		[0, 2],
		[0, [0, 3], 'C', true, [1, [2], 'n', 7], [2, 1, 2]],
	],
]

describe('unmarshal without the defaults block', () => {
	// A payload from a backend that does not send the block has to decode exactly as it always
	// did: zero-valued fields stay absent. This is the guarantee that lets the flag be switched
	// on the backend without deploying the frontend in lockstep.
	it('leaves zero-valued fields absent', () => {
		expect(unmarshal(SIN_DEFAULTS)).toEqual([
			{ id: 1, codigo: 'A', cantidad: 1.5, activo: true, opcional: 'x' },
			{ id: 2 },
			{ codigo: 'C', activo: true, anidado: { nombre: 'n', valor: 7 }, lista: [1, 2] },
		])
	})
})

describe('unmarshal with the defaults block', () => {
	it('reconstructs the zero values encoding/json would have sent', () => {
		expect(unmarshal(CON_DEFAULTS)).toEqual([
			{
				id: 1,
				codigo: 'A',
				cantidad: 1.5,
				activo: true,
				nunca: 0, // never carried a value, so the keys header never named it
				anidado: { nombre: '', valor: 0 }, // skipped in this record
				lista: null, // a nil slice, which encoding/json writes as null
				opcional: 'x',
			},
			{
				// Everything past `id` is truncated off the end of the record rather than skipped.
				id: 2,
				codigo: '',
				cantidad: 0,
				activo: false,
				nunca: 0,
				anidado: { nombre: '', valor: 0 },
				lista: null,
				// `opcional` is omitempty: encoding/json would have dropped it too, so it stays out.
			},
			{
				id: 0, // skipped
				codigo: 'C',
				cantidad: 0, // skipped
				activo: true,
				nunca: 0,
				anidado: { nombre: 'n', valor: 7 },
				lista: [1, 2],
			},
		])
	})

	it('never gives a key to an omitempty field that was zero', () => {
		const records = unmarshal(CON_DEFAULTS)
		expect('opcional' in records[1]).toBe(false)
		expect('opcional' in records[2]).toBe(false)
	})

	// Every non-omitempty field is written once per record, in the same order, so a payload yields
	// one object shape instead of one per distinct skip set. This is pinned for the predictability
	// it gives consumers — `record.campo` is never `undefined` for a non-omitempty field — not for
	// a measured speed-up: benchmarking found traversal marginally slower, since every record now
	// carries every field.
	it('gives every record the same keys in the same order', () => {
		const records = unmarshal(CON_DEFAULTS)
		const shape = (record: object) => Object.keys(record).filter(k => k !== 'opcional')

		expect(shape(records[1])).toEqual(shape(records[0]))
		expect(shape(records[2])).toEqual(shape(records[0]))
	})

	// A struct or array default is the only non-primitive one. Handing the same object to every
	// record would make a write to one record's field visible in all the others.
	it('does not share a mutable default between records', () => {
		const records = unmarshal(CON_DEFAULTS)

		expect(records[0].anidado).not.toBe(records[1].anidado)
		records[0].anidado.nombre = 'mutado'
		expect(records[1].anidado.nombre).toBe('')
	})

	// An entry with no (position, name) pairs at all: every field of the type was zero in every
	// record, so the block is the only thing that can reconstruct the object.
	it('reconstructs an all-zero-fields struct from the block alone', () => {
		const keys = [[7, { id: 0, codigo: '', activo: false }]]
		const content = [2, [1, [7]], [0]]

		expect(unmarshal([keys, content])).toEqual([
			{ id: 0, codigo: '', activo: false },
			{ id: 0, codigo: '', activo: false },
		])
	})

	// Field names are Go json tags, but the lookup still must not resolve against Object.prototype.
	it('does not pick up prototype members as defaults', () => {
		const keys = [[9, 0, 'id', { id: 0 }]]
		const content = [1, [9, 0]]

		const decoded = unmarshal([keys, content])
		expect(decoded).toEqual({ id: 0 })
		expect('constructor' in decoded && decoded.constructor === Object).toBe(true)
	})
})
