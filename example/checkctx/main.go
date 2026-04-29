// example/checkctx mirrors docs/api/checkctx.md one-to-one.
// Run with:
//
//	go run -toolexec=q ./example/checkctx
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Item struct{ ID int }

func process(item Item) error { return nil }

// ---------- "What q.CheckCtx does" ----------
//
//	q.CheckCtx(ctx)
func whatCheckCtxDoes(ctx context.Context) error {
	q.CheckCtx(ctx)
	return nil
}

// ---------- "Where to put checkpoints" ----------
//
//	func processBatch(ctx context.Context, items []Item) error {
//	    for _, item := range items {
//	        q.CheckCtx(ctx)
//	        if err := process(item); err != nil {
//	            return err
//	        }
//	    }
//	    return nil
//	}
func processBatch(ctx context.Context, items []Item) error {
	for _, item := range items {
		q.CheckCtx(ctx)
		if err := process(item); err != nil {
			return err
		}
	}
	return nil
}

// ---------- "Chain methods on q.CheckCtxE" ----------
//
//	q.CheckCtxE(ctx).Wrap("loading users")
func checkCtxEWrap(ctx context.Context) error {
	q.CheckCtxE(ctx).Wrap("loading users")
	return nil
}

//	q.CheckCtxE(ctx).Catch(func(e error) error {
//	    if errors.Is(e, context.Canceled) {
//	        return nil
//	    }
//	    return fmt.Errorf("deadline hit: %w", e)
//	})
func checkCtxECatch(ctx context.Context) error {
	q.CheckCtxE(ctx).Catch(func(e error) error {
		if errors.Is(e, context.Canceled) {
			return nil
		}
		return fmt.Errorf("deadline hit: %w", e)
	})
	return nil
}

// Each remaining terminal — exercised on the cancelled path so a
// regression in any reshape would show in the diff.

func checkCtxEErr(ctx context.Context) error {
	q.CheckCtxE(ctx).Err(errors.New("replaced"))
	return nil
}

func checkCtxEErrF(ctx context.Context) error {
	q.CheckCtxE(ctx).ErrF(func(e error) error { return fmt.Errorf("transformed: %w", e) })
	return nil
}

func checkCtxEWrapf(ctx context.Context, id int) error {
	q.CheckCtxE(ctx).Wrapf("loading id=%d", id)
	return nil
}

func main() {
	live := context.Background()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_ = cancelled // keep staticcheck happy; cancelled is read by the calls below

	if err := whatCheckCtxDoes(live); err != nil {
		fmt.Printf("whatCheckCtxDoes(live): err=%s\n", err)
	} else {
		fmt.Println("whatCheckCtxDoes(live): ok")
	}
	if err := whatCheckCtxDoes(cancelled); err != nil {
		fmt.Printf("whatCheckCtxDoes(cancelled): err=%s\n", err)
	}

	items := []Item{{1}, {2}, {3}}
	if err := processBatch(live, items); err != nil {
		fmt.Printf("processBatch(live): err=%s\n", err)
	} else {
		fmt.Println("processBatch(live): ok")
	}
	if err := processBatch(cancelled, items); err != nil {
		fmt.Printf("processBatch(cancelled): err=%s\n", err)
	}

	if err := checkCtxEWrap(cancelled); err != nil {
		fmt.Printf("checkCtxEWrap(cancelled): err=%s\n", err)
	}
	if err := checkCtxECatch(cancelled); err != nil {
		fmt.Printf("checkCtxECatch(cancelled): err=%s\n", err)
	} else {
		fmt.Println("checkCtxECatch(cancelled): ok (suppressed)")
	}

	if err := checkCtxEErr(cancelled); err != nil {
		fmt.Printf("checkCtxEErr(cancelled): err=%s\n", err)
	}
	if err := checkCtxEErrF(cancelled); err != nil {
		fmt.Printf("checkCtxEErrF(cancelled): err=%s\n", err)
	}
	if err := checkCtxEWrapf(cancelled, 42); err != nil {
		fmt.Printf("checkCtxEWrapf(cancelled,42): err=%s\n", err)
	}
}
