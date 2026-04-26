package q

// scope.go — first-class lifetime container for assembled components.
// q.NewScope().{DeferCleanup,NoDeferCleanup,BoundTo}(...) builds a
// *Scope with the chosen close trigger; q.Assemble[T](...).WithScope(s)
// uses the scope as a shared cache + cleanup target across multiple
// assemblies. Manual Attach/Detach lets non-Assemble resources and
// nested subscopes participate in the same lifetime. ZIO Scope is the
// conceptual ancestor: a runtime resource manager that owns
// finalisers.
//
// Cache discipline (write-at-end): mid-flight cache writes never
// happen. The rewriter's WithScope IIFE accumulates fresh deps in a
// local list and Commit's them atomically on the success path. On
// failure or mid-flight Close, fresh-deps' cleanups fire locally and
// the assembly returns ErrScopeClosed; the scope's pre-existing
// entries are untouched.

import (
	"context"
	"errors"
	"sync"
)

// Scope is a runtime cache + cleanup container. Holds a typed cache
// (key = type identity string emitted by the rewriter) and an
// ordered cleanup list. Close fires every registered cleanup in
// reverse and is idempotent; Closed reports the state.
//
// Scopes are NOT tied to program startup. Useful patterns:
//
//   - Per-request: scope := q.NewScope().BoundTo(reqCtx) — handler-
//     constructed deps share a scope that closes with the request.
//   - Per-tenant: long-lived scope per tenant, closed on tenant
//     deletion.
//   - Per-session: opened when a session starts, closed on logout.
//   - Per-test: opened per test, closed in t.Cleanup.
//
// Components attach to the scope incrementally as the program
// progresses. Three registration paths cover the surface:
//
//   - q.Assemble[T](...).WithScope(s) — the rewriter Commit's freshly-
//     built deps (cache + cleanups) atomically on success.
//   - scope.Attach(c) / scope.AttachE(c) — bind a Closer / Closer-
//     with-error to the scope. *Scope itself satisfies Closer, so
//     subscopes nest naturally.
//   - scope.AttachFn(handle, cleanup) — bind a custom closure under
//     a comparable handle for later scope.Detach(handle).
//
// All accessors are safe for concurrent use. Commit and Attach
// serialise writes under a single lock; a concurrent Close either
// sees the entire batch or none of it.
type Scope struct {
	mu        sync.Mutex
	cache     map[string]any
	cleanups  []scopeCleanup
	closed    bool
	closeOnce sync.Once
}

// scopeCleanup is one entry in the scope's cleanup list. handle is
// the comparable identity used by Detach to find the entry; fn is
// the closure fired on Close.
type scopeCleanup struct {
	handle any
	fn     func()
}

// NewScope constructs a fresh *Scope with no close trigger attached.
// The caller is expected to chain one of .DeferCleanup() /
// .NoDeferCleanup() / .BoundTo(ctx) to set the close trigger:
//
//	scope := q.NewScope().DeferCleanup()             // close on enclosing func return
//	scope, shutdown := q.NewScope().NoDeferCleanup() // caller-managed close
//	scope := q.NewScope().BoundTo(ctx)               // close on ctx cancellation
//
// Calling q.NewScope() without a chain terminator is also valid —
// the caller manages lifetime explicitly via scope.Close() at the
// right point. The chain is sugar for the three common patterns.
func NewScope() *Scope {
	return &Scope{cache: map[string]any{}}
}

// DeferCleanup chains onto a freshly-constructed scope. The
// preprocessor injects a `defer scope.Close()` into the enclosing
// function so cleanups fire when that function returns. Returns
// the same *Scope so the result is directly usable.
//
// The runtime body is unreachable in a successful build — bare
// invocation (rewriter missed it) panics so the gap is loud.
//
//	scope := q.NewScope().DeferCleanup()
//	server := q.Try(q.Assemble[*Server](recipes...).WithScope(scope))
//	// scope.Close() fires when the enclosing function returns.
//
// The chain ONLY works when applied directly to q.NewScope() — the
// rewriter recognises the literal `q.NewScope().DeferCleanup()`
// shape. Calling .DeferCleanup() on an existing scope (e.g. one
// passed in as a parameter) is rejected by the rewriter.
func (s *Scope) DeferCleanup() *Scope {
	panicUnrewritten("q.NewScope().DeferCleanup")
	return s
}

// NoDeferCleanup chains onto a freshly-constructed scope. Returns
// the same *Scope plus an idempotent close func wrapping
// scope.Close. The caller is responsible for firing close.
//
//	scope, shutdown := q.NewScope().NoDeferCleanup()
//	defer shutdown()
//	server := q.Try(q.Assemble[*Server](recipes...).WithScope(scope))
//
// Equivalent to q.NewScope() plus calling scope.Close() directly;
// the close func form composes with patterns like
// context.AfterFunc(ctx, shutdown) or signal-handler wiring.
func (s *Scope) NoDeferCleanup() (*Scope, func()) {
	return s, s.Close
}

// BoundTo chains onto a freshly-constructed scope and wires its
// close to fire when ctx is cancelled (via context.AfterFunc).
// Returns the same *Scope. Direct scope.Close() remains valid;
// both paths are idempotent.
//
//	scope := q.NewScope().BoundTo(ctx)
//	server := q.Try(q.Assemble[*Server](recipes...).WithScope(scope))
//	// scope.Close() fires when ctx is cancelled — typical app-shutdown
//	// or per-request pattern.
func (s *Scope) BoundTo(ctx context.Context) *Scope {
	context.AfterFunc(ctx, s.Close)
	return s
}

