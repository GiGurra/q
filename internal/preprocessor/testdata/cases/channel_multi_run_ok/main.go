// Fixture: channel fan-in family — q.RecvAny (+ Ctx/E), q.Drain
// (runtime, no bubble), q.DrainAll (runtime, no bubble), and the
// ctx-aware q.DrainCtx / q.DrainAllCtx (+ E).
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

// --- RecvAny ---

func recvAnyFirst(a, b <-chan int) (int, error) {
	return q.RecvAny(a, b), nil
}

func recvAnyCtx(ctx context.Context, a, b <-chan int) (int, error) {
	return q.RecvAnyCtx(ctx, a, b), nil
}

// RecvAnyE.Catch recovers from close — keep the "winner" call side
// flat while skipping the close-bubble entirely.
func recvAnyCatchClose(a, b <-chan int) (int, error) {
	return q.RecvAnyE(a, b).Catch(func(e error) (int, error) {
		if errors.Is(e, q.ErrChanClosed) {
			return -1, nil
		}
		return 0, e
	}), nil
}

// --- Drain ---

// drain collects from a finite channel (runtime — no bubble).
func drain(ch <-chan int) []int {
	return q.Drain(ch)
}

// drainCtx bubbles ctx.Err() on cancel.
func drainCtx(ctx context.Context, ch <-chan int) ([]int, error) {
	return q.DrainCtx(ctx, ch), nil
}

func drainCtxWrap(ctx context.Context, ch <-chan int) ([]int, error) {
	return q.DrainCtxE(ctx, ch).Wrap("drain"), nil
}

// --- DrainAll ---

// drainAll — runtime, per-channel in input order.
func drainAll(a, b, c <-chan int) [][]int {
	return q.DrainAll(a, b, c)
}

// drainAllCtx bubbles ctx.Err() on cancel.
func drainAllCtx(ctx context.Context, a, b <-chan int) ([][]int, error) {
	return q.DrainAllCtx(ctx, a, b), nil
}

// ---- helpers ----

func feed(values ...int) <-chan int {
	ch := make(chan int, len(values))
	for _, v := range values {
		ch <- v
	}
	close(ch)
	return ch
}

// feedDelayed sends the values after a delay, then closes.
func feedDelayed(delay time.Duration, values ...int) <-chan int {
	ch := make(chan int, len(values))
	go func() {
		time.Sleep(delay)
		for _, v := range values {
			ch <- v
		}
		close(ch)
	}()
	return ch
}

// neverCloses returns a channel that stays open with one buffered send
// (so we can drain that one value) and then blocks forever on further
// receives. Use alongside a ctx timeout to test cancellation.
func neverCloses(v int) <-chan int {
	ch := make(chan int, 1)
	ch <- v
	return ch
}

func printSlice(name string, vs []int) {
	var parts []string
	for _, v := range vs {
		parts = append(parts, fmt.Sprintf("%d", v))
	}
	fmt.Printf("%s: [%s]\n", name, strings.Join(parts, ","))
}

func report(name string, v int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
		return
	}
	fmt.Printf("%s: ok=%d\n", name, v)
}

func reportSlice(name string, vs []int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
		return
	}
	printSlice(name, vs)
}

func main() {
	// RecvAny — first value wins. One channel has a buffered value,
	// the other is empty (would block forever).
	buffered := make(chan int, 1)
	buffered <- 7
	empty := make(chan int)
	v, err := recvAnyFirst(buffered, empty)
	report("recvAny.first", v, err)

	// RecvAny on a closed channel bubbles ErrChanClosed.
	closed := make(chan int)
	close(closed)
	empty2 := make(chan int)
	v, err = recvAnyFirst(closed, empty2)
	report("recvAny.closed", v, err)

	// RecvAnyE.Catch recovers the close into a sentinel value.
	closed2 := make(chan int)
	close(closed2)
	empty3 := make(chan int)
	v, err = recvAnyCatchClose(closed2, empty3)
	report("recvAny.catchClose", v, err)

	// RecvAnyCtx — ctx fires first.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	e4 := make(chan int)
	e5 := make(chan int)
	v, err = recvAnyCtx(cancelled, e4, e5)
	report("recvAny.ctx", v, err)

	// Drain — gather from closed finite channel.
	printSlice("drain.three", drain(feed(10, 20, 30)))
	printSlice("drain.empty", drain(feed()))

	// DrainCtx — cancel fires, values already in buffer discarded.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Millisecond)
	vs, err := drainCtx(ctx2, neverCloses(99))
	cancel2()
	reportSlice("drainCtx.cancel", vs, err)

	// DrainCtx happy path — channel closes in time.
	ctx3, cancel3 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	vs, err = drainCtx(ctx3, feed(1, 2, 3))
	cancel3()
	reportSlice("drainCtx.ok", vs, err)

	// DrainCtxE.Wrap — shape the ctx.Err bubble.
	ctx4, cancel4 := context.WithTimeout(context.Background(), 5*time.Millisecond)
	vs, err = drainCtxWrap(ctx4, neverCloses(99))
	cancel4()
	reportSlice("drainCtx.wrap", vs, err)

	// DrainAll — per-channel slices in input order.
	result := drainAll(feed(1, 2), feed(10), feed(100, 200, 300))
	for i, r := range result {
		printSlice(fmt.Sprintf("drainAll.%d", i), r)
	}

	// DrainAllCtx — bubble on ctx timeout.
	ctx5, cancel5 := context.WithTimeout(context.Background(), 5*time.Millisecond)
	vs2, err := drainAllCtx(ctx5, neverCloses(1), neverCloses(2))
	cancel5()
	if err != nil {
		fmt.Printf("drainAllCtx.cancel: err=%s\n", err)
	} else {
		for i, r := range vs2 {
			printSlice(fmt.Sprintf("drainAllCtx.%d", i), r)
		}
	}

	// DrainAllCtx happy — all close before ctx fires.
	ctx6, cancel6 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	vs2, err = drainAllCtx(ctx6, feedDelayed(1*time.Millisecond, 5, 6), feed(7, 8, 9))
	cancel6()
	if err != nil {
		fmt.Printf("drainAllCtx.ok.err: %s\n", err)
	} else {
		for i, r := range vs2 {
			printSlice(fmt.Sprintf("drainAllCtx.ok.%d", i), r)
		}
	}
}
