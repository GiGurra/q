// Fixture: q.Tern(cond, ifTrue, ifFalse) with INFERRED T — Go's
// type inference resolves T from the branch values, so the explicit
// `[T]` is optional. Same source-splicing semantics as the explicit
// form (lazy on each branch, single cond eval).
//
// Validates:
//   1. Implicit T works for primitive types (int, string, bool).
//   2. Implicit T works for pointer / slice / map types.
//   3. Implicit T works for user-defined struct types.
//   4. Lazy evaluation of both branches is preserved.
//   5. Implicit and explicit forms produce identical results.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type User struct{ Name string }

var trueCalls, falseCalls int

func sideEffectTrue[T any](v T) T  { trueCalls++; return v }
func sideEffectFalse[T any](v T) T { falseCalls++; return v }

func main() {
	a, b := 5, 3

	// Primitives — T inferred as int / string / bool from the branches.
	maxv := q.Tern(a > b, a, b)
	fmt.Println("max:", maxv)

	greeting := q.Tern(true, "hello", "goodbye")
	fmt.Println("greeting:", greeting)

	flag := q.Tern(false, true, false)
	fmt.Println("flag (false branch):", flag)

	// Pointer T inferred from &User{...}.
	u := q.Tern(true, &User{Name: "alice"}, &User{Name: "bob"})
	fmt.Println("user:", u.Name)

	// Slice / map — both branches must agree on element type.
	ints := q.Tern(true, []int{1, 2, 3}, []int{9, 9, 9})
	fmt.Println("ints:", ints)

	pickedMap := q.Tern(false, map[string]int{"x": 1}, map[string]int{"y": 2})
	fmt.Println("map:", pickedMap)

	// User-defined struct value.
	usr := q.Tern(true, User{Name: "carol"}, User{Name: "dave"})
	fmt.Println("usr:", usr.Name)

	// Lazy semantics — the unused branch must not invoke its sideEffect helper.
	_ = q.Tern(false, sideEffectTrue(99), sideEffectFalse(11))
	fmt.Println("after false-pick: trueCalls=", trueCalls, "falseCalls=", falseCalls) // 0, 1

	_ = q.Tern(true, sideEffectTrue(42), sideEffectFalse(33))
	fmt.Println("after true-pick: trueCalls=", trueCalls, "falseCalls=", falseCalls) // 1, 1

	// Mixed in same call site: explicit + implicit produce identical
	// rewritten code.
	x := q.Tern(true, 7, 0)
	y := q.Tern[int](true, 7, 0)
	fmt.Println("x == y:", x == y)

	// Chained inference: each inner q.Tern infers its own T,
	// the outer infers from its branches (which include another
	// q.Tern returning the same T).
	score := 85
	tier := q.Tern(score >= 90, "A",
		q.Tern(score >= 80, "B",
			q.Tern(score >= 70, "C", "F")))
	fmt.Println("tier:", tier)
}
