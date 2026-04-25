// Fixture: registering `defer c.Close()` and then returning c. The
// deferred close fires at function exit, so the caller receives a
// closed resource.
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
	defer c.Close()
	return c // BUG — c will be closed by the deferred call before the caller uses it
}

func main() {
	_, _ = parseAndUse("1")
	_ = factory()
}
