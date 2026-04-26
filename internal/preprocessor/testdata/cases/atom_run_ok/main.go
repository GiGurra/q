// Fixture: q.A[T]() typed-string atoms.
//
// Demonstrates:
//
//   1. Each user-declared atom type derived from q.Atom is its own
//      type — no central declaration needed.
//   2. q.A[T]() summons an instance whose value is the bare name of T.
//   3. Atoms of the same type compare via plain string equality.
//   4. The Go type system distinguishes different atom types — you
//      can't mix a Pending with a Done.
//   5. Cross-package atoms work the same way.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Pending q.Atom
type Done    q.Atom
type Failed  q.Atom

// matchStatus uses plain string-equality on the typed atoms.
func matchStatus(p Pending) string {
	if p == q.A[Pending]() {
		return "still pending"
	}
	return "unknown"
}

func main() {
	p := q.A[Pending]()
	d := q.A[Done]()
	f := q.A[Failed]()

	// (1) Atom values are derived from the type name.
	fmt.Println("p:", p) // expect "Pending"
	fmt.Println("d:", d) // expect "Done"
	fmt.Println("f:", f) // expect "Failed"

	// (2) Same-type equality.
	fmt.Println("p == q.A[Pending]():", p == q.A[Pending]())

	// (3) Underlying string conversion still works.
	fmt.Println("string(p):", string(p))

	// (4) Function takes a specific atom type — Go's type system enforces it.
	fmt.Println("matchStatus:", matchStatus(p))

	// (5) Atoms work in switch case-list expressions. q.AtomOf[T]()
	//     returns the value pre-cast to q.Atom, so it slots straight
	//     into a `switch a q.Atom { case … }` without the boilerplate
	//     q.Atom(q.A[T]()) wrap.
	classify := func(a q.Atom) string {
		switch a {
		case q.AtomOf[Pending]():
			return "p-class"
		case q.AtomOf[Done]():
			return "d-class"
		}
		return "?"
	}
	fmt.Println("classify(Pending):", classify(q.AtomOf[Pending]()))
	fmt.Println("classify(Done):", classify(q.AtomOf[Done]()))

	// (6) Atoms work as map keys.
	m := map[Pending]int{q.A[Pending](): 1}
	fmt.Println("map lookup:", m[q.A[Pending]()])
}
