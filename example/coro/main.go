// example/coro mirrors docs/api/coro.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/coro
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- Top-of-doc — doubler ----------
//
//	doubler := q.Coro(func(in <-chan int, out chan<- int) {
//	    for v := range in {
//	        out <- v * 2
//	    }
//	})
func doublerDemo() (int, int) {
	doubler := q.Coro(func(in <-chan int, out chan<- int) {
		for v := range in {
			out <- v * 2
		}
	})
	defer doubler.Close()

	a, _ := doubler.Resume(21)
	b, _ := doubler.Resume(100)
	return a, b
}

// ---------- "Cooperative protocol — token-style fibs" ----------
//
//	type tick struct{}
//	fibs := q.Coro(func(in <-chan tick, out chan<- int) {
//	    a, b := 0, 1
//	    for range in {
//	        out <- a
//	        a, b = b, a+b
//	    }
//	})
type tick struct{}

func fibsDemo() []int {
	fibs := q.Coro(func(in <-chan tick, out chan<- int) {
		a, b := 0, 1
		for range in {
			out <- a
			a, b = b, a+b
		}
	})
	defer fibs.Close()

	out := make([]int, 0, 10)
	for range 10 {
		n, _ := fibs.Resume(tick{})
		out = append(out, n)
	}
	return out
}

// ---------- "Stateful conversation — running sum" ----------
//
//	summer := q.Coro(func(in <-chan int, out chan<- int) {
//	    sum := 0
//	    for v := range in {
//	        sum += v
//	        out <- sum
//	    }
//	})
func summerDemo() []int {
	summer := q.Coro(func(in <-chan int, out chan<- int) {
		sum := 0
		for v := range in {
			sum += v
			out <- sum
		}
	})
	defer summer.Close()

	a, _ := summer.Resume(10)
	b, _ := summer.Resume(20)
	c, _ := summer.Resume(30)
	return []int{a, b, c}
}

// ---------- "Termination — body finishes after 2 inputs" ----------
//
//	twice := q.Coro(func(in <-chan int, out chan<- int) {
//	    for i := 0; i < 2; i++ {
//	        v, ok := <-in
//	        if !ok { return }
//	        out <- v * 10
//	    }
//	})
func twiceDemo() (int, int, int, bool) {
	twice := q.Coro(func(in <-chan int, out chan<- int) {
		for i := 0; i < 2; i++ {
			v, ok := <-in
			if !ok {
				return
			}
			out <- v * 10
		}
	})

	a, _ := twice.Resume(1)
	b, _ := twice.Resume(2)
	c, ok := twice.Resume(3)
	twice.Wait()
	return a, b, c, ok
}

func main() {
	a, b := doublerDemo()
	fmt.Printf("doublerDemo: %d, %d\n", a, b)

	fmt.Printf("fibsDemo: %v\n", fibsDemo())

	fmt.Printf("summerDemo: %v\n", summerDemo())

	a2, b2, c2, ok2 := twiceDemo()
	fmt.Printf("twiceDemo: a=%d b=%d c=%d ok-after-done=%v\n", a2, b2, c2, ok2)
}
