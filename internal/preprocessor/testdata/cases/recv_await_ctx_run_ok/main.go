// Fixture: q.RecvCtx + q.AwaitCtx + their E-variants.
// Covers three paths per family:
//  1. Happy path — value arrives before ctx cancels.
//  2. Close/close — channel closed or future errors.
//  3. Ctx cancellation fires first — returns ctx.Err().
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

// recvBare — bare q.RecvCtx: bubbles ctx.Err() on cancel or
// ErrChanClosed on close.
func recvBare(ctx context.Context, ch <-chan int) (int, error) {
	return q.RecvCtx(ctx, ch), nil
}

// recvWrap — q.RecvCtxE.Wrap shapes the bubbled error.
func recvWrap(ctx context.Context, ch <-chan int) (int, error) {
	return q.RecvCtxE(ctx, ch).Wrap("recv"), nil
}

// recvCatch — q.RecvCtxE.Catch recovers on channel close only.
func recvCatch(ctx context.Context, ch <-chan int) (int, error) {
	return q.RecvCtxE(ctx, ch).Catch(func(e error) (int, error) {
		if errors.Is(e, q.ErrChanClosed) {
			return 99, nil
		}
		return 0, e
	}), nil
}

// awaitBare — bare q.AwaitCtx bubbles ctx.Err() or the future's err.
func awaitBare(ctx context.Context, f q.Future[int]) (int, error) {
	return q.AwaitCtx(ctx, f), nil
}

// awaitWrap — q.AwaitCtxE.Wrap shapes the bubble.
func awaitWrap(ctx context.Context, f q.Future[int]) (int, error) {
	return q.AwaitCtxE(ctx, f).Wrap("await"), nil
}

// awaitErr — replace ctx.Err with a sentinel.
var ErrTimedOut = errors.New("timed out")

func awaitErr(ctx context.Context, f q.Future[int]) (int, error) {
	return q.AwaitCtxE(ctx, f).Err(ErrTimedOut), nil
}

func report(name string, n int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
		return
	}
	fmt.Printf("%s: ok=%d\n", name, n)
}

func slowFetch(v int, err error) func() (int, error) {
	return func() (int, error) {
		time.Sleep(40 * time.Millisecond)
		return v, err
	}
}

func main() {
	live := context.Background()

	// RecvCtx happy path — value already in buffered channel.
	ch := make(chan int, 1)
	ch <- 7
	n, err := recvBare(live, ch)
	report("recv.ok", n, err)

	// RecvCtx channel closed — bubble ErrChanClosed.
	closed := make(chan int)
	close(closed)
	n, err = recvBare(live, closed)
	report("recv.closed", n, err)

	// RecvCtx ctx cancelled — bubble ctx.Err().
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	empty := make(chan int)
	n, err = recvBare(cancelled, empty)
	report("recv.cancelled", n, err)

	// RecvCtxE.Wrap — wraps the close bubble with a prefix.
	closed2 := make(chan int)
	close(closed2)
	n, err = recvWrap(live, closed2)
	report("recv.wrap.closed", n, err)

	// RecvCtxE.Catch — recover only from close.
	closed3 := make(chan int)
	close(closed3)
	n, err = recvCatch(live, closed3)
	report("recv.catch.closed", n, err)

	cancelled2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	empty2 := make(chan int)
	n, err = recvCatch(cancelled2, empty2)
	report("recv.catch.cancelled", n, err)

	// AwaitCtx happy path — future completes under ctx.
	ok := q.Async(func() (int, error) { return 42, nil })
	n, err = awaitBare(live, ok)
	report("await.ok", n, err)

	// AwaitCtx ctx fires first — bubble ctx.Err().
	timeout, tcancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	slow := q.Async(slowFetch(1, nil))
	n, err = awaitBare(timeout, slow)
	report("await.timeout", n, err)
	tcancel()

	// AwaitCtx future error.
	errF := q.Async(func() (int, error) { return 0, errors.New("boom") })
	n, err = awaitBare(live, errF)
	report("await.fail", n, err)

	// AwaitCtxE.Wrap on future error.
	errF2 := q.Async(func() (int, error) { return 0, errors.New("boom") })
	n, err = awaitWrap(live, errF2)
	report("await.wrap.fail", n, err)

	// AwaitCtxE.Err — replace ctx.Err with sentinel.
	timeout2, t2cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	slow2 := q.Async(slowFetch(1, nil))
	_, err = awaitErr(timeout2, slow2)
	t2cancel()
	fmt.Printf("await.err.isTimedOut: %v\n", errors.Is(err, ErrTimedOut))
}
