// Fixture: q.* used inside closures / anonymous functions. Each
// FuncLit has its own result list, so the rewriter must derive zero-
// values and the bubble shape from the nearest-enclosing function —
// FuncLit or FuncDecl — not always the outer FuncDecl.
//
// Especially important for the future q.TryManage design (#17), which
// inherently runs inside a deferred closure.
package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

// Closure stored in a variable. The FuncLit's result shape is
// (int, error); the enclosing FuncDecl returns nothing.
func closureInVar(s string) (int, error) {
	parse := func(in string) (int, error) {
		n := q.Try(strconv.Atoi(in))
		return n * 2, nil
	}
	return parse(s)
}

// Immediately-invoked closure returning (string, error). The
// enclosing FuncDecl returns only error.
func closureImmediate(p *int) error {
	_, err := func() (string, error) {
		v := q.NotNil(p)
		return fmt.Sprintf("got=%d", *v), nil
	}()
	return err
}

// Closure that returns just an error. Different arity from the outer.
func closureErrorOnly(p *int) error {
	check := func() error {
		q.NotNil(p)
		return nil
	}
	return check()
}

// Closure as a defer target. The deferred fn returns (nothing) — so
// q.* inside it has nowhere to bubble; we use the closure to mutate
// an outer variable via captured state and let the surrounding fn
// return normally. (Not an error bubble itself — just proves the
// closure body gets scanned.)
var ErrScanned = errors.New("closure ran")

func closureInDefer(p *int) (err error) {
	defer func() {
		// A closure whose result list matches (none). If we tried to
		// q.Try here it'd have nowhere to go, so the body only has a
		// pointer-use guarded by a separate check. Left intentionally
		// simple to stay focused on the scan-reach question: does the
		// scanner walk FuncLit bodies?
		if p == nil {
			err = ErrScanned
		}
	}()
	return nil
}

// Closure nested *inside* another closure. The scanner must descend
// through two FuncLit layers and attach each q.* to the correct
// inner-most one.
func nestedClosures(s string) (int, error) {
	outer := func(in string) (int, error) {
		inner := func(x string) (int, error) {
			n := q.Try(strconv.Atoi(x))
			return n + 1, nil
		}
		return inner(in)
	}
	return outer(s)
}

// Closure returning the chain-method shape.
func closureChainWrap(s string) (int, error) {
	parse := func(in string) (int, error) {
		return q.TryE(strconv.Atoi(in)).Wrap("inner"), nil
	}
	return parse(s)
}

func report(name string, n int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
	} else {
		fmt.Printf("%s: ok=%d\n", name, n)
	}
}

func reportE(name string, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
	} else {
		fmt.Printf("%s: ok\n", name)
	}
}

func main() {
	n, err := closureInVar("11")
	report("closureInVar.ok", n, err)
	n, err = closureInVar("abc")
	report("closureInVar.bad", n, err)

	x := 7
	good := &x
	reportE("closureImmediate.ok", closureImmediate(good))
	reportE("closureImmediate.bad", closureImmediate(nil))

	reportE("closureErrorOnly.ok", closureErrorOnly(good))
	reportE("closureErrorOnly.bad", closureErrorOnly(nil))

	reportE("closureInDefer.ok", closureInDefer(good))
	reportE("closureInDefer.bad", closureInDefer(nil))

	n, err = nestedClosures("5")
	report("nestedClosures.ok", n, err)
	n, err = nestedClosures("xyz")
	report("nestedClosures.bad", n, err)

	n, err = closureChainWrap("8")
	report("closureChainWrap.ok", n, err)
	n, err = closureChainWrap("pqr")
	report("closureChainWrap.bad", n, err)
}
