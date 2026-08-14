package minijson

// Field promotion for embedded structs.
//
// encoding/json flattens an anonymous embedded struct into its parent: given
//
//	type Item struct {
//	    Base                 // anonymous, no json tag
//	    Codigo string `json:"codigo"`
//	}
//
// it writes Base's fields at the top level, alongside "codigo". minijson used to walk NumField()
// directly and treat Base as an ordinary field named "Base", so the same struct arrived nested
// under a literal "Base" key and every promoted field read back as undefined on a schemaless
// decoder. That is what this file fixes.
//
// Promotion means a field is no longer addressable by a single index, so FieldInfo carries an
// index path instead: [2] for a directly declared field, [0, 1] for the second field of the first
// embedded struct. fieldByIndexPath and fieldByIndexPathAlloc walk those paths on the encode and
// decode sides respectively.

import (
	"reflect"
	"strings"
)

// promotedFields returns a type's serializable fields in wire order, with anonymous embedded
// structs flattened the way encoding/json flattens them.
//
// The traversal is breadth-first by embedding depth, which is what makes the conflict rules below
// expressible: a name declared closer to the surface wins outright over a deeper one.
func promotedFields(t reflect.Type) []FieldInfo {
	var fields []FieldInfo

	// current/next hold the structs to walk at this depth and the next one. visited guards against
	// a type embedding a pointer to itself, which would otherwise recurse forever.
	type queued struct {
		typ  reflect.Type
		path []int
	}
	current := []queued{}
	next := []queued{{typ: t}}
	visited := map[reflect.Type]bool{}

	for len(next) > 0 {
		current, next = next, current[:0]

		// Depth-local record of which names this level declared, so a same-level conflict can be
		// detected and resolved without looking at the fields already accepted at shallower levels.
		firstAtThisDepth := len(fields)

		for _, item := range current {
			if visited[item.typ] {
				continue
			}
			visited[item.typ] = true

			for i := 0; i < item.typ.NumField(); i++ {
				structField := item.typ.Field(i)

				embeddedType := structField.Type
				if embeddedType.Kind() == reflect.Ptr {
					embeddedType = embeddedType.Elem()
				}

				jsonTag := structField.Tag.Get("json")
				hasExplicitName := taggedName(jsonTag) != ""

				if structField.Anonymous && !hasExplicitName && embeddedType.Kind() == reflect.Struct {
					// An embedded struct without an explicit json name is flattened, not written.
					// This holds even when the embedded type itself is unexported, because its
					// exported fields are still reachable — which is how bun.BaseModel disappears
					// and how ProcesoAlta's fields reach the top level.
					if jsonTag == "-" {
						continue
					}
					next = append(next, queued{typ: embeddedType, path: appendPath(item.path, i)})
					continue
				}

				// Unexported fields cannot be read through an interface, and encoding/json ignores
				// them. minijson used to emit them, which is how a time.Time turned into its
				// private internals; see TestTimeTimeIsMangled.
				if structField.PkgPath != "" {
					continue
				}

				name, omitEmpty := parseFieldTag(structField)
				if name == "-" {
					continue
				}

				fields = append(fields, FieldInfo{
					Name:      name,
					RawName:   structField.Name,
					OmitEmpty: omitEmpty,
					IndexPath: appendPath(item.path, i),
					Type:      structField.Type,
					depth:     len(item.path),
					tagged:    hasExplicitName,
				})
			}
		}

		fields = resolveConflicts(fields, firstAtThisDepth)
	}

	// Resolve everything that depends only on the type here, once, so neither the encoder nor the
	// decoder repeats it per record.
	for i := range fields {
		field := &fields[i]
		field.Index = i
		field.direct = -1
		if len(field.IndexPath) == 1 {
			field.direct = field.IndexPath[0]
		}
		field.codec = staticCodecFor(field.Type)
		field.decoder = unmarshalerFor(field.Type)
	}
	return fields
}

