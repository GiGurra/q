// Fixture: q.ParMap / q.ParMapErr / q.ParFlatMap / q.ParFlatMapErr /
// q.ParFilter / q.ParFilterErr / q.ParForEach / q.ParForEachErr plus the
// ctx helpers (q.WithPar / q.WithParUnbounded / q.GetPar). Pure
// runtime helpers — no preprocessor rewriting expected.
//
// Order-independent assertions are sorted before printing for
// deterministic output. Concurrency cap (q.WithPar(ctx, n)) is
// verified by tracking max-active workers via atomics.
package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	ctx := context.Background()

	// q.GetPar default = NumCPU, set value, unbounded.
	fmt.Println("default GetPar == NumCPU:", q.GetPar(ctx) == runtime.NumCPU())
	fmt.Println("WithPar(8):", q.GetPar(q.WithPar(ctx, 8)))
	fmt.Println("WithParUnbounded sentinel:", q.GetPar(q.WithParUnbounded(ctx)) == -1)
	// Non-positive limit falls back to default (NumCPU).
	fmt.Println("WithPar(0) falls back:", q.GetPar(q.WithPar(ctx, 0)) == runtime.NumCPU())
	fmt.Println("WithPar(-5) falls back:", q.GetPar(q.WithPar(ctx, -5)) == runtime.NumCPU())

	// ParMap: doubles 1..5, results in input order.
	doubled := q.ParMap(ctx, []int{1, 2, 3, 4, 5}, func(n int) int { return n * 2 })
	fmt.Println("ParMap:", doubled)

	// ParMap empty input.
	fmt.Println("ParMap empty:", q.ParMap(ctx, []int{}, func(n int) int { return n }))

	// Concurrency cap: q.WithPar(ctx, 3) means max-active <= 3.
	{
		ctx := q.WithPar(ctx, 3)
		var active, maxActive int64
		_ = q.ParMap(ctx, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, func(n int) int {
			cur := atomic.AddInt64(&active, 1)
			for {
				old := atomic.LoadInt64(&maxActive)
				if cur <= old || atomic.CompareAndSwapInt64(&maxActive, old, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt64(&active, -1)
			return n
		})
		fmt.Println("ParMap WithPar(3) cap honoured:", maxActive <= 3)
	}

	// ParMapErr happy path + q.Try composition.
	parsed, _ := tryParMap(ctx, []string{"1", "2", "3"})
	fmt.Println("ParMapErr ok:", parsed)

	// ParMapErr first-error-wins.
	_, err := q.ParMapErr(ctx, []string{"1", "x", "3"}, func(_ context.Context, s string) (int, error) {
		return strconv.Atoi(s)
	})
	fmt.Println("ParMapErr fail:", err != nil)

	// q.TryE composition: wrap the bubbled error.
	if _, e := tryEParMap(ctx, []string{"1", "y"}); e != nil {
		fmt.Println("q.TryE(ParMapErr).Wrap fail:", e.Error())
	}

	// ParMapErr ctx-cancel: cancelled ctx → returns ctx.Err().
	{
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := q.ParMapErr(ctx, []int{1, 2, 3}, func(_ context.Context, n int) (int, error) {
			return n, nil
		})
		fmt.Println("ParMapErr cancel:", errors.Is(err, context.Canceled))
	}

	// ParFlatMap concatenates per-element slices in input order.
	flat := q.ParFlatMap(ctx, []int{1, 2, 3}, func(n int) []int { return []int{n, n * 10} })
	fmt.Println("ParFlatMap:", flat)

	// ParFlatMapErr with one failing element.
	_, err = q.ParFlatMapErr(ctx, []int{1, 2, 3}, func(_ context.Context, n int) ([]int, error) {
		if n == 2 {
			return nil, errors.New("flat-fail")
		}
		return []int{n}, nil
	})
	fmt.Println("ParFlatMapErr fail:", err)

	// ParFilter keeps input order.
	even := q.ParFilter(ctx, []int{1, 2, 3, 4, 5, 6}, func(n int) bool { return n%2 == 0 })
	fmt.Println("ParFilter:", even)

	// ParFilterErr happy path.
	odd, _ := q.ParFilterErr(ctx, []string{"1", "2", "3"}, func(_ context.Context, s string) (bool, error) {
		n, err := strconv.Atoi(s)
		return n%2 == 1, err
	})
	fmt.Println("ParFilterErr ok:", odd)

	// ParFilterErr fail bubble.
	_, err = q.ParFilterErr(ctx, []string{"1", "x"}, func(_ context.Context, s string) (bool, error) {
		_, err := strconv.Atoi(s)
		return true, err
	})
	fmt.Println("ParFilterErr fail:", err != nil)

	// ParForEach: side effects, no results. Use atomic counter for the assert.
	{
		var sum int64
		q.ParForEach(ctx, []int{1, 2, 3, 4, 5}, func(n int) {
			atomic.AddInt64(&sum, int64(n))
		})
		fmt.Println("ParForEach sum:", sum)
	}

	// ParForEachErr first-error wins; q.Check composition path.
	// Limit=1 + slice ending at the erroring element ⇒ deterministic
	// "seen" set (no later element gets dispatched).
	{
		var seen []int
		var mu sync.Mutex // keeps "seen" race-free
		err := q.ParForEachErr(q.WithPar(ctx, 1), []int{1, 2, 3}, func(_ context.Context, n int) error {
			if n == 3 {
				return errors.New("boom")
			}
			mu.Lock()
			seen = append(seen, n)
			mu.Unlock()
			return nil
		})
		sort.Ints(seen)
		fmt.Println("ParForEachErr err:", err)
		fmt.Println("ParForEachErr seen-before-err:", seen)
	}

	// ParForEachErr cancellation.
	{
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		err := q.ParForEachErr(ctx, []int{1, 2, 3}, func(_ context.Context, n int) error {
			return nil
		})
		fmt.Println("ParForEachErr cancel:", errors.Is(err, context.Canceled))
	}

	// q.ForEach (sequential) and q.ForEachErr.
	{
		var sum int
		q.ForEach([]int{1, 2, 3, 4, 5}, func(n int) { sum += n })
		fmt.Println("ForEach sum:", sum)
	}
	{
		err := q.ForEachErr([]string{"1", "2", "x", "4"}, func(s string) error {
			_, err := strconv.Atoi(s)
			return err
		})
		fmt.Println("ForEachErr fail:", err != nil)
	}

	// ParExists — finds an element in parallel.
	{
		found := q.ParExists(ctx, []int{1, 2, 3, 4, 5}, func(n int) bool { return n == 3 })
		fmt.Println("ParExists yes:", found)

		nope := q.ParExists(ctx, []int{1, 2, 3}, func(n int) bool { return n == 99 })
		fmt.Println("ParExists no:", nope)

		empty := q.ParExists(ctx, []int{}, func(n int) bool { return true })
		fmt.Println("ParExists empty:", empty)
	}

	// ParExistsErr — fallible predicate; first true wins, first err bubbles.
	{
		yes, _ := q.ParExistsErr(ctx, []string{"1", "2", "3"}, func(_ context.Context, s string) (bool, error) {
			n, err := strconv.Atoi(s)
			if err != nil {
				return false, err
			}
			return n == 2, nil
		})
		fmt.Println("ParExistsErr yes:", yes)

		_, err := q.ParExistsErr(ctx, []string{"x"}, func(_ context.Context, s string) (bool, error) {
			_, err := strconv.Atoi(s)
			return false, err
		})
		fmt.Println("ParExistsErr fail:", err != nil)
	}

	// ParForAll — every element matches.
	{
		all := q.ParForAll(ctx, []int{2, 4, 6}, func(n int) bool { return n%2 == 0 })
		fmt.Println("ParForAll yes:", all)

		not := q.ParForAll(ctx, []int{2, 3, 6}, func(n int) bool { return n%2 == 0 })
		fmt.Println("ParForAll no:", not)

		empty := q.ParForAll(ctx, []int{}, func(n int) bool { return false })
		fmt.Println("ParForAll empty:", empty)
	}

	// ParGroupBy — keys computed in parallel, reassembly sequential.
	{
		type item struct {
			cat string
			v   int
		}
		items := []item{{"a", 1}, {"b", 2}, {"a", 3}, {"b", 4}, {"a", 5}}
		grouped := q.ParGroupBy(ctx, items, func(it item) string { return it.cat })
		keys := make([]string, 0, len(grouped))
		for k := range grouped {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("ParGroupBy[%s]=%v\n", k, grouped[k])
		}
	}

	// ParGroupByErr happy path.
	{
		type entry struct{ s string }
		entries := []entry{{"1"}, {"2"}, {"3"}, {"4"}}
		grouped, err := q.ParGroupByErr(ctx, entries, func(_ context.Context, e entry) (string, error) {
			n, err := strconv.Atoi(e.s)
			if err != nil {
				return "", err
			}
			if n%2 == 0 {
				return "even", nil
			}
			return "odd", nil
		})
		fmt.Println("ParGroupByErr ok err:", err)
		keys := make([]string, 0, len(grouped))
		for k := range grouped {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("ParGroupByErr[%s]=", k)
			for i, e := range grouped[k] {
				if i > 0 {
					fmt.Print(",")
				}
				fmt.Print(e.s)
			}
			fmt.Println()
		}
	}

	// ParForAllErr — fallible predicate.
	{
		all, _ := q.ParForAllErr(ctx, []int{2, 4}, func(_ context.Context, n int) (bool, error) {
			return n%2 == 0, nil
		})
		fmt.Println("ParForAllErr yes:", all)

		not, _ := q.ParForAllErr(ctx, []int{2, 3}, func(_ context.Context, n int) (bool, error) {
			return n%2 == 0, nil
		})
		fmt.Println("ParForAllErr no:", not)

		_, err := q.ParForAllErr(ctx, []string{"x"}, func(_ context.Context, s string) (bool, error) {
			_, err := strconv.Atoi(s)
			return false, err
		})
		fmt.Println("ParForAllErr fail:", err != nil)
	}

	// Bare ParMap honours ctx cancel — dispatch stops, partial slice
	// returned. ctx.Err() is set so caller can detect.
	{
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		out := q.ParMap(ctx, []int{1, 2, 3, 4, 5}, func(n int) int { return n * 2 })
		fmt.Println("ParMap cancelled (result len matches input):", len(out) == 5)
		fmt.Println("ParMap cancelled (ctx.Err detectable):", errors.Is(ctx.Err(), context.Canceled))
	}

	// Timeout: same shape as cancel, but ctx.Err() is DeadlineExceeded.
	// Use a long-running fn + sub-millisecond timeout so the deadline
	// fires before any work completes.
	{
		ctx, cancel := context.WithTimeout(ctx, time.Microsecond)
		defer cancel()
		// Wait long enough that the timeout has fired before dispatch starts.
		time.Sleep(10 * time.Millisecond)
		_, err := q.ParMapErr(ctx, []int{1, 2, 3}, func(_ context.Context, n int) (int, error) {
			return n * 2, nil
		})
		fmt.Println("ParMapErr timeout (DeadlineExceeded):", errors.Is(err, context.DeadlineExceeded))
	}

	// q.WithParUnbounded — workers spawn one-per-item.
	{
		ctx := q.WithParUnbounded(ctx)
		var maxActive int64
		var active int64
		_ = q.ParMap(ctx, []int{1, 2, 3, 4, 5, 6, 7, 8}, func(n int) int {
			cur := atomic.AddInt64(&active, 1)
			for {
				old := atomic.LoadInt64(&maxActive)
				if cur <= old || atomic.CompareAndSwapInt64(&maxActive, old, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt64(&active, -1)
			return n
		})
		// Unbounded means we hit ~len(slice) parallelism — at least 4
		// (substantially more than NumCPU's typical default would allow).
		fmt.Println("ParMap unbounded all-spawned:", maxActive >= 4)
	}
}

func tryParMap(ctx context.Context, ss []string) ([]int, error) {
	return q.Try(q.ParMapErr(ctx, ss, func(_ context.Context, s string) (int, error) {
		return strconv.Atoi(s)
	})), nil
}

func tryEParMap(ctx context.Context, ss []string) ([]int, error) {
	return q.TryE(q.ParMapErr(ctx, ss, func(_ context.Context, s string) (int, error) {
		return strconv.Atoi(s)
	})).Wrap("parsing"), nil
}
