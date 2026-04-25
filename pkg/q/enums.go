package q

// enums.go — compile-time helpers for the de-facto Go enum pattern
// (`type X int; const A, B, C X = iota, …`). All bodies panic; the
// preprocessor rewrites every call site to a literal slice / switch
// expression at compile time, with the constant set discovered by
// scanning the package for `*types.Const` declarations of T.
//
// Same-package enums only — cross-package T (e.g. `q.EnumName[other.Color](v)`)
// surfaces a diagnostic asking the user to declare a thin local
// wrapper. A future revision can lift this restriction by emitting a
// qualified identifier; today the rewriter only writes unqualified
// names.

import "errors"

// ErrEnumUnknown is wrapped (via %w) into the bubble produced by
// q.EnumParse when the input string doesn't match any known constant
// of the target enum type. Callers can errors.Is the resulting error
// against this sentinel to identify the failure mode.
var ErrEnumUnknown = errors.New("q: unknown enum value")

// EnumValues returns every constant of type T declared in T's package,
// in source declaration order. Rewritten to a literal slice expression
// at compile time.
//
// Example:
//
//	type Color int
//	const (Red Color = iota; Green; Blue)
//
//	colors := q.EnumValues[Color]()  // []Color{Red, Green, Blue}
func EnumValues[T comparable]() []T {
	panicUnrewritten("q.EnumValues")
	return nil
}

// EnumNames returns the identifier names of every constant of type T,
// in source declaration order. Rewritten to a literal slice of
// strings.
//
//	names := q.EnumNames[Color]()  // []string{"Red", "Green", "Blue"}
func EnumNames[T comparable]() []string {
	panicUnrewritten("q.EnumNames")
	return nil
}

// EnumName returns the identifier name corresponding to v, or "" when
// v is not a known constant of T. Rewritten to an inline switch
// expression.
//
//	q.EnumName[Color](Green)  // "Green"
func EnumName[T comparable](v T) string {
	panicUnrewritten("q.EnumName")
	return ""
}

// EnumParse converts s into the corresponding T constant, or
// (zero, q.ErrEnumUnknown wrapped with the input) when s names no
// constant of T. Rewritten to an inline switch expression.
//
//	c, err := q.EnumParse[Color]("Green")  // Green, nil
//	_, err = q.EnumParse[Color]("Pink")    // errors.Is(err, q.ErrEnumUnknown) == true
func EnumParse[T comparable](s string) (T, error) {
	panicUnrewritten("q.EnumParse")
	var zero T
	return zero, nil
}

// EnumValid reports whether v matches one of T's constants. Rewritten
// to an inline switch expression.
//
//	q.EnumValid[Color](Green)         // true
//	q.EnumValid[Color](Color(99))     // false
func EnumValid[T comparable](v T) bool {
	panicUnrewritten("q.EnumValid")
	return false
}

// EnumOrdinal returns v's 0-based position among T's constants in
// declaration order, or -1 when v is not a known constant. Rewritten
// to an inline switch expression.
//
//	q.EnumOrdinal[Color](Green)  // 1
func EnumOrdinal[T comparable](v T) int {
	panicUnrewritten("q.EnumOrdinal")
	return -1
}

// Exhaustive marks a `switch` as exhaustively covering every constant
// of T. The preprocessor recognises the shape
//
//	switch q.Exhaustive(v) {
//	case A: …
//	case B: …
//	}
//
// at compile time: if any constant of v's defined type is missing
// from the case clauses, the build fails with a diagnostic naming
// the missing constants.
//
// A `default:` clause does NOT replace coverage of the declared
// constants — it catches values outside the declared set
// (forward-compat with Lax-JSON-opted types, wire drift, or future
// constants a downstream service hasn't adopted yet). Every declared
// constant still needs its own case; default is additive and
// recommended for any type that can carry unknown values.
//
// The wrapper is removed at rewrite time, so the runtime code is a
// plain `switch v { … }`. Legal only as the tag of a switch
// statement; any other position is a build error.
//
// Anything declared via `const … T = …` in T's home package counts
// as a constant. Cross-package T is rejected (the rewriter would
// otherwise need to write qualified case names — declare a thin
// wrapper in the enum's home package).
func Exhaustive[T any](v T) T {
	panicUnrewritten("q.Exhaustive")
	return v
}
