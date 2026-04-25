package fib

import "github.com/GiGurra/q/pkg/q"

// Fib is a Zig-style comptime function. Calls to Fib at any package
// in the module become preprocessor-time evaluations that splice the
// computed result at the call site. The closure recurses normally
// inside one synthesis subprocess — Go recursion at preprocess time.
var Fib = q.Comptime(func(n int) int {
	if n < 2 {
		return n
	}
	return Fib(n-1) + Fib(n-2)
})

// Square is another comptime function in the same package. Call sites
// of Square also become comptime invocations.
var Square = q.Comptime(func(n int) int {
	return n * n
})
