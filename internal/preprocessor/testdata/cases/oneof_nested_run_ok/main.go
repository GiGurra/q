// Fixture: nested-sum dispatch via q.Match leaf-type arms (Phase C).
// q.Either[ErrSet, OkSet] where each arm is itself a q.OneOf2 — the
// q.Match arms target the LEAF variants directly and the rewriter
// emits nested switches. Coverage is enforced over the flat leaf set.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
	"github.com/GiGurra/q/pkg/q/either"
)

// Error variants:
type NotFound struct {
	Path string
}
type Forbidden struct {
	Reason string
}

// Success variants:
type Created struct {
	ID int
}
type Updated struct {
	ID int
}

// Two-level grouping: error-set + ok-set, each its own sum.
type ErrSet q.OneOf2[NotFound, Forbidden]
type OkSet q.OneOf2[Created, Updated]

// Outer: Either[ErrSet, OkSet]. Underlying is a OneOf2[ErrSet, OkSet]
// (Either is structurally a 2-arm OneOf with named arms).
type Result = either.Either[ErrSet, OkSet]

func process(kind string) Result {
	switch kind {
	case "missing":
		return either.AsEither[Result](
			q.AsOneOf[ErrSet](NotFound{Path: "/x"}),
		)
	case "denied":
		return either.AsEither[Result](
			q.AsOneOf[ErrSet](Forbidden{Reason: "no creds"}),
		)
	case "made":
		return either.AsEither[Result](
			q.AsOneOf[OkSet](Created{ID: 42}),
		)
	default:
		return either.AsEither[Result](
			q.AsOneOf[OkSet](Updated{ID: 7}),
		)
	}
}

// Flat-leaf q.Match: each arm targets a LEAF, not the immediate
// ErrSet/OkSet arms. The preprocessor flattens the sum tree and
// emits nested switches.
func describe(r Result) string {
	return q.Match(r,
		q.OnType(func(n NotFound) string { return fmt.Sprintf("404: %s", n.Path) }),
		q.OnType(func(f Forbidden) string { return fmt.Sprintf("403: %s", f.Reason) }),
		q.OnType(func(c Created) string { return fmt.Sprintf("201: %d", c.ID) }),
		q.OnType(func(u Updated) string { return fmt.Sprintf("200: %d", u.ID) }),
	)
}

// q.Default catches the rest if you only handle one leaf:
func onlyMissing(r Result) string {
	return q.Match(r,
		q.OnType(func(n NotFound) string { return "got missing: " + n.Path }),
		q.Default("not a missing-resource"),
	)
}

func main() {
	for _, k := range []string{"missing", "denied", "made", "updated"} {
		r := process(k)
		fmt.Println(describe(r))
		fmt.Println(onlyMissing(r))
	}
}
