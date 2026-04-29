// example/lazy mirrors docs/api/lazy.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/lazy
package main

import (
	"errors"
	"fmt"
	"sync"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ Host string }

var loadConfigCalls int

func loadConfig() (Config, error) {
	loadConfigCalls++
	return Config{Host: "primary"}, nil
}

var loadCalls int

func loadConfigFromDisk() Config {
	loadCalls++
	return Config{Host: "from-disk"}
}

var sumCalls int

func sumLargeSlice(xs []int) int {
	sumCalls++
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}

type DB struct{ host string; port int }

func connect(host string, port int) *DB { return &DB{host: host, port: port} }

// ---------- Top-of-doc — laziness ----------
//
//	l := q.Lazy(expensiveLookup(key))
//	if condition { v := l.Value() }
func conditionalDemo(condition bool) (Config, int) {
	loadCalls = 0
	cfg := q.Lazy(loadConfigFromDisk())
	if condition {
		return cfg.Value(), loadCalls
	}
	return Config{}, loadCalls
}

// "Force-eval and reuse" — memoisation:
func memoisationDemo() (int, int) {
	sumCalls = 0
	total := q.Lazy(sumLargeSlice([]int{1, 2, 3, 4, 5}))
	first := total.Value()
	second := total.Value()
	return first + second, sumCalls
}

// "Diagnostic check":
func diagnosticDemo() (bool, bool) {
	cfg := q.Lazy(loadConfigFromDisk())
	beforeForce := cfg.IsForced()
	_ = cfg.Value()
	afterForce := cfg.IsForced()
	return beforeForce, afterForce
}

// "Hand-written thunk via q.LazyFromThunk":
//
//	mk := q.LazyFromThunk(func() *DB { return connect(host, port) })
func handThunkDemo() string {
	mk := q.LazyFromThunk(func() *DB { return connect("db.example", 5432) })
	db := mk.Value()
	return fmt.Sprintf("%s:%d", db.host, db.port)
}

// ---------- "Pairing q.LazyE with q.Try" ----------
//
//	cfgL := q.LazyE(loadConfig())
//	cfg := q.Try(cfgL.Value())
func lazyEPairing() (Config, error) {
	cfgL := q.LazyE(loadConfig())
	cfg := q.Try(cfgL.Value())
	return cfg, nil
}

// "Cached errors don't retry":
type errPair struct {
	first, second error
	calls         int
}

func cachedErrors() errPair {
	loadFailingCalls := 0
	loadFailing := func() (Config, error) {
		loadFailingCalls++
		return Config{}, errors.New("boom")
	}
	cfgL := q.LazyEFromThunk(loadFailing)
	_, err1 := cfgL.Value()
	_, err2 := cfgL.Value()
	return errPair{first: err1, second: err2, calls: loadFailingCalls}
}

// "Concurrency-safe first-eval":
func concurrentFirstEval() (int, int) {
	calls := 0
	var mu sync.Mutex
	value := q.LazyFromThunk(func() int {
		mu.Lock()
		calls++
		mu.Unlock()
		return 42
	})

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = value.Value() }()
	}
	wg.Wait()
	return value.Value(), calls
}

func main() {
	cfg, calls := conditionalDemo(true)
	fmt.Printf("conditional(true): cfg=%s calls=%d\n", cfg.Host, calls)
	_, calls = conditionalDemo(false)
	fmt.Printf("conditional(false): calls=%d (no eval)\n", calls)

	total, sumCount := memoisationDemo()
	fmt.Printf("memoisation: total=%d sumCalls=%d (memoised)\n", total, sumCount)

	bf, af := diagnosticDemo()
	fmt.Printf("diagnostic: beforeForce=%v afterForce=%v\n", bf, af)

	fmt.Printf("handThunk: %s\n", handThunkDemo())

	if cfg, err := lazyEPairing(); err != nil {
		fmt.Printf("lazyEPairing: err=%s\n", err)
	} else {
		fmt.Printf("lazyEPairing: cfg=%s loadCalls=%d\n", cfg.Host, loadConfigCalls)
	}

	ep := cachedErrors()
	fmt.Printf("cachedErrors: e1=%s e2=%s same-msg=%v calls=%d (memoised)\n",
		ep.first, ep.second, ep.first.Error() == ep.second.Error(), ep.calls)

	v, callCount := concurrentFirstEval()
	fmt.Printf("concurrentFirstEval: v=%d calls=%d (single eval)\n", v, callCount)
}
