// Negative fixture: q.Exhaustive type-switch on a q.OneOfN-derived
// value's .Value, missing one of the variant cases — build must fail.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Pending struct{}
type Done struct{}
type Failed struct{}

type Status q.OneOf3[Pending, Done, Failed]

func describe(s Status) {
	switch v := q.Exhaustive(s.Value).(type) {
	case Pending:
		fmt.Println("p")
	case Done:
		fmt.Println("d", v)
		// Failed missing — build must fail.
	}
}

func main() {
	describe(q.AsOneOf[Status](Pending{}))
}
