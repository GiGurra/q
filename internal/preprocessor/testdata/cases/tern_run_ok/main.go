// Fixture: q.Tern[T](cond, ifTrue, ifFalse) — conditional expression
// sugar with preprocessor-injected lazy evaluation of BOTH branches.
//
// This fixture demonstrates the FOUR properties that distinguish
// q.Tern from a plain function call:
//
//   1. Side-effect laziness — only the matching branch runs. We
//      assert this via call counters on each branch independently.
//   2. Nil-deref safety — `u.Name` on a nil `u` would panic if
//      eagerly evaluated; under q.Tern it's a no-op when its arm
//      isn't taken.
//   3. Both branches accepted — q.Tern is a true ternary, not a
//      "value-or-zero" picker.
//   4. Chaining — nested q.Tern calls rewrite cleanly because the
//      inner tern's IIFE becomes the outer's branch text.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type User struct{ Name string }

// trueCalls / falseCalls track invocation counts so the test can
// assert lazy semantics: only the taken branch runs.
var trueCalls, falseCalls int

func incTrue(v int) int  { trueCalls++; return v }
func incFalse(v int) int { falseCalls++; return v }

func main() {
	// (1) Side-effect laziness. Both args are real Go function calls;
	// without source-splicing, BOTH would always run before q.Tern
	// gets to choose.
	a := q.Tern[int](true, incTrue(1), incFalse(11))
	b := q.Tern[int](false, incTrue(2), incFalse(22))
	fmt.Println("a:", a)
	fmt.Println("b:", b)
	fmt.Println("trueCalls:", trueCalls)   // expect 1 — only a's true branch
	fmt.Println("falseCalls:", falseCalls) // expect 1 — only b's false branch

	// Run the false-branch picker many times — must never invoke incTrue.
	for i := 0; i < 100; i++ {
		_ = q.Tern[int](false, incTrue(99), 0)
	}
	fmt.Println("trueCalls after 100 false-loops:", trueCalls) // still 1

	// (2) Nil-deref safety. `u.Name` would panic if evaluated eagerly.
	// q.Tern's source-splicing keeps it inside the if-body so it
	// never runs when u is nil.
	var u *User
	display := q.Tern[string](u != nil, u.Name, "anonymous")
	fmt.Println("nil-user display:", display)

	// Same with nil-method-call. expensiveLookup() is on a nil
	// receiver but never invoked.
	var lookups int
	getName := func() string { lookups++; return "named" }
	name := q.Tern[string](false, getName(), "fallback")
	fmt.Println("name on false:", name)
	fmt.Println("lookups (false path):", lookups) // expect 0

	name2 := q.Tern[string](true, getName(), "fallback")
	fmt.Println("name on true:", name2)
	fmt.Println("lookups (true path):", lookups) // expect 1

	// (3) Both branches for various T.
	fmt.Println("string pick:", q.Tern[string](false, "yes", "no"))
	fmt.Println("int pick:", q.Tern[int](false, 99, 42))
	fmt.Println("bool pick:", q.Tern[bool](false, true, false))
	fmt.Println("ptr pick:", q.Tern[*User](false, &User{Name: "x"}, &User{Name: "y"}).Name)
	fmt.Println("slice pick:", q.Tern[[]int](false, []int{1, 2, 3}, []int{9}))

	// Verify cond is also evaluated only once even with side-effects.
	var condCalls int
	checkCond := func() bool { condCalls++; return true }
	_ = q.Tern[int](checkCond(), 7, 8)
	fmt.Println("condCalls:", condCalls) // expect 1 — single eval

	// (4) Chained terns for multi-way pick. The rewriter handles
	// nested q.Tern calls via exprTextSubst, so each inner call
	// becomes its own IIFE inside the outer's branch text.
	score := 75
	tier := q.Tern[string](score >= 90, "A",
		q.Tern[string](score >= 80, "B",
			q.Tern[string](score >= 70, "C", "F")))
	fmt.Println("tier:", tier)
}
