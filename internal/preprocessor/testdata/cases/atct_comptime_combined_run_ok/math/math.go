package math

import "github.com/GiGurra/q/pkg/q"

// Recursive comptime function — calls to math.Fact at any call site
// resolve at preprocessor time.
var Fact = q.Comptime(func(n int) int {
	if n < 2 {
		return 1
	}
	return n * Fact(n-1)
})

// Comptime function that itself uses another comptime function (Fact).
// Cross-comptime composition: when the synthesis pass renders Power's
// impl, references to Fact get rewritten to the synthesised name so
// the recursion resolves inside the synthesis program.
var Power = q.Comptime(func(base, exp int) int {
	if exp == 0 {
		return 1
	}
	return base * Power(base, exp-1)
})
