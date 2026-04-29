//go:build q_sealed_demo

// example/sealed mirrors docs/api/sealed.md one-to-one. The build tag
// `q_sealed_demo` gates it because the pre-rewrite source uses
// synthesised marker methods that plain Go can't see (gopls /
// golangci-lint would flag every `Ping{}` send to `chan Message` as a
// type error). The TestExamples harness passes `-tags=q_sealed_demo`
// when invoking it.
//
// Run with:
//
//	go run -toolexec=q -tags=q_sealed_demo ./example/sealed
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- "At a glance" ----------
type Message interface{ message() } // 1-line marker — name is yours

type Ping struct{ ID int }
type Pong struct{ ID int }
type Disconnect struct{ Reason string }

var _ = q.Sealed[Message](Ping{}, Pong{}, Disconnect{})

// ---------- Statement-form dispatch ----------
func handle(m Message) string {
	switch v := q.Exhaustive(m).(type) {
	case Ping:
		return fmt.Sprintf("ping %d", v.ID)
	case Pong:
		return fmt.Sprintf("pong %d", v.ID)
	case Disconnect:
		return fmt.Sprintf("dc: %s", v.Reason)
	}
	return ""
}

// ---------- Expression-form dispatch (q.Match + q.OnType) ----------
func describe(m Message) string {
	return q.Match(m,
		q.OnType(func(p Ping) string { return fmt.Sprintf("ping %d", p.ID) }),
		q.OnType(func(p Pong) string { return fmt.Sprintf("pong %d", p.ID) }),
		q.OnType(func(d Disconnect) string { return fmt.Sprintf("dc: %s", d.Reason) }),
	)
}

// ---------- "q.Default waives the missing-variant rule" ----------
func describePingOnly(m Message) string {
	return q.Match(m,
		q.OnType(func(p Ping) string { return fmt.Sprintf("only-ping %d", p.ID) }),
		q.Default("not a ping"),
	)
}

// ---------- "Construction — direct, no helpers needed" ----------
func produce(ch chan<- Message) {
	ch <- Ping{ID: 1}
	ch <- Pong{ID: 2}
	ch <- Disconnect{Reason: "timeout"}
	close(ch)
}

func main() {
	ch := make(chan Message, 3)
	produce(ch)
	for m := range ch {
		fmt.Printf("handle: %s\n", handle(m))
	}

	for _, m := range []Message{Ping{ID: 7}, Pong{ID: 8}, Disconnect{Reason: "bye"}} {
		fmt.Printf("describe: %s\n", describe(m))
	}

	for _, m := range []Message{Ping{ID: 9}, Pong{ID: 10}, Disconnect{Reason: "x"}} {
		fmt.Printf("describePingOnly: %s\n", describePingOnly(m))
	}
}
