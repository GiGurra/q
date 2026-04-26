// Negative fixture: q.Sealed[I] where I has more than one method —
// must be a single-marker interface. Build must fail.
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

// TWO methods — q.Sealed only handles 1-method markers.
type Message interface {
	message()
	other()
}

type Ping struct{ ID int }

var _ = q.Sealed[Message](Ping{})

func main() {}
