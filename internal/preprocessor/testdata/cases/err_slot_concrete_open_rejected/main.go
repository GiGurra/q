package main

import "github.com/GiGurra/q/pkg/q"

type Handle struct{ id int }

type MyErr struct{ msg string }

func (e *MyErr) Error() string { return e.msg }

func Acquire() (*Handle, *MyErr) {
	return &Handle{id: 1}, nil
}

func run() (*Handle, error) {
	h := q.Open(Acquire()).DeferCleanup(func(h *Handle) {})
	return h, nil
}

func main() { _, _ = run() }
