package main

import (
	"fmt"

	"fixture/math"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	// Direct comptime calls — folded to literals at the call site.
	fmt.Println("fact(6):", math.Fact(6))
	fmt.Println("fact(8):", math.Fact(8))
	fmt.Println("pow(2,10):", math.Power(2, 10))
	fmt.Println("pow(3,5):", math.Power(3, 5))

	// Compose comptime call with q.AtCompileTime: the comptime call
	// is the body of an AtCompileTime closure that doubles the
	// result. The synthesis pass evaluates both in topo order.
	doubled := q.AtCompileTime[int](func() int {
		return math.Fact(5) + math.Fact(5)
	})
	fmt.Println("2 * fact(5):", doubled)
}
