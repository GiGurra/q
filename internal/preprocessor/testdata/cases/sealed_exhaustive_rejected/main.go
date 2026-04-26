// Negative fixture: q.Exhaustive type switch on a Sealed-marker
// interface with a missing variant case — build must fail.
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

func handle(m Message) {
	switch v := q.Exhaustive(m).(type) {
	case Ping:
		fmt.Println("ping", v.ID)
	case Pong:
		fmt.Println("pong", v.ID)
		// Disconnect missing — build must fail.
	}
}

func main() {
	handle(Ping{ID: 1})
}