// resolveConflicts applies encoding/json's shadowing rules to the fields accepted so far.
//
// Two rules, in order:
//   - a name already accepted at a shallower depth wins, so the deeper one is dropped;
//   - among fields that tie at the same depth, an explicitly tagged one wins over untagged ones;
//     if that still leaves more than one, they all drop, because there is no defensible pick.
//
// newFrom marks where the current depth's fields start, so everything before it is already
// resolved and only needs to be consulted for the shallower-wins rule.
func resolveConflicts(fields []FieldInfo, newFrom int) []FieldInfo {
	if newFrom >= len(fields) {
		return fields
	}

	shallower := make(map[string]bool, newFrom)
	for _, field := range fields[:newFrom] {
		shallower[field.Name] = true
	}

	countAtThisDepth := make(map[string]int, len(fields)-newFrom)
	taggedAtThisDepth := make(map[string]int, len(fields)-newFrom)
	for _, field := range fields[newFrom:] {
		countAtThisDepth[field.Name]++
		if field.tagged {
			taggedAtThisDepth[field.Name]++
		}
	}

	kept := fields[:newFrom]
	for _, field := range fields[newFrom:] {
		if shallower[field.Name] {
			continue
		}
		if countAtThisDepth[field.Name] > 1 {
			// Ambiguous at this depth: only a single tagged field breaks the tie.
			if taggedAtThisDepth[field.Name] != 1 || !field.tagged {
				continue
			}
		}
		kept = append(kept, field)
	}
	return kept
}

// taggedName returns the explicit name from a json tag, or "" when the tag gives none. A `json:"-"`
// tag names nothing; it suppresses the field, which the caller handles separately.
func taggedName(jsonTag string) string {
	if jsonTag == "" || jsonTag == "-" {
		return ""
	}
	if idx := strings.Index(jsonTag, ","); idx != -1 {
		return jsonTag[:idx]
	}
	return jsonTag
}

// appendPath returns base extended by one index, always as a fresh slice. Copying rather than
// appending in place matters: sibling fields share the same base path, and append would have them
// overwrite each other whenever the base had spare capacity.
func appendPath(base []int, index int) []int {
	path := make([]int, len(base)+1)
	copy(path, base)
	path[len(base)] = index
	return path
}

// fieldByIndexPath resolves a field for reading.
//
// Returns the zero Value when the path crosses a nil embedded pointer. The encoder treats that as
// "carries nothing" and skips the field, which is the closest analogue to encoding/json omitting
// the promoted fields of a nil embedded pointer outright.
func fieldByIndexPath(val reflect.Value, path []int) reflect.Value {
	if len(path) == 1 {
		return val.Field(path[0])
	}
	for depth, index := range path {
		if depth > 0 && val.Kind() == reflect.Ptr {
			if val.IsNil() {
				return reflect.Value{}
			}
			val = val.Elem()
		}
		val = val.Field(index)
	}
	return val
}

// fieldByIndexPathAlloc resolves a field for writing, allocating any nil embedded pointer it has
// to pass through. Reports false when a nil pointer on the way is unsettable, in which case there
// is nowhere to put the value and the caller skips it.
func fieldByIndexPathAlloc(val reflect.Value, path []int) (reflect.Value, bool) {
	if len(path) == 1 {
		return val.Field(path[0]), true
	}
	for depth, index := range path {
		if depth > 0 && val.Kind() == reflect.Ptr {
			if val.IsNil() {
				if !val.CanSet() {
					return reflect.Value{}, false
				}
				val.Set(reflect.New(val.Type().Elem()))
			}
			val = val.Elem()
		}
		val = val.Field(index)
	}
	return val, true
}

// fieldValueOf resolves a field's value, taking the single-index fast path whenever the field is
// declared directly on the type. Only a promoted field pays for the path walk.
func fieldValueOf(val reflect.Value, field *FieldInfo) reflect.Value {
	if field.direct >= 0 {
		return val.Field(field.direct)
	}
	return fieldByIndexPath(val, field.IndexPath)
}
