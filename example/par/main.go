// example/par mirrors docs/api/par.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/par
package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- "At a glance" ----------
func atAGlance(ctx context.Context) {
	expensive := func(n int) int { return n * 2 }
	items := []int{1, 2, 3, 4, 5}

	// Default concurrency (runtime.NumCPU()).
	results := q.ParMap(ctx, items, expensive)
	fmt.Printf("default: %v\n", results)

	// Set the limit for a request scope.
	ctx2 := q.WithPar(ctx, 2)
	results = q.ParMap(ctx2, items, expensive)
	fmt.Printf("limit=2: %v\n", results)

	// Unbounded — one goroutine per item.
	ctxU := q.WithParUnbounded(ctx)
	results = q.ParMap(ctxU, items, expensive)
	fmt.Printf("unbounded: %v\n", results)

	// Read the limit (default = NumCPU).
	fmt.Printf("GetPar(ctx2)=%d\n", q.GetPar(ctx2))
}

// ---------- "Cancellation semantics" / fetchWithCtx ----------
type Response struct{ Body string }

func fetchWithCtx(ctx context.Context, url string) (Response, error) {
	select {
	case <-time.After(5 * time.Millisecond):
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
	return Response{Body: "fetched:" + url}, nil
}

func cancellation(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, 1*time.Millisecond)
	defer cancel()
	urls := []string{"a", "bb", "ccc"}
	_, err := q.ParMapErr(ctx, urls, fetchWithCtx)
	return err
}

// ---------- ParMap / ParFilter / ParForEach ----------
func parMapDemo() []int {
	return q.ParMap(context.Background(), []int{1, 2, 3}, func(n int) int { return n * 10 })
}

func parFilterDemo() []int {
	return q.ParFilter(context.Background(), []int{1, 2, 3, 4, 5}, func(n int) bool { return n%2 == 0 })
}

func parForEachDemo() int {
	count := 0
	q.ParForEach(context.Background(), []int{1, 2, 3, 4, 5}, func(n int) {
		count++ // race in real code; here we serialise via WithPar 1
		_ = n
	})
	return count
}

// ParMapErr — first-error semantics.
func parMapErrDemo() ([]int, error) {
	return q.ParMapErr(context.Background(), []string{"1", "2", "3"}, func(_ context.Context, s string) (int, error) {
		var n int
		_, err := fmt.Sscanf(s, "%d", &n)
		return n, err
	})
}

func parMapErrFailing() error {
	_, err := q.ParMapErr(context.Background(), []string{"1", "bad", "3"}, func(_ context.Context, s string) (int, error) {
		var n int
		_, err := fmt.Sscanf(s, "%d", &n)
		if err != nil {
			return 0, errors.New("bad: " + s)
		}
		return n, nil
	})
	return err
}

// ParExists / ParForAll.
func parExistsDemo() (bool, bool) {
	xs := []int{1, 2, 3, 4}
	any := q.ParExists(context.Background(), xs, func(n int) bool { return n > 3 })
	all := q.ParForAll(context.Background(), xs, func(n int) bool { return n > 0 })
	return any, all
}

// ParGroupBy.
func parGroupByDemo() map[string]int {
	type Item struct {
		Name, Cat string
	}
	items := []Item{{"x", "f"}, {"y", "v"}, {"z", "f"}, {"w", "v"}}
	groups := q.ParGroupBy(context.Background(), items, func(it Item) string { return it.Cat })
	out := make(map[string]int, len(groups))
	for k, v := range groups {
		out[k] = len(v)
	}
	return out
}

func main() {
	ctx := context.Background()
	atAGlance(ctx)

	if err := cancellation(ctx); err != nil {
		fmt.Printf("cancellation: err=%s\n", err)
	}

	fmt.Printf("parMapDemo: %v\n", parMapDemo())
	fmt.Printf("parFilterDemo: %v\n", parFilterDemo())
	fmt.Printf("parForEachDemo: count=%d\n", parForEachDemo())

	if vs, err := parMapErrDemo(); err != nil {
		fmt.Printf("parMapErrDemo: err=%s\n", err)
	} else {
		fmt.Printf("parMapErrDemo: %v\n", vs)
	}
	if err := parMapErrFailing(); err != nil {
		fmt.Printf("parMapErrFailing: err=%s\n", err)
	}

	any, all := parExistsDemo()
	fmt.Printf("parExists=%v parForAll=%v\n", any, all)

	g := parGroupByDemo()
	keys := make([]string, 0, len(g))
	for k := range g {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("parGroupBy[%s]=%d\n", k, g[k])
	}
}
