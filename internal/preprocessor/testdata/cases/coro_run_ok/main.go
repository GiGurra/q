// Fixture: q.Coro — bidirectional coroutines via goroutine + two
// channels. Pure runtime, no preprocessor work.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	// Doubler: each Resume(v) returns 2*v.
	doubler := q.Coro(func(in <-chan int, out chan<- int) {
		for v := range in {
			out <- v * 2
		}
	})
	for _, v := range []int{1, 21, 100, 7} {
		got, ok := doubler.Resume(v)
		fmt.Println("Doubler:", got, ok)
	}
	doubler.Close()
	doubler.Wait()
	fmt.Println("Doubler Done:", doubler.Done())

	// Resume after Close returns (zero, false).
	v, ok := doubler.Resume(99)
	fmt.Println("Resume after Close:", v, ok)

	// Stateful counter: each Resume returns the running sum.
	summer := q.Coro(func(in <-chan int, out chan<- int) {
		sum := 0
		for v := range in {
			sum += v
			out <- sum
		}
	})
	defer summer.Close()
	for _, v := range []int{10, 20, 30, 40} {
		got, _ := summer.Resume(v)
		fmt.Println("Summer:", got)
	}

	// Body that returns on its own (after exactly 2 inputs).
	twice := q.Coro(func(in <-chan int, out chan<- int) {
		for i := 0; i < 2; i++ {
			v, ok := <-in
			if !ok {
				return
			}
			out <- v * 10
		}
		// Returns here — subsequent Resume should give (zero, false).
	})
	v1, _ := twice.Resume(1)
	v2, _ := twice.Resume(2)
	v3, ok3 := twice.Resume(3) // body has returned by now
	fmt.Println("Twice:", v1, v2, v3, ok3)
	twice.Wait()

	// Generator pattern: body emits a sequence and returns. Caller
	// reads "echo" inputs to drive each step.
	type token struct{}
	fibs := q.Coro(func(in <-chan token, out chan<- int) {
		a, b := 0, 1
		for range in {
			out <- a
			a, b = b, a+b
		}
	})
	defer fibs.Close()
	for range 6 {
		n, _ := fibs.Resume(token{})
		fmt.Println("Fib:", n)
	}

	// String coroutine — different I / O types work independently
	// of the doubler/summer above.
	upper := q.Coro(func(in <-chan string, out chan<- string) {
		for s := range in {
			out <- "UPPER:" + s
		}
	})
	defer upper.Close()
	got, _ := upper.Resume("hello")
	fmt.Println(got)

	// Idempotent Close.
	upper.Close()
	upper.Close()
	fmt.Println("Idempotent Close OK")
}
