// example/generator mirrors docs/api/generator.md one-to-one.
// Run with:
//
//	go run -toolexec=q ./example/generator
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- Top-of-doc — fibs ----------
//
//	fibs := q.Generator[int](func() {
//	    a, b := 0, 1
//	    for {
//	        q.Yield(a)
//	        a, b = b, a+b
//	    }
//	})
//	for v := range fibs {
//	    if v > 100 { break }
//	    fmt.Println(v)
//	}
func fibsDemo() []int {
	fibs := q.Generator[int](func() {
		a, b := 0, 1
		for {
			q.Yield(a)
			a, b = b, a+b
		}
	})
	out := []int{}
	for v := range fibs {
		if v > 100 {
			break
		}
		out = append(out, v)
	}
	return out
}

// ---------- "Termination — body returns naturally" ----------
//
//	letters := q.Generator[string](func() {
//	    for _, s := range []string{"a", "b", "c"} {
//	        q.Yield(s)
//	    }
//	})
func lettersDemo() []string {
	letters := q.Generator[string](func() {
		for _, s := range []string{"a", "b", "c"} {
			q.Yield(s)
		}
	})
	out := []string{}
	for s := range letters {
		out = append(out, s)
	}
	return out
}

func main() {
	fmt.Printf("fibsDemo: %v\n", fibsDemo())
	fmt.Printf("lettersDemo: %v\n", lettersDemo())
}
