// Negative fixture: q.OnType used in a q.Match whose value isn't a
// q.OneOfN-derived sum — build must fail.
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

type Color int

const (
	Red Color = iota
	Green
	Blue
)

func describe(c Color) string {
	return q.Match(c,
		q.OnType(func(c Color) string { return "x" }),
		q.Default("?"),
	)
}

func main() {
	_ = describe(Red)
}
