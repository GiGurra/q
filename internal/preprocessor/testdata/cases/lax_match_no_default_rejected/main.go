// Negative fixture: q.Match on a q.GenEnumJSONLax-opted type MUST
// include a q.Default(...) arm. Mirrors the q.Exhaustive Lax-default
// rule.
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

// All declared cases covered via q.Case, but no q.Default — must
// reject because Status is Lax-opted.
func describe(s Status) string {
	return q.Match(s,
		q.Case(StatusActive, "active"),
		q.Case(StatusBlocked, "blocked"),
	)
}

func main() {
	_ = describe(StatusActive)
}
