// Negative fixture: q.Exhaustive on a q.GenEnumJSONLax-opted type
// MUST include a default: arm. The wire format admits unknown values
// (Lax JSON pass-through), so the switch needs an explicit catch-all
// even when every currently-declared constant is covered.
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

type Status int

const (
	StatusActive Status = iota
	StatusBlocked
)

var _ = q.GenEnumJSONLax[Status]()

// All declared cases covered, but no default: — must reject because
// Status is opted into Lax JSON.
func describe(s Status) string {
	switch q.Exhaustive(s) {
	case StatusActive:
		return "active"
	case StatusBlocked:
		return "blocked"
	}
	return ""
}

func main() {
	_ = describe(StatusActive)
}
