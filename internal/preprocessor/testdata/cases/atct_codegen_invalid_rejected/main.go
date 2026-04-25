package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	// Returns malformed Go source — the spliced expression won't
	// parse, so the user-package compile fails.
	x := q.AtCompileTimeCode[int](func() string {
		return "this is not valid go"
	})
	fmt.Println(x)
}
