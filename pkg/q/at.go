package q

// at.go — q.At: nested-nil safe traversal with a chain of fallbacks.
//
// Surface (one entry, three terminals in v1):
//
//	q.At[T any](expr T) PathChain[T]
//
//	(PathChain[T]) OrElse(alt T) PathChain[T]   // chain another path / value
//	(PathChain[T]) Or(fallback T) T              // terminal: literal/expr fallback
//	(PathChain[T]) OrZero() T                 // terminal: zero value of T
//
// The preprocessor rewrites every q.At chain at the call site into an
// IIFE that walks each path, nil-guards every nilable hop, and falls
// through to the next path's start label (or the terminal block) when
// any guard fires:
//
//	v := q.At(user.Profile.Theme).
//	     OrElse(user.Defaults.Theme).
//	     Or("light")
//
// rewrites to (approximately):
//
//	v := func() string {
//	    _qAt0_0 := user;        if _qAt0_0 == nil { goto _qAtAlt1 }
//	    _qAt0_1 := _qAt0_0.Profile; if _qAt0_1 == nil { goto _qAtAlt1 }
//	    return _qAt0_1.Theme
//	_qAtAlt1:
//	    _qAt1_0 := user;        if _qAt1_0 == nil { goto _qAtAlt2 }
//	    _qAt1_1 := _qAt1_0.Defaults; if _qAt1_1 == nil { goto _qAtAlt2 }
//	    return _qAt1_1.Theme
//	_qAtAlt2:
//	    return "light"
//	}()
//
// Lazy semantics fall out for free: each path's expression source
// text is spliced into its own arm, so a path is only evaluated when
// reached. .Or's fallback expression is only evaluated when every
// path is nil.
//
// Bubble terminals (.OrErr / .OrWrap / etc.) are deferred. Today, to
// bubble a specific error use q.NotNilE on the leaf instead — q.At
// is for value-fallback shapes.

// At begins a nested-nil safe traversal chain. The argument is a path
// expression — a selector chain like `user.Profile.Theme` is the
// common case; a single identifier or a call result also works. The
// returned PathChain[T] is closed by .Or(fallback) or .OrZero();
// .OrElse(alt) chains additional paths / values to try before the
// terminal kicks in.
//
// The runtime body is unreachable in a successful build — every q.At
// chain is rewritten away by the preprocessor.
func At[T any](expr T) PathChain[T] {
	panicUnrewritten("q.At")
	return PathChain[T]{}
}

// PathChain carries the running q.At chain. Its methods are all
// rewritten away — the struct is never materialized in production
// code.
type PathChain[T any] struct {
	// v is documented as part of the chain contract; the rewriter
	// consults the surrounding source-text rather than this field, but
	// keeping it carries the type parameter through gopls.
	v T //nolint:unused
}

// OrElse appends another path / value to try when every prior path
// has yielded nil. alt may itself be a selector chain (the rewriter
// walks it with the same per-hop nil checks) or any other expression
// (returned as-is when reached). alt is evaluated lazily — only when
// every prior path was nil.
func (c PathChain[T]) OrElse(alt T) PathChain[T] {
	panicUnrewritten("q.At(...).OrElse")
	return c
}

// Or terminates the chain with a literal/expression fallback. Returns
// the first non-nil path's value, falling back to fallback when every
// path is nil. fallback is evaluated lazily.
func (c PathChain[T]) Or(fallback T) T {
	panicUnrewritten("q.At(...).Or")
	return c.v
}

// OrZero terminates the chain with the zero value of T. Returns
// the first non-nil path's value, or T's zero value when every path
// is nil. Useful when the natural fallback is the empty / zero state.
func (c PathChain[T]) OrZero() T {
	panicUnrewritten("q.At(...).OrZero")
	return c.v
}

// OrError terminates the chain with an error bubble. Returns the
// first non-nil path's value; if every path is nil, the rewriter
// emits an early return that bubbles err through the enclosing
// function's error return slot. The enclosing function MUST have a
// trailing `error` return — same constraint as q.Try.
//
// Example:
//
//	cfg := q.At(opts.Config).OrError(ErrConfigMissing)
//	// Returns opts.Config when non-nil; bubbles ErrConfigMissing
//	// otherwise (function must return ..., error).
func (c PathChain[T]) OrError(err error) T {
	panicUnrewritten("q.At(...).OrError")
	return c.v
}

// OrE terminates the chain by delegating to a (T, error)-returning
// fallback fetcher. Returns the first non-nil path's value; if every
// path is nil, the fetcher's call result drives the result — its T
// becomes the chain's value, its error (if non-nil) bubbles through
// the enclosing function's error return slot.
//
// Spread form: pass a single (T, error)-returning call so Go's
// f(g())-multi-spread rule applies.
//
// Example:
//
//	cfg := q.At(cache.Config).OrE(loadFromDisk(path))
//	// Cache hit -> use cached. Cache miss -> call loadFromDisk; its
//	// error (if any) bubbles, its value (if ok) becomes the result.
func (c PathChain[T]) OrE(v T, err error) T {
	panicUnrewritten("q.At(...).OrE")
	return c.v
}
