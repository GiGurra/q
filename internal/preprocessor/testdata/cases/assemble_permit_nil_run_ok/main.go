// Fixture: q.PermitNil(<recipe>) opts a recipe out of the runtime
// nil-check. Demonstrates that a single typed-identity helper
// covers EVERY recipe shape that q.Assemble accepts:
//
//	* pure          func() *T
//	* errored       func() (*T, error)
//	* resource      func() (*T, func(), error)
//	* non-err res   func() (*T, func())
//	* inline value  *T
//
// Each branded variant below opts in via q.PermitNil and returns
// nil from its recipe. Without q.PermitNil the rewriter would
// bubble q.ErrNil; with it, the nil flows through to the consumer
// which is written to handle nil inputs.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// Branded variants per shape so each gets its own provider key.
type Pure struct{ name string }
type Errd struct{ name string }
type Res struct{ name string }
type ResNoErr struct{ name string }
type Inline struct{ name string }

// Recipes — every shape returns nil deliberately.
func newPure() *Pure                    { return nil }
func newErrd() (*Errd, error)           { return nil, nil }
func newRes() (*Res, func(), error)     { return nil, nil, nil }
func newResNoErr() (*ResNoErr, func())  { return nil, nil }

func describe(p *Pure, e *Errd, r *Res, rn *ResNoErr, i *Inline) string {
	tag := func(label string, isNil bool) string {
		if isNil {
			return label + ":<nil>"
		}
		return label + ":<set>"
	}
	return tag("pure", p == nil) + " " +
		tag("errd", e == nil) + " " +
		tag("res", r == nil) + " " +
		tag("rnoerr", rn == nil) + " " +
		tag("inline", i == nil)
}

func main() {
	// inline value passed as nil — must also accept PermitNil.
	var inlineNil *Inline = nil

	out, _, err := q.Assemble[string](
		q.PermitNil(newPure),
		q.PermitNil(newErrd),
		q.PermitNil(newRes),
		q.PermitNil(newResNoErr),
		q.PermitNil(inlineNil),
		describe,
	).NoRelease()

	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println(out)
}
