// Fixture: q.File / q.Line / q.FileLine / q.Expr — primitive-typed
// compile-time captures. Plus q.SlogFileLine — slog.Attr flavour
// with combined "<file>:<line>" value.
//
// Each helper is rewritten to a literal in the user file, so the
// runtime values are constants the compiler can fold. The fixture
// asserts each shape and verifies that q.Expr discards its
// argument's runtime value (the side-effect counter stays at 0).
package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

var sideEffectCount int

func sideEffect() int {
	sideEffectCount++
	return 99
}

func main() {
	// q.File / q.Line / q.FileLine — primitives.
	fmt.Printf("file=%s\n", stripLineNumber(q.File()))
	fmt.Printf("line.type=%T\n", q.Line())
	fmt.Printf("fileLine=%s\n", stripLineNumber(q.FileLine()))

	// q.Expr — captures literal source text. The argument's
	// runtime value is discarded; sideEffect should NOT run.
	a, b := 3, 4
	_, _ = a, b // silence "declared but not used" — q.Expr captures source text only
	expr1 := q.Expr(a + b)
	expr2 := q.Expr(sideEffect())
	expr3 := q.Expr(items["key"])
	fmt.Printf("expr.add=%s\n", expr1)
	fmt.Printf("expr.call=%s\n", expr2)
	fmt.Printf("expr.index=%s\n", expr3)
	fmt.Printf("expr.sideEffectCount=%d\n", sideEffectCount)

	// q.SlogFileLine — slog.Attr with combined "<file>:<line>" value.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) == 0 && attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return attr
		},
	}))
	logger.Info("event", q.SlogFileLine())
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		fmt.Println(stripLineNumber(line))
	}
}

// items is referenced inside q.Expr to exercise an indexed expression.
// Its actual contents don't matter — q.Expr discards the value.
var items = map[string]int{"key": 1}

// stripLineNumber rewrites every "main.go:<digits>" occurrence to
// "main.go:N" so the captured output is stable across edits.
var fileLineRe = regexp.MustCompile(`main\.go:\d+`)

func stripLineNumber(s string) string {
	return fileLineRe.ReplaceAllString(s, "main.go:N")
}
