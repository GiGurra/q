package main

import "github.com/GiGurra/q/pkg/q"

type MyErr struct{ msg string }

func (e *MyErr) Error() string { return e.msg }

func Validate() *MyErr { return nil }

func run() error {
	q.Check(Validate())
	return nil
}

func main() { _ = run() }
