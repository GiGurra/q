// Fixture: q.Bubble + q.BubbleE — statement-only ctx.Err()
// checkpoint that bubbles when ctx has been cancelled or deadlined.
// Covers bare Bubble plus every BubbleE chain method on both a
// live ctx (no bubble) and a cancelled ctx (bubble fires).
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

var ErrBusiness = errors.New("business")

// bareBubble — live ctx falls through, cancelled ctx bubbles
// ctx.Err() (context.Canceled).
func bareBubble(ctx context.Context) error {
	q.Bubble(ctx)
	return nil
}

// barePair exercises the (T, error) signature — same shape, just
// two-slot zero return.
func barePair(ctx context.Context) (int, error) {
	q.Bubble(ctx)
	return 42, nil
}

// errMethod replaces ctx.Err() with a constant.
func errMethod(ctx context.Context) error {
	q.BubbleE(ctx).Err(ErrBusiness)
	return nil
}

// errFMethod transforms the captured err.
func errFMethod(ctx context.Context) error {
	q.BubbleE(ctx).ErrF(func(e error) error {
		return fmt.Errorf("translated: %w", e)
	})
	return nil
}

// wrapMethod wraps with a message.
func wrapMethod(ctx context.Context) error {
	q.BubbleE(ctx).Wrap("loading")
	return nil
}

// wrapfMethod wraps with a formatted message.
func wrapfMethod(ctx context.Context, id int) error {
	q.BubbleE(ctx).Wrapf("loading item %d", id)
	return nil
}

// catchSuppressOnCanceled — return nil from Catch to swallow the
// cancellation (non-default but occasionally useful).
func catchSuppress(ctx context.Context) error {
	q.BubbleE(ctx).Catch(func(e error) error {
		if errors.Is(e, context.Canceled) {
			return nil
		}
		return e
	})
	return nil
}

// catchBubble — return a new error to bubble a shaped one.
func catchBubble(ctx context.Context) error {
	q.BubbleE(ctx).Catch(func(e error) error {
		return fmt.Errorf("caught: %w", e)
	})
	return nil
}

func report(name string, err error) {
	if err == nil {
		fmt.Printf("%s: ok\n", name)
		return
	}
	fmt.Printf("%s: %s\n", name, err)
}
func reportPair(name string, _ int, err error) { report(name, err) }

func main() {
	live, cancel := context.WithCancel(context.Background())
	defer cancel()

	dead, cancelDead := context.WithCancel(context.Background())
	cancelDead()

	// Bare Bubble — live passes, cancelled bubbles ctx.Err().
	report("bare.live", bareBubble(live))
	report("bare.cancelled", bareBubble(dead))

	// (T, error) signature.
	n, err := barePair(live)
	fmt.Printf("pair.live: n=%d err=%v\n", n, err)
	_, err = barePair(dead)
	reportPair("pair.cancelled", 0, err)

	// Chain methods on live + cancelled.
	report("err.live", errMethod(live))
	report("err.cancelled", errMethod(dead))

	report("errF.live", errFMethod(live))
	report("errF.cancelled", errFMethod(dead))

	report("wrap.live", wrapMethod(live))
	report("wrap.cancelled", wrapMethod(dead))

	report("wrapf.live", wrapfMethod(live, 7))
	report("wrapf.cancelled", wrapfMethod(dead, 7))

	// Catch: suppress on Canceled, bubble otherwise.
	report("catch.suppress.cancelled", catchSuppress(dead))
	report("catch.bubble.cancelled", catchBubble(dead))

	// errors.Is chain — Wrap preserves %w identity back to
	// context.Canceled.
	err = wrapMethod(dead)
	fmt.Printf("wrap.isCanceled: %v\n", errors.Is(err, context.Canceled))
}
