//go:build qtoolexec

// Unit tests for q.Scope. Build-tag gated so plain `go test ./...`
// skips them — pkg/q's link gate fails to link without -toolexec=q.
// The e2e harness's TestPackageQUnit invokes these via
// `go test -tags=qtoolexec -toolexec=<qBin>`.

package q_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/GiGurra/q/pkg/q"
)

func TestScope_NewScope_Empty(t *testing.T) {
	s := q.NewScope()
	if s.Closed() {
		t.Fatal("fresh scope reports closed")
	}
	if v, ok := s.Load("anything"); ok || v != nil {
		t.Fatalf("Load on empty scope: got (%v, %v), want (nil, false)", v, ok)
	}
}

func TestScope_NoDeferCleanup_ReturnsCloseFunc(t *testing.T) {
	s, close := q.NewScope().NoDeferCleanup()
	if s == nil {
		t.Fatal("NoDeferCleanup returned nil scope")
	}
	if close == nil {
		t.Fatal("NoDeferCleanup returned nil close func")
	}
	close()
	if !s.Closed() {
		t.Fatal("scope not closed after close func ran")
	}
	close()
	close()
}

func TestScope_BoundTo_ClosesOnCtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := q.NewScope().BoundTo(ctx)
	if s.Closed() {
		t.Fatal("scope closed before ctx cancellation")
	}
	cancel()
	for range 100 {
		if s.Closed() {
			break
		}
		runtimeGosched()
	}
	if !s.Closed() {
		t.Fatal("scope did not close after ctx cancellation")
	}
}

func TestScope_Commit_CachesAndAttachesChild(t *testing.T) {
	external := q.NewScope()
	internal := q.NewScope()
	var fired []string
	type h struct{ id int }
	if err := internal.AttachFn(&h{1}, func() { fired = append(fired, "c0") }); err != nil {
		t.Fatalf("internal.AttachFn c0: %v", err)
	}
	if err := internal.AttachFn(&h{2}, func() { fired = append(fired, "c1") }); err != nil {
		t.Fatalf("internal.AttachFn c1: %v", err)
	}
	if err := external.Commit([]q.ScopeEntry{
		{Key: "k0", Value: "v0"},
		{Key: "k1", Value: "v1"},
	}, internal); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if v, ok := external.Load("k0"); !ok || v != "v0" {
		t.Errorf("Load k0: %v %v", v, ok)
	}
	external.Close()
	want := []string{"c1", "c0"}
	if !equalSlices(fired, want) {
		t.Errorf("cleanup order: got %v, want %v", fired, want)
	}
	if !internal.Closed() {
		t.Error("internal child scope not closed when external closed")
	}
}

func TestScope_Commit_NilChild_CacheOnly(t *testing.T) {
	s := q.NewScope()
	if err := s.Commit([]q.ScopeEntry{{Key: "k", Value: "v"}}, nil); err != nil {
		t.Fatalf("Commit nil child: %v", err)
	}
	if v, ok := s.Load("k"); !ok || v != "v" {
		t.Errorf("Load: %v %v", v, ok)
	}
}

func TestScope_Close_Idempotent(t *testing.T) {
	s := q.NewScope()
	var n atomic.Int32
	if err := s.AttachFn("h", func() { n.Add(1) }); err != nil {
		t.Fatalf("AttachFn: %v", err)
	}
	s.Close()
	s.Close()
	s.Close()
	if got := n.Load(); got != 1 {
		t.Errorf("cleanup ran %d times, want 1", got)
	}
}

func TestScope_Commit_AfterClose_Errors(t *testing.T) {
	s := q.NewScope()
	s.Close()
	err := s.Commit([]q.ScopeEntry{{Key: "k", Value: "v"}}, nil)
	if !errors.Is(err, q.ErrScopeClosed) {
		t.Fatalf("Commit after close: got %v, want q.ErrScopeClosed", err)
	}
}

func TestScope_Load_AfterClose_ReturnsFalse(t *testing.T) {
	s := q.NewScope()
	if err := s.Commit([]q.ScopeEntry{{Key: "k", Value: "v"}}, nil); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if v, ok := s.Load("k"); !ok || v != "v" {
		t.Fatalf("pre-close Load: %v %v", v, ok)
	}
	s.Close()
	if v, ok := s.Load("k"); ok || v != nil {
		t.Errorf("post-close Load: got (%v, %v), want (nil, false)", v, ok)
	}
}

