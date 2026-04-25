package q

// match.go — value-returning switch as an expression. Scala / Rust /
// most modern languages have this; Go's `switch` is statement-only,
// forcing IIFE wrapping or a temp var when you actually want
// "compute a value based on cases". `q.Match` ships that pattern.
//
//	desc := q.Match(c,
//	    q.Case(Red,   "warm"),
//	    q.Case(Green, "natural"),
//	    q.Case(Blue,  "cool"),
//	)
//
// Rewrites at compile time to an IIFE-wrapped switch returning the
// chosen result. When the match value's type is an enum (a defined
// type with declared constants) AND no `q.Default` arm is provided,
// the typecheck pass validates that every constant has a case —
// same coverage rule q.Exhaustive enforces, but for a value-
// producing form.
//
// `q.Default(result)` opts out of the coverage check and provides
// the result for any value not covered by an explicit case.

// MatchCase carries one arm of a q.Match. Constructed via q.Case or
// q.Default; the rewriter consumes them at compile time, so the
// runtime body is not reached in a successful build.
type MatchCase[V, R any] struct {
	value     V
	result    R
	isDefault bool
}

// Match folds to a value-returning switch. value is dispatched
// against each q.Case's first argument; the matching case's result
// is returned. q.Default catches anything not covered.
//
// V must be comparable (Go switch requirement). When V is a defined
// type with declared constants and no q.Default is supplied, the
// build fails if any constant lacks a case — symmetric with
// q.Exhaustive's compile-time coverage check.
func Match[V comparable, R any](value V, cases ...MatchCase[V, R]) R {
	panicUnrewritten("q.Match")
	var zero R
	return zero
}

// Case constructs one value→result arm of a q.Match. The result
// expression is evaluated EAGERLY at the q.Match call site (Go's
// argument-evaluation rules), so use q.CaseFn for expensive or
// side-effecting computations that should only run when this arm
// matches.
//
//	q.Match(c, q.Case(Red, "warm"), q.Case(Blue, "cool"))
func Case[V, R any](value V, result R) MatchCase[V, R] {
	panicUnrewritten("q.Case")
	return MatchCase[V, R]{}
}

// CaseFn constructs a value→result arm whose result is produced
// LAZILY by calling fn — only when this arm matches. Useful when
// the result is expensive to compute or has side effects you only
// want on the matching branch.
//
//	q.Match(c,
//	    q.Case(Red, "warm"),
//	    q.CaseFn(Green, func() string { return loadFromDB() }),
//	    q.Case(Blue, "cool"),
//	)
//
// Mixes freely with q.Case in the same q.Match.
func CaseFn[V, R any](value V, fn func() R) MatchCase[V, R] {
	panicUnrewritten("q.CaseFn")
	return MatchCase[V, R]{}
}

// Default constructs the catch-all arm of a q.Match. The result is
// evaluated EAGERLY at the call site. Use q.DefaultFn for lazy
// catch-all computation. Up to one q.Default / q.DefaultFn per
// q.Match call. When present, the typecheck pass skips the
// missing-constants coverage check (the catch-all covers anything
// missing).
//
//	q.Match(c, q.Case(Red, "warm"), q.Default("unknown"))
func Default[V comparable, R any](result R) MatchCase[V, R] {
	panicUnrewritten("q.Default")
	return MatchCase[V, R]{isDefault: true}
}

// DefaultFn is the lazy form of q.Default — fn runs only when the
// match falls through to the catch-all. Symmetric with q.CaseFn.
//
//	q.Match(c, q.Case(Red, "warm"),
//	    q.DefaultFn(func() string { return logUnknown(c); return "unknown" }),
//	)
func DefaultFn[V comparable, R any](fn func() R) MatchCase[V, R] {
	panicUnrewritten("q.DefaultFn")
	return MatchCase[V, R]{isDefault: true}
}
