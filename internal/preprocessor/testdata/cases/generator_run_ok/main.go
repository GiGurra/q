// Fixture: q.Generator + q.Yield — sugar over Go 1.23 iter.Seq[T].
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	// Fibonacci generator. Body has no return type; q.Yield produces
	// the next value in the sequence, the consumer ranges over it.
	fibs := q.Generator[int](func() {
		a, b := 0, 1
		for {
			q.Yield(a)
			a, b = b, a+b
		}
	})

	// Pull the first 7 values via range.
	count := 0
	for v := range fibs {
		fmt.Println("fib:", v)
		count++
		if count >= 7 {
			break
		}
	}

	// Finite generator — body returns naturally, range exits.
	letters := q.Generator[string](func() {
		for _, s := range []string{"a", "b", "c"} {
			q.Yield(s)
		}
	})
	for s := range letters {
		fmt.Println("letter:", s)
	}

	// Yield an arbitrary expression — the rewriter must splice the
	// expression text verbatim, not the variable identifier.
	doubled := q.Generator[int](func() {
		for i := 1; i <= 3; i++ {
			q.Yield(i * 2)
		}
	})
	for v := range doubled {
		fmt.Println("doubled:", v)
	}

	// Multi-yield in one body — early-return after yield-no.
	pair := q.Generator[int](func() {
		q.Yield(100)
		q.Yield(200)
		q.Yield(300)
	})
	for v := range pair {
		fmt.Println("pair:", v)
		if v == 200 {
			break // exercise the if-!yield-return path
		}
	}
}
