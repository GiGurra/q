// Fixture: sending a q.Open DeferCleanup-bound value on a channel — the
// receiver may use it after the deferred cleanup has fired.
package main

import "github.com/GiGurra/q/pkg/q"

type Conn struct{ id int }

func (c *Conn) Close() error { return nil }

func dial() (*Conn, error) { return &Conn{id: 1}, nil }

func send(out chan<- *Conn) error {
	c := q.Open(dial()).DeferCleanup((*Conn).Close)
	out <- c // BUG — receiver gets a soon-to-be-closed Conn
	return nil
}

func main() {
	ch := make(chan *Conn, 1)
	_ = send(ch)
}
