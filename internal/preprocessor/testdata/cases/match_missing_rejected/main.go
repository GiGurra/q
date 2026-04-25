// Negative fixture: q.Match on an enum type with a missing case and
// no q.Default — build must fail.
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
		q.Case(Red, "warm"),
		q.Case(Green, "natural"),
		// Blue is missing — build must fail.
	)
}

func main() {
	_ = describe(Red)
}