type closer struct {
	closed *atomic.Bool
}

func (c *closer) Close() { c.closed.Store(true) }

type closerE struct {
	closed *atomic.Bool
	err    error
}

func (c *closerE) Close() error {
	c.closed.Store(true)
	return c.err
}

func TestScope_Attach_FiresOnClose(t *testing.T) {
	s := q.NewScope()
	var b atomic.Bool
	c := &closer{closed: &b}
	if err := s.Attach(c); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	s.Close()
	if !b.Load() {
		t.Fatal("Closer.Close not invoked")
	}
}

func TestScope_AttachE_FiresOnClose(t *testing.T) {
	s := q.NewScope()
	var b atomic.Bool
	c := &closerE{closed: &b}
	if err := s.AttachE(c); err != nil {
		t.Fatalf("AttachE: %v", err)
	}
	s.Close()
	if !b.Load() {
		t.Fatal("CloserE.Close not invoked")
	}
}

func TestScope_AttachFn_AndDetach(t *testing.T) {
	s := q.NewScope()
	var n atomic.Int32
	type handle struct{ id int }
	h := &handle{id: 1}
	if err := s.AttachFn(h, func() { n.Add(1) }); err != nil {
		t.Fatalf("AttachFn: %v", err)
	}
	if !s.Detach(h) {
		t.Fatal("Detach returned false for known handle")
	}
	s.Close()
	if got := n.Load(); got != 0 {
		t.Errorf("cleanup ran after Detach: %d", got)
	}
}

func TestScope_Detach_Unknown_ReturnsFalse(t *testing.T) {
	s := q.NewScope()
	type handle struct{ id int }
	h := &handle{id: 1}
	if s.Detach(h) {
		t.Error("Detach of never-attached handle returned true")
	}
}

func TestScope_AttachAfterClose_Errors(t *testing.T) {
	s := q.NewScope()
	s.Close()
	type handle struct{}
	if err := s.AttachFn(&handle{}, func() {}); !errors.Is(err, q.ErrScopeClosed) {
		t.Errorf("AttachFn after close: got %v, want q.ErrScopeClosed", err)
	}
	var b atomic.Bool
	if err := s.Attach(&closer{closed: &b}); !errors.Is(err, q.ErrScopeClosed) {
		t.Errorf("Attach after close: got %v, want q.ErrScopeClosed", err)
	}
}

func TestScope_NestedSubscopes(t *testing.T) {
	parent := q.NewScope()
	child := q.NewScope()
	var grandchildClosed atomic.Bool
	if err := child.AttachFn(&grandchildClosed, func() { grandchildClosed.Store(true) }); err != nil {
		t.Fatalf("child.AttachFn: %v", err)
	}
	if err := parent.Attach(child); err != nil {
		t.Fatalf("parent.Attach(child): %v", err)
	}
	parent.Close()
	if !child.Closed() {
		t.Fatal("child not closed when parent closed")
	}
	if !grandchildClosed.Load() {
		t.Fatal("grandchild cleanup not fired")
	}
}

func TestScope_AttachFn_NilArgs_Rejected(t *testing.T) {
	s := q.NewScope()
	if err := s.AttachFn(nil, func() {}); err == nil {
		t.Error("AttachFn(nil handle) did not error")
	}
	type h struct{}
	if err := s.AttachFn(&h{}, nil); err == nil {
		t.Error("AttachFn(nil cleanup) did not error")
	}
}

func TestScope_ConcurrentAttachAndClose(t *testing.T) {
	s := q.NewScope()
	var n atomic.Int32
	var wg sync.WaitGroup
	const N = 50
	wg.Add(N)
	for i := range N {
		go func(i int) {
			defer wg.Done()
			h := struct{ ID int }{ID: i}
			if err := s.AttachFn(h, func() { n.Add(1) }); err != nil {
				return
			}
		}(i)
	}
	wg.Wait()
	s.Close()
	if got := n.Load(); got > N {
		t.Errorf("more cleanups fired than attached: %d > %d", got, N)
	}
	if !s.Closed() {
		t.Error("scope not closed after Close")
	}
}

func equalSlices[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

// runtimeGosched yields the goroutine briefly so a goroutine
// dispatched by context.AfterFunc has a chance to run. Used by
// the BoundTo test which polls Closed() in a bounded loop.
func runtimeGosched() {
	for range 1000 {
	}
}
