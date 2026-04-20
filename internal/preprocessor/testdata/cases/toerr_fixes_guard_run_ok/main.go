package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// MyErr is a concrete error pointer type — the exact shape q's
// typed-nil guard rejects when passed directly to q.Try(...).
type MyErr struct{ msg string }

func (e *MyErr) Error() string { return e.msg }

// FooOK returns a typed-nil *MyErr (the scenario that would
// silently fail as a non-nil error under Go's default conversion).
func FooOK() (int, *MyErr) { return 42, nil }

// FooBad returns a concrete *MyErr.
func FooBad() (int, *MyErr) { return 0, &MyErr{msg: "bad input"} }

// Adapter pattern: q.ToErr collapses the typed-nil, q.Try bubbles
// a real error. The guard is satisfied because q.ToErr's return
// type is literally `(T, error)`.
func okPath() (int, error) {
	v := q.Try(q.ToErr(FooOK()))
	return v, nil
}

func badPath() (int, error) {
	v := q.Try(q.ToErr(FooBad()))
	return v, nil
}

func main() {
	v, err := okPath()
	fmt.Printf("ok: v=%d err_nil=%v\n", v, err == nil)

	_, err = badPath()
	fmt.Printf("bad: err=%v\n", err)
}
