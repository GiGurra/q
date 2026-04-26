package q

import "sync"

// lazy.go — q.Lazy: deferred evaluation of arbitrary expressions.
//
// Surface (one entry, two methods):
//
//	q.Lazy[T any](v T) *LazyValue[T]
//	(*LazyValue[T]).Value() T          // sync.Once-backed first-eval
//	(*LazyValue[T]).IsForced() bool    // diagnostic: has Value() been called?
//
// The user writes the call as if it were eager:
//
//	l := q.Lazy(calculateValue())
//
// but the preprocessor rewrites the call site to wrap the value
// expression in a thunk closure:
//
//	l := q.LazyFromThunk(func() int { return calculateValue() })
//
// so calculateValue() runs only on the first .Value() call.
//
// Thread safety: .Value() is sync.Once-backed; concurrent first-call
// races resolve to a single execution, with later callers seeing the
// memoised result.
//
// Recursive .Value() inside the thunk deadlocks via sync.Once. This
// is the Go-standard behaviour and is not guarded.

// Lazy is the deferred-value handle. The fields are unexported and
// only set by q.LazyFromThunk; the rewriter never materialises this
// struct directly at user call sites.
type LazyValue[T any] struct {
	once   sync.Once
	thunk  func() T
	value  T
	forced bool
}

// LazyFromThunk constructs a *LazyValue[T] from an explicit thunk. Callers
// should normally write q.Lazy(<expr>) and let the preprocessor wrap
// the expression in a thunk; LazyFromThunk is the underlying real
// constructor that the rewriter targets, exported so the rewritten
// code can reach it.
//
// Plain runtime function — NOT rewritten by the preprocessor. Safe to
// use directly when you genuinely have a hand-written thunk and want
// the same semantics without going through the q.Lazy(<expr>) sugar.
func LazyFromThunk[T any](thunk func() T) *LazyValue[T] {
	return &LazyValue[T]{thunk: thunk}
}

// Lazy wraps the value expression in a thunk via the preprocessor.
// The runtime body is unreachable in a successful build — every
// q.Lazy(<expr>) call site is rewritten away to q.LazyFromThunk(...).
func Lazy[T any](v T) *LazyValue[T] {
	panicUnrewritten("q.Lazy")
	return &LazyValue[T]{value: v, forced: true}
}

// Value evaluates the wrapped thunk on the first call (under
// sync.Once) and returns the cached value on every subsequent call.
// Concurrent first-call races resolve to a single thunk execution.
func (l *LazyValue[T]) Value() T {
	l.once.Do(func() {
		if l.thunk != nil {
			l.value = l.thunk()
		}
		l.forced = true
	})
	return l.value
}

// IsForced reports whether the underlying thunk has been evaluated.
// Diagnostic only — observing IsForced does not race with Value, but
// callers that act on the result race with concurrent first-call
// users in the obvious way.
func (l *LazyValue[T]) IsForced() bool {
	return l.forced
}

// LazyValueE is the (T, error)-shaped sibling of LazyValue. The
// wrapped thunk runs once on first .Value() under sync.Once; both T
// and error are cached and returned on every subsequent call.
type LazyValueE[T any] struct {
	once   sync.Once
	thunk  func() (T, error)
	value  T
	err    error
	forced bool
}

// LazyEFromThunk constructs a *LazyValueE[T] from an explicit thunk.
// The rewriter targets this constructor when it lowers q.LazyE(<call>);
// callers may also reach for it directly when they have a hand-written
// (T, error) thunk.
//
// Plain runtime function — NOT rewritten by the preprocessor.
func LazyEFromThunk[T any](thunk func() (T, error)) *LazyValueE[T] {
	return &LazyValueE[T]{thunk: thunk}
}

// LazyE wraps a (T, error)-returning call site in a thunk via the
// preprocessor. The user writes:
//
//	l := q.LazyE(loadConfig())  // loadConfig() (Config, error)
//
// and the rewriter emits q.LazyEFromThunk(func() (Config, error) {
// return loadConfig() }). The runtime body is unreachable in a
// successful build.
//
// At the consumer, pair .Value() with q.Try:
//
//	cfg := q.Try(l.Value())
func LazyE[T any](v T, err error) *LazyValueE[T] {
	panicUnrewritten("q.LazyE")
	return &LazyValueE[T]{value: v, err: err, forced: true}
}

// Value evaluates the wrapped thunk on the first call (under sync.Once)
// and returns the cached (T, error) pair on every subsequent call. A
// non-nil error from the first call is cached too — repeated calls
// keep returning it; the thunk does not retry.
func (l *LazyValueE[T]) Value() (T, error) {
	l.once.Do(func() {
		if l.thunk != nil {
			l.value, l.err = l.thunk()
		}
		l.forced = true
	})
	return l.value, l.err
}

// IsForced reports whether the underlying thunk has been evaluated.
// Diagnostic only.
func (l *LazyValueE[T]) IsForced() bool {
	return l.forced
}
