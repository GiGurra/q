package main

import (
	"fmt"

	"fixture/data"

	"github.com/GiGurra/q/pkg/q"
)

// main also uses q.AtCompileTime — the cross-package references show
// that each package's q.AtCompileTime calls run in their own
// synthesis pass (per-package compiler invocation), then the
// resulting values flow naturally through normal Go imports.

func main() {
	greeting := q.AtCompileTime[string](func() string {
		return "table size: this side"
	})
	fmt.Println(greeting)
	fmt.Println("data.TableSize:", data.TableSize)
	fmt.Println("data.Lookup:", data.Lookup)
}
