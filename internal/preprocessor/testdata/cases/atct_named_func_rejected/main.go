package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

func compute() int { return 7 * 7 }

func main() {
	// Argument must be a function literal — passing a named function
	// reference is rejected.
	n := q.AtCompileTime[int](compute)
	fmt.Println(n)
}
