// Negative fixture: a dependency cycle (A needs B, B needs A). The
// preprocessor must detect the cycle and surface the path.
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

type A struct{ b *B }
type B struct{ a *A }
type Root struct{ a *A }

func newA(b *B) *A     { return &A{b: b} }
func newB(a *A) *B     { return &B{a: a} }
func newRoot(a *A) *Root { return &Root{a: a} }

func main() {
	_, _ = q.Assemble[*Root](newA, newB, newRoot).DeferCleanup()
}
