package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	primes := q.AtCompileTime[[]int](func() []int {
		out := []int{}
		for n := 2; len(out) < 10; n++ {
			isPrime := true
			for _, p := range out {
				if p*p > n {
					break
				}
				if n%p == 0 {
					isPrime = false
					break
				}
			}
			if isPrime {
				out = append(out, n)
			}
		}
		return out
	})
	weights := q.AtCompileTime[map[string]int](func() map[string]int {
		return map[string]int{"a": 1, "b": 2, "c": 3}
	})
	pairs := q.AtCompileTime[[][]string](func() [][]string {
		return [][]string{{"alice", "30"}, {"bob", "25"}}
	})
	fmt.Println("primes:", primes)
	fmt.Println("weights[a]:", weights["a"])
	fmt.Println("weights[b]:", weights["b"])
	fmt.Println("weights[c]:", weights["c"])
	fmt.Println("pairs:", pairs)
}
