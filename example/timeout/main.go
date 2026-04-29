// example/timeout mirrors docs/api/timeout.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/timeout
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- "What q.Timeout does — shadowing form" ----------
func shadowingForm() error {
	ctx := context.Background()
	ctx = q.Timeout(ctx, 1*time.Millisecond)
	<-ctx.Done()
	return ctx.Err()
}

// ---------- "Define form — preserve parent under a different name" ----------
func defineForm(parent context.Context) (parentErr, tightErr error) {
	tight := q.Timeout(parent, 1*time.Millisecond)
	<-tight.Done()
	return parent.Err(), tight.Err()
}

// ---------- "q.Deadline — propagating an inherited deadline" ----------
func deadlineForm(parent context.Context) error {
	ctx := q.Deadline(parent, time.Now().Add(1*time.Millisecond))
	<-ctx.Done()
	return ctx.Err()
}

// ---------- "Example" — auto-cancel pattern ----------
type Result struct{ Body string }

func slowFetch(ctx context.Context, _ string) (Result, error) {
	select {
	case <-time.After(50 * time.Millisecond):
		return Result{Body: "ok"}, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

func fetch(parent context.Context, url string) (Result, error) {
	ctx := q.Timeout(parent, 1*time.Millisecond)
	return slowFetch(ctx, url)
}

func main() {
	if err := shadowingForm(); err != nil {
		fmt.Printf("shadowingForm: err=%s is(DeadlineExceeded)=%v\n", err, errors.Is(err, context.DeadlineExceeded))
	}

	parent := context.Background()
	pErr, tErr := defineForm(parent)
	fmt.Printf("defineForm: parent.Err=%v tight.Err=%v\n", pErr, tErr)

	if err := deadlineForm(parent); err != nil {
		fmt.Printf("deadlineForm: err=%s is(DeadlineExceeded)=%v\n", err, errors.Is(err, context.DeadlineExceeded))
	}

	if _, err := fetch(parent, "https://example.com"); err != nil {
		fmt.Printf("fetch: err=%s is(DeadlineExceeded)=%v\n", err, errors.Is(err, context.DeadlineExceeded))
	}
}
