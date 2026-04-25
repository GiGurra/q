// Fixture: q.DebugPrintln is Go's missing `dbg!` / `println!`. The
// preprocessor rewrites `q.DebugPrintln(x)` into
// `q.DebugPrintlnAt("<file>:<line> <src>", x)`. We capture the
// output by redirecting q.DebugWriter to a buffer, then print it
// normalised (line numbers replaced with N so the fixture stays
// stable across source edits).
package main

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

// stripLineNumber replaces the digits after "main.go:" with "N"
// once per line.
func stripLineNumber(line string) string {
	marker := "main.go:"
	i := strings.Index(line, marker)
	if i < 0 {
		return line
	}
	j := i + len(marker)
	for j < len(line) && line[j] >= '0' && line[j] <= '9' {
		j++
	}
	return line[:i+len(marker)] + "N" + line[j:]
}

func main() {
	var dbg bytes.Buffer
	q.DebugWriter = &dbg

	// Pass-through: the return value must match the input.
	x := q.DebugPrintln(42)
	fmt.Printf("x=%d\n", x)

	// Nested in a normal call.
	fmt.Printf("double=%d\n", double(q.DebugPrintln(7)))

	// Mid-expression in an arithmetic expression.
	y := q.DebugPrintln(10) + q.DebugPrintln(5)
	fmt.Printf("y=%d\n", y)

	// Works with strings too.
	s := q.DebugPrintln("hello")
	fmt.Printf("s=%s\n", s)

	// Direct DebugPrintlnAt call (runtime path, not preprocessor-rewritten).
	z := q.DebugPrintlnAt("custom-label", 100)
	fmt.Printf("z=%d\n", z)

	// Dump captured debug output with line numbers normalised.
	fmt.Println("--- debug ---")
	for _, line := range strings.Split(strings.TrimRight(dbg.String(), "\n"), "\n") {
		fmt.Println(stripLineNumber(line))
	}
}

func double(n int) int { return n * 2 }
