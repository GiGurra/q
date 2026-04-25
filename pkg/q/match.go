package q

// match.go — value-returning switch as an expression. Scala / Rust /
// most modern languages have this; Go's `switch` is statement-only,
// forcing IIFE wrapping or a temp var when you actually want
// "compute a value based on cases". `q.Match` ships that pattern.
//
//	desc := q.Match(c,
//	    q.Case(Red,        "warm"),
//	    q.Case(Green,      "natural"),
//	    q.Case(Blue,       "cool"),
//	    q.Case(c == magic, "magic"), // bool expression — predicate match
//	    q.Default("unknown"),
//	)
//
// Rewrites at compile time to a value-returning expression. When
// every arm is a value-equality match (the cond's type matches the
// matched value's type), the output is an IIFE-wrapped Go `switch`.
// As soon as any arm is a predicate (bool-typed cond), the output
// flips to an if/else-if chain — Go's switch can't carry predicate
// cases.
//
// Coverage check: when the matched value's type is an enum (a
// defined type with declared constants) AND no q.Default arm is
// provided AND every arm is a value match, the typecheck pass
// validates that every constant has a case — same coverage rule
// q.Exhaustive enforces. Predicate-arm forms always require a
// q.Default since predicates can't be statically checked.
//
// All q.Case / q.Default arguments are SOURCE-REWRITTEN: the
// preprocessor extracts their literal source text and re-emits it
// inside the rewritten if/case body, so neither cond nor result is
// evaluated as a Go argument at the q.Case call site. The arm only
// runs when it matches — even for q.Case(0, expensive()).

// MatchArm is one arm of a q.Match. Constructed via q.Case /
// q.Default; the preprocessor consumes them at compile time, so the
// runtime body is not reached in a successful build.
//
// R is a phantom type parameter — the compile-time-consumed nature
// of the arm means the result isn't actually needed at runtime, but
// the type parameter must still flow through for q.Match to type-
// check. The `[0]R` zero-size array consumes the type parameter
// without taking any space.
type MatchArm[R any] struct {
	_         [0]R
	isDefault bool
}

// Match folds to a value-returning switch (or if/else-if chain when
// any arm is a predicate). value is dispatched against each q.Case's
// first argument; the matching arm's result is returned. q.Default
// catches anything not covered.
//
// value is typed `any` — the preprocessor recovers the actual type
// via go/types and validates each arm's cond against it. Outside the
// preprocessor, `any` typing means q.Match is freely callable from
// Go's typechecker (you can pass a value of any type), with mistakes
// surfacing as q-pass diagnostics rather than gopls errors.
func Match[R any](value any, arms ...MatchArm[R]) R {
	panicUnrewritten("q.Match")
	var zero R
	return zero
}

// Case is the universal arm. cond decides whether this arm fires;
// result is what the arm returns when it does.
//
// The preprocessor inspects cond's type via go/types and dispatches:
//
//   cond is matched-value-typed   → value match (v == cond)
//   cond is bool                  → predicate match (if cond)
//   cond is func() V              → lazy value match (v == cond())
//   cond is func() bool           → lazy predicate (if cond())
//
// Both cond and result are source-rewritten regardless of type — the
// preprocessor extracts their literal source text and re-emits it
// inside the rewritten if/case body, so expressions only run on the
// matching arm. `q.Case(0, expensive())` only calls expensive()
// when v==0.
//
// Examples:
//
//	q.Case(Red, "warm")            // value match (cond is the enum type)
//	q.Case(n > 0, "positive")      // predicate (cond is bool)
//	q.Case(getThreshold(), "x")    // value match, lazy via source rewrite
//	q.Case(predFn, "x")            // lazy predicate; predFn is func()bool
//	q.Case(0, expensive())         // lazy result via source rewrite
//
// To pass a result that is itself a function value (rather than the
// function's call result), write the call explicitly:
// `q.Case(0, makeFallback())` not `q.Case(0, makeFallback)`.
func Case[R any](cond any, result R) MatchArm[R] {
	panicUnrewritten("q.Case")
	return MatchArm[R]{}
}

// Default is the catch-all arm. Up to one q.Default per q.Match
// call. When present, the typecheck pass skips the missing-constants
// coverage check (the catch-all covers anything missing).
//
//	q.Match(c, q.Case(Red, "warm"), q.Default("unknown"))
func Default[R any](result R) MatchArm[R] {
	panicUnrewritten("q.Default")
	return MatchArm[R]{isDefault: true}
}
