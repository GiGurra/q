// Fixture: q.A[T] / q.AtomOf[T] reject non-atom type arguments.
//
// Bare `string` is not a named type usable as an atom, and named
// non-string types fail the underlying-type check.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type NotAtom int // wrong underlying type for an atom

func main() {
	// MISUSE: bare string, not a named type derived from q.Atom.
	bad := q.A[string]()
	fmt.Println(bad)

	// MISUSE: named type with non-string underlying.
	badInt := q.A[NotAtom]()
	fmt.Println(badInt)
}
