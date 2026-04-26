// Negative fixture: q.Match on a q.OneOfN-derived value with a
// missing arm and no q.Default — build must fail.
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

type Pending struct{}
type Done struct{}
type Failed struct{}

type Status q.OneOf3[Pending, Done, Failed]

func describe(s Status) string {
	return q.Match(s,
		q.Case(Pending{}, "p"),
		q.Case(Done{}, "d"),
		// Failed is missing — build must fail.
	)
}

func main() {
	_ = describe(q.AsOneOf[Status](Pending{}))
}
