package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// fib(5) — 5 levels of nested q.AtCompileTime, each spawning a deeper
// q-toolexec recursion. fib(5) = 5.
//
// Manually unrolled rather than using a recursive Go function because
// q.AtCompileTime requires a function-literal argument (no named
// references), so the macro tree has to be spelled out.

// Reusable leaves at the bottom:
//   fib(0) = 0
//   fib(1) = 1

// Expansion:
//   fib(5) = fib(4) + fib(3)
//   fib(4) = fib(3) + fib(2)
//   fib(3) = fib(2) + fib(1)
//   fib(2) = fib(1) + fib(0)

func main() {
	fib5 := q.AtCompileTime[int](func() int {
		// fib(4) branch
		f4 := q.AtCompileTime[int](func() int {
			// fib(3)
			f3a := q.AtCompileTime[int](func() int {
				f2a := q.AtCompileTime[int](func() int {
					one := q.AtCompileTime[int](func() int { return 1 })
					zero := q.AtCompileTime[int](func() int { return 0 })
					return one + zero
				})
				one := q.AtCompileTime[int](func() int { return 1 })
				return f2a + one
			})
			// fib(2)
			f2b := q.AtCompileTime[int](func() int {
				one := q.AtCompileTime[int](func() int { return 1 })
				zero := q.AtCompileTime[int](func() int { return 0 })
				return one + zero
			})
			return f3a + f2b
		})
		// fib(3) branch
		f3 := q.AtCompileTime[int](func() int {
			f2 := q.AtCompileTime[int](func() int {
				one := q.AtCompileTime[int](func() int { return 1 })
				zero := q.AtCompileTime[int](func() int { return 0 })
				return one + zero
			})
			one := q.AtCompileTime[int](func() int { return 1 })
			return f2 + one
		})
		return f4 + f3
	})
	fmt.Println("fib(5):", fib5)
}
