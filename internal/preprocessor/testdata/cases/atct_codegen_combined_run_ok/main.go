package main

import (
	"fmt"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

// Compose q.AtCompileTime (value-returning) with q.AtCompileTimeCode
// (code generation). The code-gen closure captures the value-returning
// closure's result, baking it into the generated source.

func main() {
	// 1. Value-returning AtCompileTime — produces a list of names.
	names := q.AtCompileTime[[]string](func() []string {
		return []string{"alice", "bob", "carol"}
	})
	// 2. Code-gen closure that captures `names` and synthesizes a
	//    `func() int` whose body inlines `len(names)` as a literal.
	count := q.AtCompileTimeCode[func() int](func() string {
		return "func() int { return " + fmt.Sprintf("%d", len(names)) + " }"
	})
	// 3. Code-gen closure that builds a string-mapping switch using
	//    the captured names.
	greet := q.AtCompileTimeCode[func(int) string](func() string {
		var b strings.Builder
		b.WriteString("func(i int) string {\n")
		b.WriteString("\tswitch i {\n")
		for i, n := range names {
			b.WriteString(fmt.Sprintf("\tcase %d:\n\t\treturn %q\n", i, "hi "+n))
		}
		b.WriteString("\tdefault:\n\t\treturn \"unknown\"\n")
		b.WriteString("\t}\n}")
		return b.String()
	})
	fmt.Println("names:", names)
	fmt.Println("count:", count())
	fmt.Println("greet(0):", greet(0))
	fmt.Println("greet(1):", greet(1))
	fmt.Println("greet(2):", greet(2))
	fmt.Println("greet(99):", greet(99))
}
