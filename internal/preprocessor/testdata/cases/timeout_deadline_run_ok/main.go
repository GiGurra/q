// Fixture: q.Timeout / q.Deadline — derive a cancel-deferred child
// context in one line. Covers define and assign forms plus the
// actual runtime semantics (child ctx fires on deadline).
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

// defineForm — newCtx := q.Timeout(parent, dur). After the function
// returns the cancel has already fired (auto-deferred), so `newCtx`
// shows canceled from outside.
func defineForm(parent context.Context) (derived context.Context) {
	newCtx := q.Timeout(parent, 5*time.Millisecond)
	// Sleep past the deadline so we can observe ctx.Err() below.
	time.Sleep(20 * time.Millisecond)
	return newCtx
}

// assignForm — ctx = q.Timeout(ctx, dur). Shadows the caller's
// variable and installs the deferred cancel transparently.
func assignForm(ctx context.Context) error {
	ctx = q.Timeout(ctx, 5*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	// CheckCtx so we can observe that the derived ctx did expire.
	q.CheckCtx(ctx)
	return nil
}

// deadlineForm — q.Deadline takes an absolute time.
func deadlineForm(parent context.Context) error {
	ctx := q.Deadline(parent, time.Now().Add(5*time.Millisecond))
	time.Sleep(20 * time.Millisecond)
	q.CheckCtx(ctx)
	return nil
}

// cancelFiresEarly — the auto-defer of cancel means the child ctx is
// cancelled when the function returns, even if the deadline hasn't
// hit yet. Useful guard against goroutine ctx leaks.
func cancelFiresEarly(parent context.Context) context.Context {
	c := q.Timeout(parent, 1*time.Hour)
	// No sleep; defer cancel() fires at return.
	return c
}

func main() {
	bg := context.Background()

	// defineForm — the returned ctx has expired (DeadlineExceeded).
	d := defineForm(bg)
	fmt.Printf("define.err: %v\n", d.Err())

	// assignForm — the deadline fires before CheckCtx, so CheckCtx
	// bubbles context.DeadlineExceeded.
	err := assignForm(bg)
	fmt.Printf("assign.err: %v\n", err)
	fmt.Printf("assign.isDeadline: %v\n", errors.Is(err, context.DeadlineExceeded))

	// deadlineForm — same, but deadline set via absolute time.
	err = deadlineForm(bg)
	fmt.Printf("deadline.err: %v\n", err)

	// cancelFiresEarly — return ctx with auto-cancelled state
	// (Canceled, not DeadlineExceeded — defer cancel() fired, the
	// 1h deadline never did).
	early := cancelFiresEarly(bg)
	fmt.Printf("early.err: %v\n", early.Err())
	fmt.Printf("early.isCanceled: %v\n", errors.Is(early.Err(), context.Canceled))
}
