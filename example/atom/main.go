// example/atom mirrors docs/api/atom.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/atom
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// Each atom is its own type — no const decl needed.
type Pending q.Atom
type Done q.Atom
type Failed q.Atom

// ---------- "Examples — type-distinct values" ----------
//
//	func ack(p Pending) string {
//	    if p == q.A[Pending]() {
//	        return "still pending"
//	    }
//	    return "unexpected"
//	}
func ack(p Pending) string {
	if p == q.A[Pending]() {
		return "still pending"
	}
	return "unexpected"
}

// ---------- "Examples — switch over the parent q.Atom type" ----------
//
//	func classify(a q.Atom) string {
//	    switch a {
//	    case q.AtomOf[Pending](): return "p"
//	    case q.AtomOf[Done]():    return "d"
//	    case q.AtomOf[Failed](): return "f"
//	    }
//	    return "?"
//	}
func classify(a q.Atom) string {
	switch a {
	case q.AtomOf[Pending]():
		return "p"
	case q.AtomOf[Done]():
		return "d"
	case q.AtomOf[Failed]():
		return "f"
	}
	return "?"
}

func main() {
	// Top-of-doc tour.
	p := q.A[Pending]()
	d := q.A[Done]()
	fmt.Printf("Pending value: %s\n", string(p))
	fmt.Printf("Done value: %s\n", string(d))

	// Type-distinct values.
	fmt.Printf("ack(p): %s\n", ack(p))

	// Switch over q.Atom — pre-cast each branch via AtomOf.
	fmt.Printf("classify(AtomOf[Pending]): %s\n", classify(q.AtomOf[Pending]()))
	fmt.Printf("classify(AtomOf[Done]): %s\n", classify(q.AtomOf[Done]()))
	fmt.Printf("classify(AtomOf[Failed]): %s\n", classify(q.AtomOf[Failed]()))
	fmt.Printf("classify(other): %s\n", classify(q.Atom("github.com/me/other.X")))

	// Atoms as map keys.
	counts := map[Pending]int{q.A[Pending](): 0}
	counts[q.A[Pending]()]++
	counts[q.A[Pending]()]++
	fmt.Printf("counts[Pending]: %d\n", counts[q.A[Pending]()])

	// Same underlying value via A vs AtomOf.
	fmt.Printf("A[Pending]==AtomOf[Pending]: %v\n", string(q.A[Pending]()) == string(q.AtomOf[Pending]()))
}
