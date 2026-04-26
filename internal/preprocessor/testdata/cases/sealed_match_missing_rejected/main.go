// Negative fixture: q.Match on a Sealed-marker interface with a
// missing variant arm and no q.Default — build must fail.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Message interface{ message() }

type Ping struct{ ID int }
type Pong struct{ ID int }
type Disconnect struct{ Reason string }

var _ = q.Sealed[Message](Ping{}, Pong{}, Disconnect{})

func describe(m Message) string {
	return q.Match(m,
		q.OnType(func(p Ping) string { return fmt.Sprintf("ping %d", p.ID) }),
		q.OnType(func(p Pong) string { return fmt.Sprintf("pong %d", p.ID) }),
		// Disconnect missing — build must fail.
	)
}

func main() {
	_ = describe(Ping{ID: 1})
}
