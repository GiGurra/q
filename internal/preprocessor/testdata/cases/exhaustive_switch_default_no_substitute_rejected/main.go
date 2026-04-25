// Negative fixture: a `default:` clause does NOT replace coverage
// of declared constants. Build must still fail when known constants
// are missing, even with default present. The default catches
// unknown/forward-compat values, not declared ones.
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
	default:
		// Blue is missing — default is for UNKNOWN values, not a
		// substitute for declared coverage. Build must fail.
		return "fallback"
	}
}

func main() {
	_ = describe(Red)
}
