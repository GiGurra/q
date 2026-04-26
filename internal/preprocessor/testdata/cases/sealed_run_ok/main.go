// Fixture: q.Sealed — interface-based sealed sums with payload data.
// Demonstrates message-passing patterns: producer constructs variant
// values directly, consumer dispatches via Go type switch with
// q.Exhaustive coverage and via q.Match + q.OnType expression form.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// Marker interface — one method, no args, no result. The preprocessor
// synthesises `func (V) message() {}` on each variant declared in the
// q.Sealed directive below.
type Message interface{ message() }

type Ping struct {
	ID int
}
type Pong struct {
	ID int
}
type Disconnect struct {
	Reason string
}

var _ = q.Sealed[Message](Ping{}, Pong{}, Disconnect{})

// Producer side: variants flow through a Message-typed channel as
// themselves — Go's type system enforces the interface at the send
// site via the synthesised marker.
func produce(ch chan<- Message) {
	ch <- Ping{ID: 1}
	ch <- Pong{ID: 2}
	ch <- Disconnect{Reason: "timeout"}
	close(ch)
}

// Consumer side (statement form): Go type switch with q.Exhaustive
// coverage. Forgetting any variant fails the build.
func handleStmt(m Message) string {
	switch v := q.Exhaustive(m).(type) {
	case Ping:
		return fmt.Sprintf("ping %d", v.ID)
	case Pong:
		return fmt.Sprintf("pong %d", v.ID)
	case Disconnect:
		return fmt.Sprintf("dc: %s", v.Reason)
	}
	return "?"
}

// Consumer side (expression form): q.Match + q.OnType binds the
// typed payload.
func handleMatch(m Message) string {
	return q.Match(m,
		q.OnType(func(p Ping) string { return fmt.Sprintf("m-ping %d", p.ID) }),
		q.OnType(func(p Pong) string { return fmt.Sprintf("m-pong %d", p.ID) }),
		q.OnType(func(d Disconnect) string { return fmt.Sprintf("m-dc: %s", d.Reason) }),
	)
}

// q.Match also accepts q.Case for tag-only arms (payload discarded):
func quickKind(m Message) string {
	return q.Match(m,
		q.Case(Ping{}, "p"),
		q.Case(Pong{}, "P"),
		q.Case(Disconnect{}, "D"),
	)
}

// Default arm waives the missing-case rule:
func partial(m Message) string {
	return q.Match(m,
		q.OnType(func(p Ping) string { return fmt.Sprintf("only-ping %d", p.ID) }),
		q.Default("not a ping"),
	)
}

func main() {
	ch := make(chan Message, 4)
	produce(ch)

	for m := range ch {
		fmt.Println(handleStmt(m))
		fmt.Println(handleMatch(m))
		fmt.Println(quickKind(m))
		fmt.Println(partial(m))
	}

	// Direct construction: variants implement Message via the
	// synthesised marker — assignment just works.
	var m Message = Ping{ID: 99}
	fmt.Println("direct:", handleStmt(m))
}
