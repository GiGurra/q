// Negative fixture: nested-sum dispatch with a missing leaf arm
// and no q.Default — build must fail listing the missing leaves.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
	"github.com/GiGurra/q/pkg/q/either"
)

type NotFound struct{}
type Forbidden struct{}
type Created struct{}
type Updated struct{}

type ErrSet q.OneOf2[NotFound, Forbidden]
type OkSet q.OneOf2[Created, Updated]

type Result = either.Either[ErrSet, OkSet]

func describe(r Result) string {
	return q.Match(r,
		q.OnType(func(n NotFound) string { return "nf" }),
		q.OnType(func(f Forbidden) string { return "fb" }),
		q.OnType(func(c Created) string { return "c" }),
		// Updated leaf missing — build must fail.
	)
}

func main() {
	_ = describe(either.AsEither[Result](q.AsOneOf[ErrSet](NotFound{})))
	fmt.Println("never reached")
}
