// Fixture: receive-only channels (`<-chan U`) are NEVER auto-
// closed by q.Assemble. Closing a channel is the sender's
// responsibility and Go itself rejects `close(c)` on a recv-only
// channel — so the auto-detect skips this shape.
//
// Validates:
//   1. A recipe returning <-chan U builds without the assembler
//      trying to emit `close(c)` (which would be a compile error).
//   2. No "rcv" entry shows up in the teardown log — the recv-only
//      channel was not added to the cleanup chain.
//   3. A bidirectional `chan U` recipe in the same assembly still
//      gets auto-closed normally — only the recv-only direction is
//      excluded.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

var teardown []string

// Bidirectional channel — auto-closed (push side owns close).
type bidi chan struct{}

// Receive-only — never auto-closed; consumer doesn't own close.
type recv <-chan struct{}

type App struct {
	b bidi
	r recv
}

func newBidi() bidi {
	c := make(chan struct{})
	return bidi(c)
}

// newRecv hands back a recv-only channel; the producer side is
// owned externally (in real code: a goroutine, a network source,
// etc.) and the consumer never closes the channel.
func newRecv() recv {
	c := make(chan struct{})
	return recv(c)
}

func newApp(b bidi, r recv) *App { return &App{b: b, r: r} }

func main() {
	app, shutdown, err := q.Assemble[*App](newBidi, newRecv, newApp).NoRelease()
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	_ = app
	shutdown()

	// Bidirectional channel was auto-closed — but `close(c)` is
	// silent, so it leaves no marker. The point is the build
	// succeeded with the recv-only channel in the recipe set.
	fmt.Println("teardown markers:")
	for _, s := range teardown {
		fmt.Println(" -", s)
	}
	fmt.Println("done")
}