// Close fires every registered cleanup in reverse registration
// order. Idempotent — sync.Once-wrapped; duplicate calls are safe.
// After Close, Load returns (nil, false) for any key, Commit /
// Attach* return ErrScopeClosed, and Detach is a no-op.
func (s *Scope) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		cleanups := s.cleanups
		s.cleanups = nil
		s.mu.Unlock()
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i].fn()
		}
	})
}

// Closed reports whether Close has been called.
func (s *Scope) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Load returns the cached value for key, if any, plus ok==true on
// hit. Returns (nil, false) when the key is absent OR the scope
// is closed. The WithScope IIFE additionally checks Closed() on a
// miss to disambiguate "build fresh" from "bail with
// ErrScopeClosed".
func (s *Scope) Load(key string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, false
	}
	v, ok := s.cache[key]
	return v, ok
}

// Attach binds a Closer (anything with `Close()`) to the scope.
// scope.Close fires c.Close along with everything else, in
// reverse registration order. Returns ErrScopeClosed if the
// scope is already closed (the caller should fire c.Close
// directly in that case).
//
// *Scope itself satisfies the Closer interface, so subscopes
// nest naturally:
//
//	parent := q.NewScope().BoundTo(ctx)
//	child := q.NewScope()
//	parent.Attach(child)
//	// when parent closes, child closes too.
//
// Pass c again to scope.Detach to remove the binding before
// scope.Close runs.
func (s *Scope) Attach(c interface{ Close() }) error {
	return s.attachWithHandle(c, c.Close)
}

// AttachE is Attach for resources whose Close returns an error.
// Errors from Close at scope-close time are routed through
// q.LogCloseErr — the same sink q.Assemble's auto-cleanup uses,
// so failed teardown is logged structurally rather than silently
// dropped.
func (s *Scope) AttachE(c interface{ Close() error }) error {
	return s.attachWithHandle(c, func() { LogCloseErr(c.Close(), "scope.AttachE") })
}

// AttachFn binds a custom cleanup closure under handle's identity.
// handle must be == comparable (pointer, comparable struct,
// interface holding such); pass it back to scope.Detach to remove
// the binding before scope.Close runs.
//
//	conn := openConnection()
//	if err := scope.AttachFn(conn, func() { conn.Drain(); conn.Close() }); err != nil {
//	    conn.Close() // scope was already closed; fall back
//	    return err
//	}
//
// A nil handle or nil cleanup is rejected.
func (s *Scope) AttachFn(handle any, cleanup func()) error {
	if handle == nil {
		return errors.New("q.Scope.AttachFn: handle is nil")
	}
	if cleanup == nil {
		return errors.New("q.Scope.AttachFn: cleanup is nil")
	}
	return s.attachWithHandle(handle, cleanup)
}

// AttachFnE is AttachFn for cleanup funcs that return an error.
// The returned error at scope-close time is routed through
// q.LogCloseErr.
func (s *Scope) AttachFnE(handle any, cleanup func() error) error {
	if handle == nil {
		return errors.New("q.Scope.AttachFnE: handle is nil")
	}
	if cleanup == nil {
		return errors.New("q.Scope.AttachFnE: cleanup is nil")
	}
	return s.attachWithHandle(handle, func() { LogCloseErr(cleanup(), "scope.AttachFnE") })
}

// Detach removes the cleanup registered under handle's identity.
// Returns true if found and removed, false if no match (including
// after scope.Close, which clears all registrations). Detach
// matches the FIRST cleanup registered under the handle — if a
// handle was attached more than once, repeated Detach calls peel
// them off in reverse-registration order.
//
// handle must be == comparable; passing an uncomparable value
// (slice, map, func) panics — same rule as Go's map keys.
func (s *Scope) Detach(handle any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		if s.cleanups[i].handle == handle {
			s.cleanups = append(s.cleanups[:i], s.cleanups[i+1:]...)
			return true
		}
	}
	return false
}

// attachWithHandle is the shared impl: lock, check closed, append.
func (s *Scope) attachWithHandle(handle any, cleanup func()) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrScopeClosed
	}
	s.cleanups = append(s.cleanups, scopeCleanup{handle: handle, fn: cleanup})
	return nil
}

// ScopeEntry is a fresh-built cache entry staged by a WithScope
// IIFE for atomic registration via Commit. Cleanups for the
// freshly-built deps live on the IIFE's internal scope (the child
// arg to Commit); ScopeEntry carries only the cache write.
type ScopeEntry struct {
	Key   string
	Value any
}

// Commit atomically writes a batch of cache entries and (if non-
// nil) attaches child as a single cleanup entry under the same
// lock acquisition. A concurrent Close either sees the whole
// batch or none of it. Returns ErrScopeClosed if s was closed
// before the call.
//
// The WithScope IIFE pattern: build fresh deps into a per-call
// internal scope (via internal.AttachE / internal.AttachFn etc.),
// then call external.Commit(freshCache, internal). Closing
// external cascades through internal in reverse-attach order so
// the per-call deps close together, after later-registered scope
// entries.
//
// child can be nil — Commit then writes only the cache entries.
// Use that shape for non-resource bulk loads.
func (s *Scope) Commit(entries []ScopeEntry, child *Scope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrScopeClosed
	}
	for _, e := range entries {
		s.cache[e.Key] = e.Value
	}
	if child != nil {
		s.cleanups = append(s.cleanups, scopeCleanup{handle: child, fn: child.Close})
	}
	return nil
}

// ErrScopeClosed is returned by q.Assemble[T](...).WithScope(s) and
// by scope.Attach* / scope.Commit when s was closed before or
// during the call. Use errors.Is to detect across wrappings.
var ErrScopeClosed = errors.New("q: scope closed")
