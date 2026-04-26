// Fixture: q.Convert[Target](src) auto-derives a struct conversion
// at compile time. Verifies:
//   - 1:1 same-name field copy (every Target field has a same-named
//     Source field; types match exactly).
//   - Source has fields that don't appear on Target — silently
//     dropped (target-driven Chimney semantics).
//   - Non-trivial source (call expression) evaluates exactly once
//     via the IIFE form.
//   - Empty target struct degenerates to Target{}.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type User struct {
	ID       int
	Name     string
	Internal bool   // dropped — no Target counterpart
	Notes    string // dropped — no Target counterpart
}

type UserDTO struct {
	ID   int
	Name string
}

type Empty struct{}

var calls int

func loadUser() User {
	calls++
	return User{ID: 7, Name: "Ada", Internal: true, Notes: "skip me"}
}

func main() {
	// Bare-identifier source — emits literal directly, no IIFE.
	u := User{ID: 1, Name: "Bob", Internal: true, Notes: "x"}
	dto := q.Convert[UserDTO](u)
	fmt.Printf("bare: %+v\n", dto)

	// Non-trivial source — bound to a temp inside an IIFE so the
	// call evaluates exactly once.
	dto2 := q.Convert[UserDTO](loadUser())
	fmt.Printf("call: %+v calls=%d\n", dto2, calls)

	// Empty target — degenerates to Empty{}.
	e := q.Convert[Empty](u)
	fmt.Printf("empty: %+v\n", e)
}
