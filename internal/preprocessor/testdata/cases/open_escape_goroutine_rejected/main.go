// Fixture: passing a q.Open DeferCleanup-bound value to `go fn(c)` —
// the spawned goroutine may use the value after this function's
// deferred cleanup has fired.
package main

import "github.com/GiGurra/q/pkg/q"

type Conn struct{ id int }

func (c *Conn) Close() error { return nil }

func dial() (*Conn, error) { return &Conn{id: 1}, nil }

func use(c *Conn) {}

func spawn() error {
	c := q.Open(dial()).DeferCleanup((*Conn).Close)
	go use(c) // BUG — goroutine outlives this function; cleanup fires while it still holds c
	return nil
}

func main() { _ = spawn() }
