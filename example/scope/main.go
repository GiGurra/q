// example/scope mirrors docs/api/scope.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/scope
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// closer / closerE / handle types — minimal stand-ins for the doc's
// myWorker / myDB / myConn so the example exercises every Attach
// shape end-to-end.

type closer struct {
	name   string
	closed *bool
}

func (c *closer) Close() { *c.closed = true; fmt.Printf("close: %s\n", c.name) }

type closerE struct {
	name   string
	closed *bool
}

func (c *closerE) Close() error { *c.closed = true; fmt.Printf("closeE: %s\n", c.name); return nil }

type handle struct{ name string }

// ---------- "Construction terminators — DeferCleanup (auto-defer)" ----------
//
//	scope := q.NewScope().DeferCleanup()
func usingDeferCleanup() {
	scope := q.NewScope().DeferCleanup()
	worker := &closer{name: "worker", closed: new(bool)}
	_ = scope.Attach(worker)
	fmt.Println("usingDeferCleanup: pre-return")
	// scope.Close() fires when this function returns.
}

// ---------- "NoDeferCleanup — caller-managed close" ----------
//
//	scope, shutdown := q.NewScope().NoDeferCleanup()
func usingNoDeferCleanup() {
	scope, shutdown := q.NewScope().NoDeferCleanup()
	defer shutdown()
	worker := &closer{name: "ndc-worker", closed: new(bool)}
	_ = scope.Attach(worker)
	// shutdown is sync.Once-backed: calling twice is safe.
	shutdown()
	shutdown()
}

// ---------- "BoundTo(ctx) — close on ctx cancellation" ----------
//
//	scope := q.NewScope().BoundTo(ctx)
func usingBoundTo() {
	ctx, cancel := context.WithCancel(context.Background())
	scope := q.NewScope().BoundTo(ctx)
	db := &closer{name: "db", closed: new(bool)}
	_ = scope.Attach(db)
	cancel()
	// give the AfterFunc a chance to fire by waiting on a channel
	// the cleanup will close.
	done := make(chan struct{})
	_ = scope.AttachFn(&handle{name: "done-signaller"}, func() { close(done) })
	// note: the close-fn we just attached fires before db's Close
	// (LIFO), so wait the other way: kick another scope or just
	// poll briefly.
	<-done
}

// ---------- "Attaching things — Attach, AttachE, AttachFn, AttachFnE" ----------
func attachShapes() {
	scope := q.NewScope()
	defer scope.Close()

	wClosed := false
	dbClosed := false
	connDrained := false
	streamFlushed := false

	// Closer with void Close().
	w := &closer{name: "myWorker", closed: &wClosed}
	_ = scope.Attach(w)

	// Closer with Close() error — errors routed through q.LogCloseErr.
	db := &closerE{name: "myDB", closed: &dbClosed}
	_ = scope.AttachE(db)

	// Custom closure — handle for later Detach.
	conn := &handle{name: "myConn"}
	_ = scope.AttachFn(conn, func() { connDrained = true; fmt.Println("conn drain+close") })

	// Error-returning custom closure — error routed through q.LogCloseErr.
	stream := &handle{name: "myStream"}
	_ = scope.AttachFnE(stream, func() error { streamFlushed = true; fmt.Println("stream flush"); return nil })

	_, _, _, _ = wClosed, dbClosed, connDrained, streamFlushed
}

// ---------- "Detach(handle) bool" ----------
func detachShape() {
	scope := q.NewScope()
	defer scope.Close()
	conn := &handle{name: "detachable"}
	_ = scope.AttachFn(conn, func() { fmt.Println("would-close: detachable") })
	if scope.Detach(conn) {
		fmt.Println("detached: detachable (cleanup unregistered)")
	}
}

// ---------- "Subscopes nest" ----------
func subscopes() {
	parent := q.NewScope()
	defer parent.Close()
	child := q.NewScope()
	_ = parent.Attach(child)
	_ = child.AttachFn(&handle{name: "child-leaf"}, func() { fmt.Println("close: child-leaf") })
}

// ---------- "ErrScopeClosed sentinel" ----------
func errScopeClosed() {
	scope := q.NewScope()
	scope.Close()
	err := scope.Attach(&closer{name: "late", closed: new(bool)})
	fmt.Printf("Attach after close: is(q.ErrScopeClosed)=%v\n", errors.Is(err, q.ErrScopeClosed))
	fmt.Printf("Detach after close: %v\n", scope.Detach(&handle{name: "anything"}))
}

// ---------- "LIFO close order" ----------
func lifoOrder() {
	scope := q.NewScope()
	for i, name := range []string{"first", "second", "third"} {
		i, name := i, name
		_ = scope.AttachFn(&handle{name: fmt.Sprintf("h%d", i)}, func() { fmt.Printf("lifo: %s\n", name) })
	}
	scope.Close()
}

func main() {
	usingDeferCleanup()
	fmt.Println("---")
	usingNoDeferCleanup()
	fmt.Println("---")
	usingBoundTo()
	fmt.Println("---")
	attachShapes()
	fmt.Println("---")
	detachShape()
	fmt.Println("---")
	subscopes()
	fmt.Println("---")
	errScopeClosed()
	fmt.Println("---")
	lifoOrder()
}
