// Fixture: closing a resource synchronously and then returning it.
// Same use-after-close — caller receives a closed resource. The
// detection covers the user-written close pattern, not just the
// q.Open auto-defer one.
package main

import (
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

type Conn struct{ id int }

func (c *Conn) Close() error { return nil }

func dial() *Conn { return &Conn{id: 1} }

// q usage so the preprocessor runs on this file.
func parseAndUse(s string) (int, error) {
	n := q.Try(strconv.Atoi(s))
	return n + 1, nil
}

func factory() *Conn {
	c := dial()
	c.Close()
	return c // BUG — c was just closed
}

func main() {
	_, _ = parseAndUse("1")
	_ = factory()
}
