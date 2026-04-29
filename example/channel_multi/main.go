// example/channel_multi mirrors docs/api/channel_multi.md one-to-one.
// Run with:
//
//	go run -toolexec=q ./example/channel_multi
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

// chanOf returns a recv-only chan with `vals` and an immediate close,
// suitable for q.Drain semantics.
func chanOf(vals ...int) <-chan int {
	ch := make(chan int, len(vals))
	for _, v := range vals {
		ch <- v
	}
	close(ch)
	return ch
}

func sendOne(v int, after time.Duration) <-chan int {
	ch := make(chan int, 1)
	go func() {
		time.Sleep(after)
		ch <- v
	}()
	return ch
}

func closedChan() <-chan int {
	ch := make(chan int)
	close(ch)
	return ch
}

// ---------- "What q.RecvAny does" ----------
//
//	v := q.RecvAny(ch1, ch2, ch3)
func recvAnyExample() (int, error) {
	v := q.RecvAny(sendOne(1, 30*time.Millisecond), sendOne(2, 5*time.Millisecond), sendOne(3, 50*time.Millisecond))
	return v, nil
}

//	v := q.RecvAnyE(ch1, ch2).Catch(func(e error) (int, error) {
//	    if errors.Is(e, q.ErrChanClosed) {
//	        return -1, nil
//	    }
//	    return 0, e
//	})
func recvAnyECatchClosed() (int, error) {
	v := q.RecvAnyE(closedChan(), sendOne(7, 100*time.Millisecond)).Catch(func(e error) (int, error) {
		if errors.Is(e, q.ErrChanClosed) {
			return -1, nil
		}
		return 0, e
	})
	return v, nil
}

// ---------- "What q.Drain does" ----------
//
//	vs := q.Drain(ch)
func drainExample() []int {
	return q.Drain(chanOf(10, 20, 30))
}

//	vs := q.DrainCtx(ctx, ch)
func drainCtxExample(parent context.Context) ([]int, error) {
	ctx := q.Timeout(parent, time.Second)
	return q.DrainCtx(ctx, chanOf(1, 2, 3, 4)), nil
}

// ---------- "What q.DrainAll does" ----------
//
//	results := q.DrainAll(chA, chB, chC)
func drainAllExample() [][]int {
	return q.DrainAll(chanOf(1, 2), chanOf(3, 4, 5), chanOf(6))
}

func drainAllCtxExample(parent context.Context) ([][]int, error) {
	ctx := q.Timeout(parent, time.Second)
	return q.DrainAllCtx(ctx, chanOf(1, 2), chanOf(3, 4)), nil
}

func main() {
	if v, err := recvAnyExample(); err != nil {
		fmt.Printf("recvAnyExample: err=%s\n", err)
	} else {
		fmt.Printf("recvAnyExample: %d\n", v)
	}

	if v, err := recvAnyECatchClosed(); err != nil {
		fmt.Printf("recvAnyECatchClosed: err=%s\n", err)
	} else {
		fmt.Printf("recvAnyECatchClosed: %d\n", v)
	}

	fmt.Printf("drainExample: %v\n", drainExample())

	if vs, err := drainCtxExample(context.Background()); err != nil {
		fmt.Printf("drainCtxExample: err=%s\n", err)
	} else {
		fmt.Printf("drainCtxExample: %v\n", vs)
	}

	fmt.Printf("drainAllExample: %v\n", drainAllExample())

	if vs, err := drainAllCtxExample(context.Background()); err != nil {
		fmt.Printf("drainAllCtxExample: err=%s\n", err)
	} else {
		fmt.Printf("drainAllCtxExample: %v\n", vs)
	}
}
