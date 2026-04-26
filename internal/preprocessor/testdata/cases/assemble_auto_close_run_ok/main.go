// Fixture: auto-detect Close() / Close() error / channel cleanup
// on q.Assemble recipes. Constructors that return a type with a
// recognisable Close shape get an automatically synthesised cleanup
// — no need for the user to wrap external-lib ctors in (T, func(),
// error)-shaped adapters.
//
// Validates:
//   1. T with `Close()` (no return) → cleanup calls `t.Close()`.
//   2. T with `Close() error` → cleanup calls `_ = t.Close()`.
//   3. Channel T → cleanup calls `close(t)`.
//   4. Inline values are user-owned: NEVER auto-closed even if T has
//      a Close shape.
//   5. Mix of auto-detected and explicit cleanups; reverse-topo
//      ordering preserved.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

var log []string

// VoidCloser has Close() — auto-cleanup synthesises t.Close().
type VoidCloser struct{ name string }

func (v *VoidCloser) Close() { log = append(log, "void:"+v.name) }

// ErrCloser has Close() error — auto-cleanup synthesises _ = t.Close().
type ErrCloser struct{ name string }

func (e *ErrCloser) Close() error {
	log = append(log, "err:"+e.name)
	return nil
}

// chanWrap exposes a chan; auto-cleanup synthesises close(c).
type chanWrap chan struct{}

type App struct {
	v *VoidCloser
	e *ErrCloser
	c chanWrap
}

// Pure (T, error) ctors — q.Assemble auto-detects the cleanup from T.
func newVoid() (*VoidCloser, error) { return &VoidCloser{name: "v1"}, nil }
func newErr() (*ErrCloser, error)   { return &ErrCloser{name: "e1"}, nil }
func newChan() (chanWrap, error)    { return chanWrap(make(chan struct{}, 1)), nil }

func newApp(v *VoidCloser, e *ErrCloser, c chanWrap) *App {
	return &App{v: v, e: e, c: c}
}

func main() {
	app, shutdown, err := q.Assemble[*App](newVoid, newErr, newChan, newApp).NoDeferCleanup()
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println("v:", app.v.name)
	fmt.Println("e:", app.e.name)

	shutdown()

	// Reverse-topo of resource recipes: chan was last → fires first;
	// then err; then void. (newApp itself is non-resource — no
	// Close() — so doesn't push.)
	fmt.Println("teardown order:")
	for _, s := range log {
		fmt.Println(" -", s)
	}
}
