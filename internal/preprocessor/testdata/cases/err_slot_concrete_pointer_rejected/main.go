package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// MyErr is a concrete error type. Its pointer satisfies the error
// interface, so Go allows `*MyErr` in an error-typed position via
// implicit conversion — but a nil `*MyErr` becomes a *non-nil*
// `error` interface value (typed-nil pitfall).
type MyErr struct{ msg string }

func (e *MyErr) Error() string { return e.msg }

// Foo returns a concrete pointer at the error slot. Passing this to
// q.Try(Foo()) is the exact shape the guard must reject — the
// rewritten `if err != nil` would fire even when Foo returned a nil
// `*MyErr`.
func Foo() (int, *MyErr) {
	return 42, nil
}

func run() (int, error) {
	v := q.Try(Foo())
	return v, nil
}

func main() {
	v, err := run()
	fmt.Println(v, err)
}
