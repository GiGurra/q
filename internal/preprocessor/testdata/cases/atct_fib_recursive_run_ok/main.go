package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// Fibonacci computed at compile time via NESTED q.AtCompileTime calls.
// Each level of nesting creates one additional q-toolexec recursion:
//
//   level 0 — user `go build` invokes q on this main.go.
//   level 1 — q's synthesis subprocess for the outermost AtCompileTime
//             runs `go run -toolexec=q ./.q-comptime-<hash>`. Inside,
//             q processes the fib(N-1) and fib(N-2) AtCompileTime
//             calls.
//   level 2 — those calls' synthesis subprocesses recursively
//             evaluate fib(N-2)+fib(N-3) and fib(N-3)+fib(N-4).
//   ...
//   level K — the leaves return 0 / 1 with no further nesting.
//
// fib(4) is unrolled all the way down. Total subprocess depth: 4.

func main() {
	// fib(4) = fib(3) + fib(2)
	fib4 := q.AtCompileTime[int](func() int {
		// fib(3) = fib(2) + fib(1)
		f3 := q.AtCompileTime[int](func() int {
			// fib(2) = fib(1) + fib(0)
			f2a := q.AtCompileTime[int](func() int {
				one := q.AtCompileTime[int](func() int { return 1 })
				zero := q.AtCompileTime[int](func() int { return 0 })
				return one + zero
			})
			one := q.AtCompileTime[int](func() int { return 1 })
			return f2a + one
		})
		// fib(2) = fib(1) + fib(0)
		f2b := q.AtCompileTime[int](func() int {
			one := q.AtCompileTime[int](func() int { return 1 })
			zero := q.AtCompileTime[int](func() int { return 0 })
			return one + zero
		})
		return f3 + f2b
	})

	fmt.Println("fib(4):", fib4)
}
