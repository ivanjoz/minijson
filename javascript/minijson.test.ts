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
