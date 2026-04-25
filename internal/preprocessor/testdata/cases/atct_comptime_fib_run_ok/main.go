package main

import (
	"fmt"

	"fixture/fib"
)

// Calls to fib.Fib resolve at preprocess time — the recursive Go
// function runs in the synthesis subprocess; the call site is
// replaced with the literal computed value.

func main() {
	fmt.Println("fib(10):", fib.Fib(10))
	fmt.Println("fib(15):", fib.Fib(15))
	fmt.Println("square(7):", fib.Square(7))
	// Composing: a value-returning AtCompileTime can sit alongside
	// comptime calls — both get processed in the same synthesis pass.
}
