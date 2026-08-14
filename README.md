# minijson

Compact, schema-aware JSON for Go backends and browser frontends.

`minijson` writes struct field names once per payload, then encodes records as positional arrays. It keeps JSON's inspectability while avoiding repeated object keys in large, similarly shaped responses. Zero-valued fields are omitted, nested structs/slices/maps are supported, and each payload carries the metadata needed by the bundled TypeScript decoder.

```text
ordinary JSON: [{"name":"Ada","age":36},{"name":"Lin"}]
minijson:      [<field metadata>,<compact positional content>]
```

The Go package provides encoding and typed decoding. The `@ivanjoz/minijson` package provides frontend decoding into ordinary JavaScript objects.

## Install

Go:

```sh
go get github.com/ivanjoz/minijson@v0.3.0
```

TypeScript from GitHub:

```sh
bun add @ivanjoz/minijson@github:ivanjoz/minijson#v0.3.0
```

## Go usage

```go
package main

import (
	"fmt"
	"github.com/ivanjoz/minijson"
)

type User struct {
	Name string   `json:"name"`
	Age  int      `json:"age,omitempty"`
	Tags []string `json:"tags,omitempty"`
}

func main() {
	users := []User{{Name: "Ada", Age: 36}, {Name: "Lin", Tags: []string{"admin"}}}
	payload, err := minijson.Marshal(users)
	if err != nil {
		panic(err)
	}

	var decoded []User
	if err := minijson.Unmarshal(payload, &decoded); err != nil {
		panic(err)
	}
	fmt.Println(decoded)
}
```

`Marshal` respects exported fields and `json` tags, including `json:"-"`. `Unmarshal` expects a non-nil pointer to the target value.

## Browser usage

Parse the HTTP response as JSON, then pass the resulting value to `unmarshal`:

```ts
import { unmarshal } from '@ivanjoz/minijson'

const response = await fetch('/api/users')
const users = unmarshal(await response.json())
```

Values that are not minijson's two-element payload shape pass through unchanged, so the decoder can be used as a general response transform.

## Wire format

Every payload is `[keys, content]`:

- `keys` describes each struct type as `[typeID, position, fieldName, ...]`.
- `content` contains positional values.
- Header `1` starts a struct with a type ID; header `0` reuses the previous type.
- Header `2` marks a slice/array; header `3` marks alternating map keys and values.
- Struct reference blocks carry skipped positions for zero-valued fields.

The format is self-describing for the TypeScript decoder. Go decoding uses the destination's concrete struct types to restore typed values.

## Zero values and `MINIJSON_INCLUDE_EMPTY`

Zero values never travel, whether or not the field's `json` tag has `omitempty`. Go's `Unmarshal` hides that because it repopulates each field from the destination struct, but a decoder without types cannot: the field is simply absent, so `0`, `""` and `false` read back as `undefined` in the browser.

Set `MINIJSON_INCLUDE_EMPTY=1` (or call `minijson.SetIncludeEmpty(true)`) and each type's keys entry gains a trailing object describing the zero value of its non-`omitempty` fields:

```text
[1, 0, "id", 1, "codigo", {"id":0,"codigo":"","cantidad":0,"activo":false}]
```

The TypeScript decoder uses it to reconstruct the exact object `encoding/json` would have produced. Fields tagged `omitempty` are left out on purpose — `encoding/json` would have dropped them too.

The block costs one object per type per payload, not per record: 2000 records of a 10-field struct grow by 114 bytes. It is appended last and is the only object in an entry, and both header parsers read `(position, name)` pairs and stop at the last complete one, so a decoder that predates the block ignores it. Old and new clients can therefore be mixed while the flag is switched.

## Agreement with encoding/json

The contract is that a value produces the same JSON value through `Marshal` as it does through `encoding/json`. `TestDifferentialAgainstEncodingJSON` enforces it: it decodes each payload with no type information — the way the browser does — and diffs the result against `json.Marshal`. Add a case there whenever a new kind of field appears in a response type.

That contract covers:

- **Embedded structs are promoted.** An anonymous field without an explicit `json` name has its fields flattened into the parent, including transitively and through embedded pointers, with `encoding/json`'s shadowing rules (shallower wins; ties at the same depth resolve only if exactly one is tagged).
- **`json.Marshaler` and `encoding.TextMarshaler` are honoured**, along with the matching unmarshalers, so a `time.Time` travels as `"2026-08-13T10:00:00Z"`.
- **Unexported fields are skipped.**

### Known limitations

Only value-receiver `MarshalJSON`/`MarshalText` are honoured. A pointer-receiver marshaler on a non-addressable value is invisible to reflection, and `Marshal` receives its input through an `any`, so nothing is addressable at the top level. `time.Time`, `decimal.Decimal` and the `null.*` family all use value receivers.

## Development

```sh
go test -race ./...
bun test javascript
go test -bench . -benchmem
```

## License

GPL-3.0-only. See [LICENSE](LICENSE).
