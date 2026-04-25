// Fixture: storing a q.Open Release-bound value into a struct field
// is a use-after-close — the field outlives the function, but the
// deferred cleanup fires on function return.
package main

import "github.com/GiGurra/q/pkg/q"

type Conn struct{ id int }

func (c *Conn) Close() error { return nil }

func dial() (*Conn, error) { return &Conn{id: 1}, nil }

type Pool struct {
	c *Conn
}

func (p *Pool) acquire() error {
	c := q.Open(dial()).Release((*Conn).Close)
	p.c = c // BUG — p.c outlives this function; cleanup fires before p.c is read
	return nil
}

func main() {
	p := &Pool{}
	_ = p.acquire()
}
