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
go get github.com/ivanjoz/minijson@v0.1.0
```

TypeScript from GitHub:

```sh
bun add @ivanjoz/minijson@github:ivanjoz/minijson#v0.1.0
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

## Development

```sh
go test -race ./...
bun test javascript
go test -bench . -benchmem
```

## License

GPL-3.0-only. See [LICENSE](LICENSE).
