// Fixture: q.AwaitAll / q.AwaitAny and their Ctx + E variants.
// Covers:
//  1. AwaitAll happy path — gather all results in order.
//  2. AwaitAll first-error bubble.
//  3. AwaitAllCtx ctx-cancel bubble.
//  4. AwaitAny first-success wins.
//  5. AwaitAny all-fail → errors.Join aggregate.
//  6. AwaitAnyCtx ctx fires before any success.
//  7. Chain E-variants with Wrap.
//  8. Variadic spread (`fs...`) survives rewrite.
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

// slowOK completes with v after delay.
func slowOK(v int, delay time.Duration) func() (int, error) {
	return func() (int, error) {
		time.Sleep(delay)
		return v, nil
	}
}

// fastFail completes immediately with err.
func fastFail(msg string) func() (int, error) {
	return func() (int, error) { return 0, errors.New(msg) }
}

// --- AwaitAll ---

func allOK() ([]int, error) {
	a := q.Async(slowOK(1, 5*time.Millisecond))
	b := q.Async(slowOK(2, 10*time.Millisecond))
	c := q.Async(slowOK(3, 1*time.Millisecond))
	return q.AwaitAll(a, b, c), nil
}

func allBubble() ([]int, error) {
	a := q.Async(slowOK(1, 10*time.Millisecond))
	b := q.Async(fastFail("boom"))
	c := q.Async(slowOK(3, 1*time.Millisecond))
	return q.AwaitAll(a, b, c), nil
}

func allWrap() ([]int, error) {
	a := q.Async(slowOK(1, 5*time.Millisecond))
	b := q.Async(fastFail("broken"))
	return q.AwaitAllE(a, b).Wrap("gather"), nil
}

// Variadic spread — fs... must survive the rewrite.
func allSpread(fs []q.Future[int]) ([]int, error) {
	return q.AwaitAll(fs...), nil
}

// Ctx variant — ctx timeout before futures complete.
func allCtxCancel(ctx context.Context) ([]int, error) {
	a := q.Async(slowOK(1, 80*time.Millisecond))
	b := q.Async(slowOK(2, 80*time.Millisecond))
	return q.AwaitAllCtx(ctx, a, b), nil
}

// --- AwaitAny ---

func anyFirstWin() (int, error) {
	slow := q.Async(slowOK(99, 50*time.Millisecond))
	fast := q.Async(slowOK(7, 1*time.Millisecond))
	return q.AwaitAny(slow, fast), nil
}

func anyAllFail() (int, error) {
	a := q.Async(fastFail("one"))
	b := q.Async(fastFail("two"))
	c := q.Async(fastFail("three"))
	return q.AwaitAny(a, b, c), nil
}

func anyCtxCancel(ctx context.Context) (int, error) {
	a := q.Async(slowOK(1, 80*time.Millisecond))
	b := q.Async(slowOK(2, 80*time.Millisecond))
	return q.AwaitAnyCtx(ctx, a, b), nil
}

// E-variant with Wrap on AwaitAny — all-fail aggregate wraps.
func anyAllFailWrap() (int, error) {
	a := q.Async(fastFail("x"))
	b := q.Async(fastFail("y"))
	return q.AwaitAnyE(a, b).Wrap("racing"), nil
}

func reportSlice(name string, vs []int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
		return
	}
	var parts []string
	for _, v := range vs {
		parts = append(parts, fmt.Sprintf("%d", v))
	}
	fmt.Printf("%s: ok=[%s]\n", name, strings.Join(parts, ","))
}

func reportInt(name string, v int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
		return
	}
	fmt.Printf("%s: ok=%d\n", name, v)
}

func main() {
	// 1. AwaitAll happy.
	vs, err := allOK()
	reportSlice("all.ok", vs, err)

	// 2. AwaitAll first-error.
	vs, err = allBubble()
	reportSlice("all.bubble", vs, err)

	// 3. AwaitAllE.Wrap.
	vs, err = allWrap()
	reportSlice("all.wrap", vs, err)

	// 4. Variadic spread.
	fs := []q.Future[int]{
		q.Async(slowOK(10, 1*time.Millisecond)),
		q.Async(slowOK(20, 2*time.Millisecond)),
	}
	vs, err = allSpread(fs)
	reportSlice("all.spread", vs, err)

	// 5. AwaitAllCtx — cancel fires first.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	vs, err = allCtxCancel(ctx)
	cancel()
	reportSlice("all.ctx", vs, err)

	// 6. AwaitAny — fast wins.
	v, err := anyFirstWin()
	reportInt("any.first", v, err)

	// 7. AwaitAny — all fail → errors.Join aggregate.
	v, err = anyAllFail()
	if err != nil {
		// errors.Join uses newlines between members; print count instead
		// for stable output.
		members := []string{"one", "two", "three"}
		got := err.Error()
		matched := true
		for _, m := range members {
			if !strings.Contains(got, m) {
				matched = false
			}
		}
		fmt.Printf("any.all.fail.joined: matched=%v\n", matched)
	} else {
		fmt.Printf("any.all.fail.joined: unexpected ok=%d\n", v)
	}

	// 8. AwaitAnyCtx — ctx fires before any.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Millisecond)
	v, err = anyCtxCancel(ctx2)
	cancel2()
	reportInt("any.ctx", v, err)

	// 9. AwaitAnyE.Wrap on all-fail aggregate.
	v, err = anyAllFailWrap()
	if err != nil {
		fmt.Printf("any.wrap.allfail: prefix=%v\n", strings.HasPrefix(err.Error(), "racing: "))
	} else {
		fmt.Printf("any.wrap.allfail: unexpected ok=%d\n", v)
	}
}
