// Fixture: q.Lazy[T](v) — deferred evaluation of arbitrary expressions.
//
// Demonstrates:
//
//   1. Eager-looking call form, lazy semantics. q.Lazy(calc()) does
//      NOT call calc() at construction; the preprocessor wraps the
//      argument in a thunk closure.
//   2. Memoisation. The first .Value() call evaluates the thunk; later
//      calls return the cached result.
//   3. IsForced diagnostic. False until Value() runs at least once.
//   4. Concurrent first-call safety via sync.Once. Many goroutines
//      racing on .Value() resolve to one execution.
//   5. Closure capture. The wrapped expression sees locals normally.
package main

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/GiGurra/q/pkg/q"
)

var calls int

func calculateValue() int {
	calls++
	return 42
}

func main() {
	// (1) Construction does not run the thunk.
	l := q.Lazy(calculateValue())
	fmt.Println("calls after Lazy:", calls)        // expect 0
	fmt.Println("forced after Lazy:", l.IsForced()) // expect false

	// (2) First .Value() forces the thunk.
	v := l.Value()
	fmt.Println("first Value:", v)                       // expect 42
	fmt.Println("calls after first Value:", calls)       // expect 1
	fmt.Println("forced after first Value:", l.IsForced()) // expect true

	// (3) Memoised — second .Value() does not re-run the thunk.
	v2 := l.Value()
	fmt.Println("second Value:", v2)                  // expect 42
	fmt.Println("calls after second Value:", calls)   // expect still 1

	// (4) Closure-captured locals.
	x := 7
	l2 := q.Lazy(x * 6)
	fmt.Println("closure-captured Value:", l2.Value()) // expect 42

	// (5) Explicit type argument.
	var iface fmt.Stringer
	l3 := q.Lazy[fmt.Stringer](iface)
	fmt.Println("typed-iface IsForced:", l3.IsForced()) // expect false (nothing forced)

	// (6) Concurrent first-call — sync.Once enforces single execution.
	var raceCalls atomic.Int64
	l4 := q.Lazy(func() int {
		raceCalls.Add(1)
		return 100
	}())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = l4.Value()
		}()
	}
	wg.Wait()
	fmt.Println("race Value:", l4.Value())
	fmt.Println("race thunk runs:", raceCalls.Load()) // expect 1

	// (7) q.LazyFromThunk — the underlying constructor users can also
	//     reach for when they have a hand-written thunk.
	l5 := q.LazyFromThunk(func() string { return "hand-thunk" })
	fmt.Println("LazyFromThunk Value:", l5.Value())

	// (8) q.LazyE — (T, error)-shaped lazy initialiser. .Value() pairs
	//     with q.Try at the consumer.
	if err := lazyERun(); err != nil {
		fmt.Println("lazyERun err:", err)
	}
}

var loadCalls int

func loadConfigE() (string, error) {
	loadCalls++
	return "loaded-cfg", nil
}

func loadConfigErr() (string, error) {
	loadCalls++
	return "", fmt.Errorf("disk on fire")
}

func lazyERun() error {
	// Happy path — the thunk runs on first .Value(), result cached.
	cfgL := q.LazyE(loadConfigE())
	fmt.Println("LazyE forced before Value:", cfgL.IsForced()) // false
	fmt.Println("loadCalls before Value:", loadCalls)          // 0
	cfg := q.Try(cfgL.Value())
	fmt.Println("LazyE Value:", cfg)
	fmt.Println("loadCalls after Value:", loadCalls)           // 1
	_ = q.Try(cfgL.Value())
	fmt.Println("loadCalls after second Value:", loadCalls)    // 1 (cached)

	// Error path — the error from the first .Value() is cached too.
	errL := q.LazyE(loadConfigErr())
	_, err := errL.Value()
	fmt.Println("LazyE first err:", err)
	_, err2 := errL.Value()
	fmt.Println("LazyE second err (cached):", err2)
	return nil
}
