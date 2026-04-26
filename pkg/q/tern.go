package q

// tern.go — q.Tern: a conditional-expression helper. The
// preprocessor rewrites the call to an IIFE that evaluates whichever
// branch matches `cond` — the other branch is never executed.
//
// Surface (one entry, three args):
//
//	q.Tern[T any](cond bool, ifTrue, ifFalse T) T
//
// Despite Go's standard arg-evaluation semantics (which would
// evaluate both branches at the call site), the preprocessor
// splices each branch's *source text* into the matching arm of an
// IIFE — so each branch's expression is only evaluated when its
// arm is taken. That's the whole point: a syntactically simple
// ternary that behaves like a real conditional expression rather
// than a function call.
//
// T is inferred from the branch values — the explicit
// `q.Tern[T](...)` form is also accepted but rarely needed (use it
// when you want to widen the result to an interface, or when both
// branches are untyped constants needing a non-default type).
//
// Examples:
//
//	// Pick between two values, lazy on the unused branch:
//	max := q.Tern(a > b, a, b)
//
//	// Lazy expensive fallback — slow() only when cache misses:
//	v := q.Tern(cached, fast(), slow())
//
//	// Chained for multi-way pick (nested terns rewrite cleanly):
//	tier := q.Tern(score >= 90, "A",
//	         q.Tern(score >= 80, "B",
//	          q.Tern(score >= 70, "C", "F")))
//
// The preprocessor's source-splicing also means side-effects fire
// only on the taken branch. q.Tern is a sugar around `if` — not a
// runtime helper.

// Tern returns ifTrue when cond evaluates to true, otherwise
// ifFalse. Despite the eager-looking call form, only the matching
// branch is evaluated — the preprocessor rewrites every call site
// at compile time. The runtime body is unreachable in a successful
// build.
func Tern[T any](cond bool, ifTrue, ifFalse T) T {
	panicUnrewritten("q.Tern")
	var zero T
	return zero
}
