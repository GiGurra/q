// example/recv_ctx mirrors docs/api/recv_ctx.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/recv_ctx
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

type Job struct {
	ID   int
	Name string
}

// ---------- "What q.RecvCtx does" ----------
//
//	v := q.RecvCtx(ctx, ch)
func recvCtxDemo(ctx context.Context, ch <-chan Job) (Job, error) {
	v := q.RecvCtx(ctx, ch)
	return v, nil
}

// ---------- "Chain methods on q.RecvCtxE" ----------
func recvCtxEWrap(ctx context.Context, ch <-chan Job) (Job, error) {
	v := q.RecvCtxE(ctx, ch).Wrap("waiting for job")
	return v, nil
}

func recvCtxECatch(ctx context.Context, ch <-chan Job) (Job, error) {
	v := q.RecvCtxE(ctx, ch).Catch(func(e error) (Job, error) {
		if errors.Is(e, q.ErrChanClosed) {
			return Job{}, nil // normal shutdown
		}
		return Job{}, e // bubble ctx cancellation
	})
	return v, nil
}

// ---------- "Distinguishing cancellation from close" ----------
func classify(ctx context.Context, ch <-chan Job) string {
	_, err := q.RecvRawCtx(ctx, ch)
	switch {
	case errors.Is(err, q.ErrChanClosed):
		return "closed"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case err != nil:
		return "other: " + err.Error()
	}
	return "ok"
}

func openWithJob() chan Job {
	ch := make(chan Job, 1)
	ch <- Job{ID: 1, Name: "build"}
	return ch
}

func openClosed() chan Job {
	ch := make(chan Job)
	close(ch)
	return ch
}

func openBlocking() chan Job {
	return make(chan Job)
}

func main() {
	ctx := context.Background()

	// ch delivers v.
	if j, err := recvCtxDemo(ctx, openWithJob()); err != nil {
		fmt.Printf("recvCtxDemo(open): err=%s\n", err)
	} else {
		fmt.Printf("recvCtxDemo(open): ok job=%d/%s\n", j.ID, j.Name)
	}

	// ch is closed → q.ErrChanClosed.
	_, err := recvCtxDemo(ctx, openClosed())
	fmt.Printf("recvCtxDemo(closed): err=%s\n", err)
	fmt.Printf("recvCtxDemo(closed).is(q.ErrChanClosed): %v\n", errors.Is(err, q.ErrChanClosed))

	// ctx cancelled → ctx.Err().
	cctx, cancel := context.WithTimeout(ctx, 1*time.Millisecond)
	defer cancel()
	_, err = recvCtxDemo(cctx, openBlocking())
	fmt.Printf("recvCtxDemo(timeout): err=%s\n", err)
	fmt.Printf("recvCtxDemo(timeout).is(context.DeadlineExceeded): %v\n", errors.Is(err, context.DeadlineExceeded))

	// Chain: Wrap on closed.
	_, err = recvCtxEWrap(ctx, openClosed())
	fmt.Printf("recvCtxEWrap(closed): err=%s\n", err)

	// Chain: Catch absorbs close, bubbles cancel.
	if j, err := recvCtxECatch(ctx, openClosed()); err != nil {
		fmt.Printf("recvCtxECatch(closed): err=%s\n", err)
	} else {
		fmt.Printf("recvCtxECatch(closed): recovered job=%d\n", j.ID)
	}
	cctx2, cancel2 := context.WithTimeout(ctx, 1*time.Millisecond)
	defer cancel2()
	_, err = recvCtxECatch(cctx2, openBlocking())
	fmt.Printf("recvCtxECatch(timeout).is(context.DeadlineExceeded): %v\n", errors.Is(err, context.DeadlineExceeded))

	// Classify via RecvRawCtx switch.
	fmt.Printf("classify(open): %s\n", classify(ctx, openWithJob()))
	fmt.Printf("classify(closed): %s\n", classify(ctx, openClosed()))
	cctx3, cancel3 := context.WithTimeout(ctx, 1*time.Millisecond)
	defer cancel3()
	fmt.Printf("classify(timeout): %s\n", classify(cctx3, openBlocking()))
}
