package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	x := 42
	n := q.AtCompileTime[int](func() int {
		return x * 2 // captures local var x — not allowed
	})
	fmt.Println(n)
}
