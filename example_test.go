package minijson_test

import (
	"fmt"

	"github.com/ivanjoz/minijson"
)

func Example() {
	type user struct {
		Name string   `json:"name"`
		Age  int      `json:"age,omitempty"`
		Tags []string `json:"tags,omitempty"`
	}

	users := []user{{Name: "Ada", Age: 36}, {Name: "Lin", Tags: []string{"admin"}}}
	encoded, _ := minijson.Marshal(users)

	var decoded []user
	_ = minijson.Unmarshal(encoded, &decoded)
	fmt.Println(decoded[0].Name, decoded[1].Tags[0])
	// Output: Ada admin
}
