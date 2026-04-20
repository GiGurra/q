package main

import "github.com/GiGurra/q/pkg/q"

type MyErr struct{ msg string }

func (e *MyErr) Error() string { return e.msg }

func Foo() (int, *MyErr) {
	return 7, nil
}

func run() (int, error) {
	v := q.TryE(Foo()).Wrap("context")
	return v, nil
}

func main() { _, _ = run() }
