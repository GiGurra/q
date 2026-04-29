// example/await_ctx mirrors docs/api/await_ctx.md one-to-one.
// Run with:
//
//	go run -toolexec=q ./example/await_ctx
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

type User struct{ Name string }

var byID = map[int]string{1: "Ada", 2: "Linus"}

func fetchUser(ctx context.Context, id int) (User, error) {
	select {
	case <-time.After(10 * time.Millisecond):
	case <-ctx.Done():
		return User{}, ctx.Err()
	}
	if name, ok := byID[id]; ok {
		return User{Name: name}, nil
	}
	return User{}, errors.New("not found")
}

func anonUser() User { return User{Name: "<anon>"} }

func fetchSize(ctx context.Context, url string) (int, error) {
	select {
	case <-time.After(5 * time.Millisecond):
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	return len(url), nil
}

// ---------- "What q.AwaitCtx does" ----------
//
//	v := q.AwaitCtx(ctx, f)
func whatAwaitCtxDoes(ctx context.Context, id int) (User, error) {
	f := q.Async(func() (User, error) { return fetchUser(ctx, id) })
	u := q.AwaitCtx(ctx, f)
	return u, nil
}

// ---------- "Chain methods on q.AwaitCtxE" ----------
//
//	u := q.AwaitCtxE(ctx, f).Wrapf("fetching user %d", id)
func awaitCtxEWrapf(ctx context.Context, id int) (User, error) {
	f := q.Async(func() (User, error) { return fetchUser(ctx, id) })
	u := q.AwaitCtxE(ctx, f).Wrapf("fetching user %d", id)
	return u, nil
}

//	u := q.AwaitCtxE(ctx, f).Catch(func(e error) (User, error) {
//	    if errors.Is(e, context.DeadlineExceeded) {
//	        return anonUser(), nil
//	    }
//	    return User{}, e
//	})
func awaitCtxECatch(ctx context.Context, id int) (User, error) {
	f := q.Async(func() (User, error) { return fetchUser(ctx, id) })
	u := q.AwaitCtxE(ctx, f).Catch(func(e error) (User, error) {
		if errors.Is(e, context.DeadlineExceeded) {
			return anonUser(), nil
		}
		return User{}, e
	})
	return u, nil
}

// ---------- "Fan-out with per-call deadlines" ----------
//
//	ctx = q.Timeout(ctx, 2*time.Second)
//	futures := make([]q.Future[int], len(urls))
//	for i, url := range urls { futures[i] = q.Async(func() (int, error) { return fetchSize(ctx, url) }) }
//	total := 0
//	for _, f := range futures { total += q.AwaitCtx(ctx, f) }
func fanOutDeadline(parent context.Context, urls []string) (int, error) {
	ctx := q.Timeout(parent, 2*time.Second)

	futures := make([]q.Future[int], len(urls))
	for i, url := range urls {
		futures[i] = q.Async(func() (int, error) { return fetchSize(ctx, url) })
	}
	total := 0
	for _, f := range futures {
		total += q.AwaitCtx(ctx, f)
	}
	return total, nil
}

// "Goroutine-leak caveat" — same ctx threaded inside and outside.
func ctxThreaded(parent context.Context, id int) (User, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Millisecond)
	defer cancel()
	f := q.Async(func() (User, error) { return fetchUser(ctx, id) })
	u := q.AwaitCtx(ctx, f)
	return u, nil
}

func main() {
	if u, err := whatAwaitCtxDoes(context.Background(), 1); err != nil {
		fmt.Printf("whatAwaitCtxDoes: err=%s\n", err)
	} else {
		fmt.Printf("whatAwaitCtxDoes: %s\n", u.Name)
	}

	if u, err := awaitCtxEWrapf(context.Background(), 1); err != nil {
		fmt.Printf("awaitCtxEWrapf(1): err=%s\n", err)
	} else {
		fmt.Printf("awaitCtxEWrapf(1): %s\n", u.Name)
	}
	if _, err := awaitCtxEWrapf(context.Background(), 99); err != nil {
		fmt.Printf("awaitCtxEWrapf(99): err=%s\n", err)
	}

	// Catch-recovers DeadlineExceeded; passes through anything else.
	if u, err := awaitCtxECatch(context.Background(), 99); err != nil {
		fmt.Printf("awaitCtxECatch(99): err=%s\n", err)
	} else {
		fmt.Printf("awaitCtxECatch(99): %s\n", u.Name)
	}

	if total, err := fanOutDeadline(context.Background(), []string{"a", "bb", "ccc", "dddd"}); err != nil {
		fmt.Printf("fanOutDeadline: err=%s\n", err)
	} else {
		fmt.Printf("fanOutDeadline: total=%d\n", total)
	}

	// Ctx threaded inside; deadline fires before fetchUser's 10ms work →
	// AwaitCtx returns ctx.Err. Recovers via Catch:
	if u, err := ctxThreaded(context.Background(), 1); err != nil {
		fmt.Printf("ctxThreaded(1): err=%s\n", err)
	} else {
		fmt.Printf("ctxThreaded(1): %s\n", u.Name)
	}
}
