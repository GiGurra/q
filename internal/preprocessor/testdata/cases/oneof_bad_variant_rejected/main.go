// Negative fixture: q.AsOneOf[T](v) where v's type isn't one of T's
// arms — build must fail with a directed diagnostic.
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

type Pending struct{}
type Done struct{}

type Status q.OneOf2[Pending, Done]

// Failed isn't a variant of Status.
type Failed struct{}

func main() {
	_ = q.AsOneOf[Status](Failed{})
}
