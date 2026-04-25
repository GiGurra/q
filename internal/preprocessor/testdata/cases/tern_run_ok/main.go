// Fixture: q.Tern[T](cond, t) — conditional expression sugar with
// preprocessor-injected lazy evaluation of t.
//
// This fixture demonstrates the THREE properties that distinguish
// q.Tern from a plain function call:
//
//   1. Side-effect laziness — t's expression only runs on the true
//      path. We assert this via call counters.
//   2. Nil-deref safety — `u.Name` on a nil `u` would panic if
//      eagerly evaluated; under q.Tern it's a no-op when cond is
//      false.
//   3. Zero-value default — when cond is false, the IIFE returns T's
//      zero value without ever evaluating t.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type User struct{ Name string }

// trueCalls / falseCalls track invocation counts so the test can
// assert lazy semantics: the false-branch expression must never run.
var trueCalls, falseCalls int

func incTrue(v int) int  { trueCalls++; return v }
func incFalse(v int) int { falseCalls++; return v }

func main() {
	// (1) Side-effect laziness. Both args are real Go function calls;
	// without source-splicing, both would always run before q.Tern
	// gets to choose.
	a := q.Tern[int](true, incTrue(1))
	b := q.Tern[int](false, incFalse(2))
	fmt.Println("a:", a)
	fmt.Println("b:", b)
	fmt.Println("trueCalls:", trueCalls)   // expect 1 — true path
	fmt.Println("falseCalls:", falseCalls) // expect 0 — never ran!

	// Run the false-branch picker many times — must never invoke incFalse.
	for i := 0; i < 100; i++ {
		_ = q.Tern[int](false, incFalse(99))
	}
	fmt.Println("falseCalls after 100 false-loops:", falseCalls) // still 0

	// (2) Nil-deref safety. `u.Name` would panic if evaluated eagerly.
	// q.Tern's source-splicing keeps it inside the if-body so it
	// never runs when u is nil.
	var u *User
	display := q.Tern[string](u != nil, u.Name)
	fmt.Println("nil-user display empty:", display == "")

	// Same with nil-method-call. expensiveLookup() is on a nil
	// receiver but never invoked.
	var lookups int
	getName := func() string { lookups++; return "named" }
	name := q.Tern[string](false, getName())
	fmt.Println("name on false:", name == "")
	fmt.Println("lookups (false path):", lookups) // expect 0

	name2 := q.Tern[string](true, getName())
	fmt.Println("name on true:", name2)
	fmt.Println("lookups (true path):", lookups) // expect 1

	// (3) Zero-value default for various T.
	fmt.Println("string zero:", q.Tern[string](false, "x") == "")
	fmt.Println("int zero:", q.Tern[int](false, 99) == 0)
	fmt.Println("bool zero:", q.Tern[bool](false, true) == false)
	fmt.Println("ptr zero:", q.Tern[*User](false, &User{Name: "x"}) == nil)
	fmt.Println("slice zero:", q.Tern[[]int](false, []int{1, 2, 3}) == nil)

	// Verify cond is also evaluated only once even with side-effects.
	var condCalls int
	checkCond := func() bool { condCalls++; return true }
	_ = q.Tern[int](checkCond(), 7)
	fmt.Println("condCalls:", condCalls) // expect 1 — single eval
}
