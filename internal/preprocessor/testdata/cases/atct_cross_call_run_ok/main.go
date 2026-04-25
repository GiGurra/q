package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// Cross-call captures: closure B references a variable bound by an
// earlier q.AtCompileTime call A. The synthesis pass must topo-sort
// so A runs before B and rewrite B's body to substitute A's result.

func main() {
	a := q.AtCompileTime[int](func() int { return 21 })
	b := q.AtCompileTime[int](func() int { return a * 2 })
	c := q.AtCompileTime[int](func() int { return a + b })
	fmt.Println("a:", a)
	fmt.Println("b:", b)
	fmt.Println("c:", c)
}
