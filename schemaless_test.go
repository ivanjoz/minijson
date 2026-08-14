package minijson

// A Go mirror of javascript/minijson.ts, for tests only.
//
// Every bug found in minijson so far — embedded fields nesting under the type name, time.Time
// arriving as its internals, zero values reading back as undefined — was invisible to the Go tests
// because Go decodes with the destination struct in hand and quietly repairs whatever the payload
// got wrong. Only a decoder without types sees what the browser sees.
//
// So this walks a payload using nothing but the payload's own keys header, exactly as the
// TypeScript decoder does, which lets a Go test assert the real contract: that a struct produces
// the same JSON value through minijson as it does through encoding/json.
//
// It deliberately does not copy the TypeScript decoder's defensive cloning of non-primitive
// defaults — nothing here mutates the result, and object identity is a JavaScript concern that
// javascript/minijson.test.ts already covers.

import (
	"encoding/json"
	"testing"
)

type schemalessType struct {
	slotNames    []string
	slotDefaults []any
	hasDefault   []bool
	tailNames    []string
	tailDefaults []any
}

// decodeSchemaless decodes a minijson payload with no type information, returning plain Go values
// shaped the way json.Unmarshal into an `any` would shape them.
func decodeSchemaless(t *testing.T, payload []byte) any {
	t.Helper()

	var outer []any
	if err := json.Unmarshal(payload, &outer); err != nil {
		t.Fatalf("payload is not valid JSON: %v\n%s", err, payload)
	}
	if len(outer) != 2 {
		t.Fatalf("expected [keys, content], got %d elements: %s", len(outer), payload)
	}

	keyEntries, isArray := outer[0].([]any)
	if !isArray {
		t.Fatalf("keys header is not an array: %s", payload)
	}

	types := map[int]*schemalessType{}
	for _, rawEntry := range keyEntries {
		entry, isEntryArray := rawEntry.([]any)
		if !isEntryArray || len(entry) == 0 {
			continue
		}
		typeIDFloat, isNumber := entry[0].(float64)
		if !isNumber {
			continue
		}

		decoded := &schemalessType{}
		for i := 1; i+1 < len(entry); i += 2 {
			orderIdx, isOrderNumber := entry[i].(float64)
			name, isName := entry[i+1].(string)
			if !isOrderNumber || !isName {
				continue
			}
			for len(decoded.slotNames) <= int(orderIdx) {
				decoded.slotNames = append(decoded.slotNames, "")
			}
			decoded.slotNames[int(orderIdx)] = name
		}

		// Optional trailing defaults block: the only element of an entry that is a JSON object.
		var defaults map[string]any
		if len(entry) > 1 {
			if block, isObject := entry[len(entry)-1].(map[string]any); isObject {
				defaults = block
			}
		}

		decoded.slotDefaults = make([]any, len(decoded.slotNames))
		decoded.hasDefault = make([]bool, len(decoded.slotNames))
		if defaults != nil {
			named := map[string]bool{}
			for i, name := range decoded.slotNames {
				if name == "" {
					continue
				}
				named[name] = true
				if value, found := defaults[name]; found {
					decoded.slotDefaults[i] = value
					decoded.hasDefault[i] = true
				}
			}
			// Fields with a default but no position were zero in every record of the payload, so
			// the keys header never named them.
			for name, value := range defaults {
				if named[name] {
					continue
				}
				decoded.tailNames = append(decoded.tailNames, name)
				decoded.tailDefaults = append(decoded.tailDefaults, value)
			}
		}

		types[int(typeIDFloat)] = decoded
	}

	lastTypeID := -1
	var decode func(value any) any

	populate := func(typeID int, record []any, valuesFrom int, skips []any) any {
		decoded, found := types[typeID]
		if !found {
			return record[valuesFrom:]
		}

		object := map[string]any{}
		valueIdx := valuesFrom
		skipIdx := 0

		for i, name := range decoded.slotNames {
			hasValue := true
			if skipIdx < len(skips) {
				if skipped, isNumber := skips[skipIdx].(float64); isNumber && int(skipped) == i {
					skipIdx++
					hasValue = false
				}
			}
			if hasValue && valueIdx >= len(record) {
				// Zero fields at the tail of a record are truncated rather than skipped.
				hasValue = false
			}

			if hasValue {
				if name != "" {
					object[name] = decode(record[valueIdx])
				}
				valueIdx++
				continue
			}
			if name != "" && decoded.hasDefault[i] {
				object[name] = decoded.slotDefaults[i]
			}
		}

		for i, name := range decoded.tailNames {
			object[name] = decoded.tailDefaults[i]
		}
		return object
	}

	decode = func(value any) any {
		arr, isArray := value.([]any)
		if !isArray || len(arr) == 0 {
			return value
		}
		header, isNumber := arr[0].(float64)
		if !isNumber {
			result := make([]any, len(arr))
			for i, item := range arr {
				result[i] = decode(item)
			}
			return result
		}

		switch int(header) {
		case 1:
			if len(arr) < 2 {
				return arr
			}
			refBlock, isRefArray := arr[1].([]any)
			if !isRefArray || len(refBlock) == 0 {
				return arr
			}
			typeIDFloat, isTypeNumber := refBlock[0].(float64)
			if !isTypeNumber {
				return arr
			}
			lastTypeID = int(typeIDFloat)
			return populate(lastTypeID, arr, 2, refBlock[1:])

		case 0:
			if lastTypeID < 0 {
				return arr
			}
			// Position 1 of a header-0 record is the skip block whenever it is an array.
			if len(arr) > 1 {
				if skips, isSkipArray := arr[1].([]any); isSkipArray {
					return populate(lastTypeID, arr, 2, skips)
				}
			}
			return populate(lastTypeID, arr, 1, nil)

		case 2:
			result := []any{}
			for _, item := range arr[1:] {
				result = append(result, decode(item))
			}
			return result

		case 3:
			result := map[string]any{}
			for i := 1; i+1 < len(arr); i += 2 {
				key, isString := arr[i].(string)
				if !isString {
					continue
				}
				result[key] = decode(arr[i+1])
			}
			return result
		}

		return value
	}

	return decode(outer[1])
}
