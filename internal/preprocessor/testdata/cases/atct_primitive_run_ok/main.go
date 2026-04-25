package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	n := q.AtCompileTime[int](func() int {
		sum := 0
		for i := 1; i <= 100; i++ {
			sum += i
		}
		return sum
	})
	fmt.Println("sum 1..100:", n)

	greeting := q.AtCompileTime[string](func() string {
		return "hello, comptime"
	})
	fmt.Println("greeting:", greeting)

	flag := q.AtCompileTime[bool](func() bool {
		return 2*2 == 4
	})
	fmt.Println("flag:", flag)

	pi := q.AtCompileTime[float64](func() float64 {
		return 3.14159
	})
	fmt.Println("pi:", pi)
}
