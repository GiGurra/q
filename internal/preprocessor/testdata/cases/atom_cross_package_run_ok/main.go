// Fixture: cross-package atom collision safety.
//
// Both main.Status and sub.Status share the bare type name "Status".
// Without qualification their q.Atom string values would collide,
// silently overwriting map keys and falsely reporting equal switch
// cases. The preprocessor's typecheck pass populates each atom's
// value with its full import-path-qualified name, so the two atoms
// stay distinct at the q.Atom level.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"

	"fixture/sub"
)

type Status q.Atom

func main() {
	a := q.AtomOf[Status]()
	b := sub.Get()

	// (1) Distinct values — no string collision.
	fmt.Println("a == b:", a == b)        // expect false
	fmt.Println("a:", string(a))           // expect "fixture.Status" (or main.Status)
	fmt.Println("b:", string(b))           // expect "fixture/sub.Status"

	// (2) Distinct map keys.
	m := map[q.Atom]string{}
	m[a] = "from main"
	m[b] = "from sub"
	fmt.Println("len(m):", len(m))         // expect 2
	fmt.Println("m[a]:", m[a])
	fmt.Println("m[b]:", m[b])
}
