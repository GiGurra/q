// Fixture: returning a q.Open DeferCleanup-bound value from the same
// function is a use-after-close — the deferred cleanup fires on
// return, so the caller receives a closed resource.
package main

import "github.com/GiGurra/q/pkg/q"

type Conn struct{ id int }

func (c *Conn) Close() error { return nil }

func dial() (*Conn, error) { return &Conn{id: 1}, nil }

func factory() (*Conn, error) {
	c := q.Open(dial()).DeferCleanup((*Conn).Close)
	return c, nil // BUG — c is closed by the deferred cleanup before the caller sees it
}

func main() { _, _ = factory() }
