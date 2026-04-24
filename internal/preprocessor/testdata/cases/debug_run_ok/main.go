// Fixture: q.Debug is Go's missing `dbg!`. The preprocessor
// rewrites `q.Debug(x)` into `q.DebugAt("<file>:<line> <src>", x)`.
// We capture the debug output by redirecting q.DebugWriter to a
// buffer, then print it normalized (line numbers replaced with N
// so the fixture stays stable across source edits).
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
	x := q.Debug(42)
	fmt.Printf("x=%d\n", x)

	// Nested in a normal call.
	fmt.Printf("double=%d\n", double(q.Debug(7)))

	// Mid-expression in an arithmetic expression.
	y := q.Debug(10) + q.Debug(5)
	fmt.Printf("y=%d\n", y)

	// Works with strings too.
	s := q.Debug("hello")
	fmt.Printf("s=%s\n", s)

	// Direct DebugAt call (runtime path, not preprocessor-rewritten).
	z := q.DebugAt("custom-label", 100)
	fmt.Printf("z=%d\n", z)

	// Dump captured debug output with line numbers normalized.
	fmt.Println("--- debug ---")
	for _, line := range strings.Split(strings.TrimRight(dbg.String(), "\n"), "\n") {
		fmt.Println(stripLineNumber(line))
	}
}

func double(n int) int { return n * 2 }
