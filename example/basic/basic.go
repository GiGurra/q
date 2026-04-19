// Package main — the smallest end-to-end demo of q. Run with:
//
//	go run -toolexec=q ./example/basic
//
// Without `-toolexec=q` the link fails on `_q_atCompileTime not
// defined` — that's the contract, not a bug. See docs/design.md §3.
package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

var ErrNotFound = errors.New("basic: not found")

// parseAndDouble uses q.Try on a (T, error) call.
func parseAndDouble(s string) (int, error) {
	n := q.Try(strconv.Atoi(s))
	return n * 2, nil
}

// parseWithContext uses q.TryE(...).Wrapf to attach context to the
// bubbled error. errors.Is / errors.As traverse the wrap.
func parseWithContext(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).Wrapf("parsing %q", s)
	return n, nil
}

// lookupOrError uses q.NotNilE(...).Err to substitute a typed
// not-found error when the map lookup misses.
func lookupOrError(table map[string]*int, key string) (int, error) {
	p := q.NotNilE(table[key]).Err(ErrNotFound)
	return *p, nil
}

// lookupSentinel uses bare q.NotNil — bubbles q.ErrNil on miss.
func lookupSentinel(table map[string]*int, key string) (int, error) {
	p := q.NotNil(table[key])
	return *p, nil
}

func main() {
	run("parseAndDouble(\"21\")", func() (int, error) { return parseAndDouble("21") })
	run("parseAndDouble(\"abc\")", func() (int, error) { return parseAndDouble("abc") })
	run("parseWithContext(\"abc\")", func() (int, error) { return parseWithContext("abc") })

	n := 7
	table := map[string]*int{"x": &n}
	run(`lookupOrError(table, "x")`, func() (int, error) { return lookupOrError(table, "x") })
	run(`lookupOrError(table, "y")`, func() (int, error) { return lookupOrError(table, "y") })

	run(`lookupSentinel(table, "y")`, func() (int, error) { return lookupSentinel(table, "y") })
	_, sentinelErr := lookupSentinel(table, "y")
	fmt.Println("errors.Is(err, q.ErrNil):", errors.Is(sentinelErr, q.ErrNil))
}

func run(label string, fn func() (int, error)) {
	n, err := fn()
	if err != nil {
		fmt.Printf("%-36s => err: %v\n", label, err)
		return
	}
	fmt.Printf("%-36s => %d\n", label, n)
}
