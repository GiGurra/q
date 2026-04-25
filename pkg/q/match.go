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

// Case constructs one value→result arm of a q.Match.
//
//	q.Match(c, q.Case(Red, "warm"), q.Case(Blue, "cool"))
func Case[V, R any](value V, result R) MatchCase[V, R] {
	panicUnrewritten("q.Case")
	return MatchCase[V, R]{}
}

// Default constructs the catch-all arm of a q.Match. Up to one
// q.Default per q.Match call. When present, the typecheck pass
// skips the missing-constants coverage check (the catch-all covers
// anything missing).
//
//	q.Match(c, q.Case(Red, "warm"), q.Default("unknown"))
func Default[V comparable, R any](result R) MatchCase[V, R] {
	panicUnrewritten("q.Default")
	return MatchCase[V, R]{isDefault: true}
}
