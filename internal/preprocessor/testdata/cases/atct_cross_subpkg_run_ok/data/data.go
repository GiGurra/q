package data

import (
	"github.com/GiGurra/q/pkg/q"
)

// Each consumer of TableSize gets the same compile-time-computed value;
// we expose it as a package-level var so other packages can read it.
var TableSize = q.AtCompileTime[int](func() int {
	sum := 0
	for i := 1; i <= 64; i++ {
		sum += i
	}
	return sum
})

// Lookup is a comptime-built [10]int. Demonstrates non-primitive R
// inside a non-main package (companion file lives in `data`).
var Lookup = q.AtCompileTime[[]int](func() []int {
	out := make([]int, 10)
	for i := range out {
		out[i] = i * i
	}
	return out
})
