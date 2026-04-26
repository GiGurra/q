// Fixture: aliasing a q.Open DeferCleanup-bound value via `c2 := c` and
// returning the alias is the same use-after-close — one-hop aliases
// inherit the dead-set membership.
package main

import "github.com/GiGurra/q/pkg/q"

type Conn struct{ id int }

func (c *Conn) Close() error { return nil }

func dial() (*Conn, error) { return &Conn{id: 1}, nil }

func factory() (*Conn, error) {
	c := q.Open(dial()).DeferCleanup((*Conn).Close)
	c2 := c
	return c2, nil // BUG — c2 is an alias of the about-to-be-closed c
}

func main() { _, _ = factory() }
