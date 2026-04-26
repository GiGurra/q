// Fixture: q.ValidatedStruct rejects missing required fields the
// same way q.FnParams does.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct {
	_       q.ValidatedStruct
	Name    string
	Version int
}

func main() {
	// MISSING: Version (required, untagged).
	c := Config{Name: "app"}
	fmt.Println(c)
}
