package main

import (
	"fmt"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

// Verify q.AtCompileTimeCode works at package level (as a `var X = …`
// initializer). The spliced expression becomes the var's value at
// var-init time.

// Generated function value at package scope.
var Classify = q.AtCompileTimeCode[func(int) string](func() string {
	var b strings.Builder
	b.WriteString("func(n int) string {\n")
	b.WriteString("\tswitch {\n")
	b.WriteString("\tcase n < 0:\n\t\treturn \"negative\"\n")
	b.WriteString("\tcase n == 0:\n\t\treturn \"zero\"\n")
	b.WriteString("\tcase n < 10:\n\t\treturn \"small\"\n")
	b.WriteString("\tdefault:\n\t\treturn \"large\"\n")
	b.WriteString("\t}\n}")
	return b.String()
})

// Generated string constant at package scope.
var Tag = q.AtCompileTimeCode[string](func() string {
	parts := []string{"prod", "edge", "v2"}
	return fmt.Sprintf("%q", strings.Join(parts, "-"))
})

// Macro that captures another package-level AtCompileTime value.
var Names = q.AtCompileTime[[]string](func() []string {
	return []string{"alice", "bob", "carol"}
})

var Greet = q.AtCompileTimeCode[func(int) string](func() string {
	var b strings.Builder
	b.WriteString("func(i int) string {\n\tswitch i {\n")
	for i, n := range Names {
		b.WriteString(fmt.Sprintf("\tcase %d:\n\t\treturn %q\n", i, "hi "+n))
	}
	b.WriteString("\tdefault:\n\t\treturn \"unknown\"\n\t}\n}")
	return b.String()
})

func main() {
	fmt.Println("classify(-5):", Classify(-5))
	fmt.Println("classify(0):", Classify(0))
	fmt.Println("classify(7):", Classify(7))
	fmt.Println("classify(100):", Classify(100))
	fmt.Println("tag:", Tag)
	fmt.Println("greet(0):", Greet(0))
	fmt.Println("greet(1):", Greet(1))
	fmt.Println("greet(99):", Greet(99))
}
