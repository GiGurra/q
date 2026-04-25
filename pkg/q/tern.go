package q

// tern.go — q.Tern: a conditional-expression helper. The
// preprocessor rewrites the call to an IIFE that only evaluates `t`
// when `cond` is true; otherwise the IIFE returns T's zero value.
//
// Surface (one entry, two args):
//
//	q.Tern[T any](cond bool, t T) T
//
// Despite Go's standard arg-evaluation semantics (which would
// evaluate `t` at the call site unconditionally), the preprocessor
// splices each arg's *source text* into the chosen branch of an
// IIFE — so `t`'s expression is only evaluated when `cond` is true.
// That's the whole point: a syntactically simple ternary that
// behaves like a real conditional expression rather than a function
// call.
//
// The "false" branch is implicitly the zero value of T. For
// situations where you want an explicit second branch, write the
// `if` yourself (it's two more lines and clearer).
//
// Examples:
//
//	// String fallback — Name only computed when user != nil:
//	display := q.Tern[string](user != nil, user.Name)
//
//	// Lazy expensive call — only invoked when cache misses:
//	value := q.Tern[*Conn](missing, openExpensiveConn())
//
//	// Sentinel default via the zero-value branch:
//	max := q.Tern[int](a > b, a) // returns 0 when a <= b — usually
//	                             // not what you want; reach for `if`
//	                             // instead when both branches matter.
//
// The preprocessor's source-splicing also means side-effects in `t`
// only fire on the true path. q.Tern is a sugar around `if` — not
// a runtime helper.

// Tern returns t when cond evaluates to true; otherwise the zero
// value of T. Despite the eager-looking call form, t is only
// evaluated on the true path — the preprocessor rewrites every
// call site at compile time. The runtime body is unreachable in a
// successful build.
func Tern[T any](cond bool, t T) T {
	panicUnrewritten("q.Tern")
	var zero T
	return zero
}
