// example/await_multi mirrors docs/api/await_multi.md one-to-one.
// Run with:
//
//	go run -toolexec=q ./example/await_multi
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

func okFuture(n int) q.Future[int]   { return q.Async(func() (int, error) { return n, nil }) }
func failFuture(s string) q.Future[int] {
	return q.Async(func() (int, error) { return 0, errors.New(s) })
}

func slowFuture(ctx context.Context, n int, d time.Duration) q.Future[int] {
	return q.Async(func() (int, error) {
		select {
		case <-time.After(d):
			return n, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	})
}

// ---------- "What q.AwaitAll does" ----------
//
//	vs := q.AwaitAll(fA, fB, fC)
func awaitAllOk() ([]int, error) {
	vs := q.AwaitAll(okFuture(1), okFuture(2), okFuture(3))
	return vs, nil
}

func awaitAllErr() ([]int, error) {
	vs := q.AwaitAll(okFuture(1), failFuture("boom"), okFuture(3))
	return vs, nil
}

// ---------- "What q.AwaitAny does" ----------
//
//	winner := q.AwaitAny(fA, fB, fC)
func awaitAnyFirstSuccess() (int, error) {
	winner := q.AwaitAny(failFuture("a"), okFuture(7), failFuture("c"))
	return winner, nil
}

func awaitAnyAllFail() (int, error) {
	winner := q.AwaitAny(failFuture("a"), failFuture("b"), failFuture("c"))
	return winner, nil
}

// ---------- "Variadic spread survives the rewrite" ----------
//
//	fs := []q.Future[int]{q.Async(f1), q.Async(f2), q.Async(f3)}
//	vs := q.AwaitAll(fs...)
func variadicSpread() ([]int, error) {
	fs := []q.Future[int]{okFuture(10), okFuture(20), okFuture(30)}
	vs := q.AwaitAll(fs...)
	return vs, nil
}

// ---------- "Fan-out / fan-in in a single line" ----------
//
//	func fetchSizes(ctx context.Context, urls []string) ([]int, error) {
//	    ctx = q.Timeout(ctx, 2*time.Second)
//	    futures := make([]q.Future[int], len(urls))
//	    for i, url := range urls {
//	        futures[i] = q.Async(func() (int, error) { return fetchSize(ctx, url) })
//	    }
//	    return q.AwaitAllCtx(ctx, futures...), nil
//	}
func fetchSize(ctx context.Context, url string) (int, error) {
	select {
	case <-time.After(5 * time.Millisecond):
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	return len(url), nil
}

func fetchSizes(parent context.Context, urls []string) ([]int, error) {
	ctx := q.Timeout(parent, 2*time.Second)
	futures := make([]q.Future[int], len(urls))
	for i, url := range urls {
		futures[i] = q.Async(func() (int, error) { return fetchSize(ctx, url) })
	}
	return q.AwaitAllCtx(ctx, futures...), nil
}

// ---------- "Chain methods" ----------
//
//	vs := q.AwaitAllE(fA, fB, fC).Wrap("gathering")
//	winner := q.AwaitAnyE(fA, fB, fC).Catch(func(e error) (int, error) {
//	    return 0, nil
//	})
func awaitAllEWrap() ([]int, error) {
	vs := q.AwaitAllE(okFuture(1), failFuture("nope"), okFuture(3)).Wrap("gathering")
	return vs, nil
}

func awaitAnyECatchDefault() (int, error) {
	winner := q.AwaitAnyE(failFuture("a"), failFuture("b")).Catch(func(e error) (int, error) {
		return 0, nil
	})
	return winner, nil
}

// "Goroutine-leak caveat" — Ctx variant under a tight deadline.
func ctxCancelsAwaitAll(parent context.Context) ([]int, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Millisecond)
	defer cancel()
	return q.AwaitAllCtx(ctx, slowFuture(ctx, 1, 50*time.Millisecond), slowFuture(ctx, 2, 50*time.Millisecond)), nil
}

func main() {
	if vs, err := awaitAllOk(); err != nil {
		fmt.Printf("awaitAllOk: err=%s\n", err)
	} else {
		fmt.Printf("awaitAllOk: %v\n", vs)
	}
	if _, err := awaitAllErr(); err != nil {
		fmt.Printf("awaitAllErr: err=%s\n", err)
	}

	if w, err := awaitAnyFirstSuccess(); err != nil {
		fmt.Printf("awaitAnyFirstSuccess: err=%s\n", err)
	} else {
		fmt.Printf("awaitAnyFirstSuccess: %d\n", w)
	}
	if _, err := awaitAnyAllFail(); err != nil {
		fmt.Printf("awaitAnyAllFail: err=%s\n", err)
	}

	if vs, err := variadicSpread(); err != nil {
		fmt.Printf("variadicSpread: err=%s\n", err)
	} else {
		fmt.Printf("variadicSpread: %v\n", vs)
	}

	if vs, err := fetchSizes(context.Background(), []string{"a", "bb", "ccc"}); err != nil {
		fmt.Printf("fetchSizes: err=%s\n", err)
	} else {
		fmt.Printf("fetchSizes: %v\n", vs)
	}

	if _, err := awaitAllEWrap(); err != nil {
		fmt.Printf("awaitAllEWrap: err=%s\n", err)
	}

	if w, err := awaitAnyECatchDefault(); err != nil {
		fmt.Printf("awaitAnyECatchDefault: err=%s\n", err)
	} else {
		fmt.Printf("awaitAnyECatchDefault: %d\n", w)
	}

	if _, err := ctxCancelsAwaitAll(context.Background()); err != nil {
		fmt.Printf("ctxCancelsAwaitAll: err=%s\n", err)
	}
}
