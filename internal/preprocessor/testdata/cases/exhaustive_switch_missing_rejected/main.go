// Negative fixture: q.Exhaustive switch missing a constant. Build
// must fail with a diagnostic naming the missing constant.
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
	switch q.Exhaustive(c) {
	case Red:
		return "red"
	case Green:
		return "green"
		// Blue is missing — build must fail.
	}
	return "unknown"
}

func main() {
	_ = describe(Red)
}
