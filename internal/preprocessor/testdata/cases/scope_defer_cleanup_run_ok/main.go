// Fixture: q.NewScope().DeferCleanup() — the rewriter must inject
// `defer scope.Close()` into the enclosing function. Verifies that
// cleanups attached to the scope fire in reverse order on function
// return, and that .NoDeferCleanup / .BoundTo work as plain runtime
// chain methods.
package main

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

type closer struct {
	name string
	out  *[]string
}

func (c *closer) Close() { *c.out = append(*c.out, c.name) }

func deferShape() []string {
	var fired []string
	scope := q.NewScope().DeferCleanup()
	if err := scope.Attach(&closer{name: "first", out: &fired}); err != nil {
		fmt.Println("attach1:", err)
	}
	if err := scope.Attach(&closer{name: "second", out: &fired}); err != nil {
		fmt.Println("attach2:", err)
	}
	return fired // Captured by reference; the deferred Close mutates the slice.
}

func noDeferShape() []string {
	var fired []string
	scope, shutdown := q.NewScope().NoDeferCleanup()
	if err := scope.Attach(&closer{name: "alpha", out: &fired}); err != nil {
		fmt.Println("attach1:", err)
	}
	if err := scope.Attach(&closer{name: "beta", out: &fired}); err != nil {
		fmt.Println("attach2:", err)
	}
	shutdown()
	return fired
}

func boundToShape() []string {
	var fired []string
	ctx, cancel := context.WithCancel(context.Background())
	scope := q.NewScope().BoundTo(ctx)
	if err := scope.Attach(&closer{name: "x", out: &fired}); err != nil {
		fmt.Println("attach1:", err)
	}
	if err := scope.Attach(&closer{name: "y", out: &fired}); err != nil {
		fmt.Println("attach2:", err)
	}
	cancel()
	for range 200 {
		if scope.Closed() {
			break
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	return fired
}

func main() {
	// fired is mutated by the deferred Close after deferShape returns,
	// so we observe the order via a closure pattern.
	var deferred []string
	func() {
		scope := q.NewScope().DeferCleanup()
		if err := scope.Attach(&closer{name: "first", out: &deferred}); err != nil {
			fmt.Println("attach:", err)
		}
		if err := scope.Attach(&closer{name: "second", out: &deferred}); err != nil {
			fmt.Println("attach:", err)
		}
	}()
	fmt.Println("defer:", deferred)

	fmt.Println("nodefer:", noDeferShape())
	fmt.Println("boundto:", boundToShape())
}
