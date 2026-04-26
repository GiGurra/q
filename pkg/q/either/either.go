// Package either ships the Scala-flavoured 2-arm sum type:
// either.Either[L, R] holds exactly one of L (the "left" arm) or R
// (the "right" arm). By convention the left arm carries the failure /
// alternative case and the right arm carries the success / primary
// case; right-biased operations (Map / FlatMap) reflect that.
//
// Either composes cleanly with the q package:
//
//   - q.Match + q.OnType / q.Default dispatches by tag, with
//     coverage checked at build (each variant must have an arm or
//     the call must include q.Default).
//   - q.Exhaustive on a type switch over .Value enforces statement-
//     level coverage.
//   - either.Fold is the Scala-style sibling for the value-returning
//     2-arm dispatch; identical to q.Match for two arms but reads
//     "Either"-flavored.
//
// The map-style operations (Fold / Map / FlatMap) live in this
// subpackage because pkg/q already exposes q.Map / q.FlatMap / q.Fold
// for slices and maps; sharing the names would shadow.
//
// Construction:
//
//	r := either.Right[Error, Response](Response{Code: 200})
//	e := either.Left[Error, Response](Error{Msg: "bad request"})
//
//	// Or via the type-arg-only constructor when the Either type has
//	// a name (often the most readable shape):
//	type Result = either.Either[Error, Response]
//	r := either.AsEither[Result](Response{Code: 200})
//	e := either.AsEither[Result](Error{Msg: "bad request"})
//
// q.AsOneOf also constructs Either values — Either is structurally a
// q.OneOf2 — but the named constructors above read more clearly at
// the call site.
package either


// Either is the binary discriminated union: exactly one of L (the
// "left" arm, Tag=1) or R (the "right" arm, Tag=2) is set.
//
// Direct construction (Either{Tag: …, Value: …}) bypasses arm
// validation; always go through the named constructors (Left, Right,
// AsEither). The Tag and Value fields are exported only because the
// q preprocessor must construct instances at the user's call site.
type Either[L, R any] struct {
	Tag   uint8
	Value any
}

// Left wraps l as the left arm (Tag=1). When neither type parameter
// can be inferred from a single value, both must be supplied
// explicitly: either.Left[Error, Response](err).
func Left[L, R any](l L) Either[L, R] {
	return Either[L, R]{Tag: 1, Value: l}
}

// Right wraps r as the right arm (Tag=2). When neither type
// parameter can be inferred from a single value, both must be
// supplied explicitly: either.Right[Error, Response](resp).
func Right[L, R any](r R) Either[L, R] {
	return Either[L, R]{Tag: 2, Value: r}
}

// AsEither wraps v as either the left or the right arm of T,
// depending on v's type. T must be (or be a defined type whose
// underlying type is) either.Either[L, R]. The q preprocessor
// validates v's type against T's arms and rewrites the call to the
// composite-literal shape with the correct Tag — same machinery as
// q.AsOneOf.
//
// Build-time errors: T isn't an Either-derived type; v's type isn't
// L or R; L and R are identical (the variant would be ambiguous).
func AsEither[T any](v any) T {
	panic("q: either.AsEither call site was not rewritten by the preprocessor")
}

// IsLeft reports whether the left arm is set.
func (e Either[L, R]) IsLeft() bool { return e.Tag == 1 }

// IsRight reports whether the right arm is set.
func (e Either[L, R]) IsRight() bool { return e.Tag == 2 }

// LeftOk returns the left value and a flag indicating whether it was
// set. Use this when you want to handle the left arm without
// reaching for q.Match / Fold.
func (e Either[L, R]) LeftOk() (L, bool) {
	if e.Tag != 1 {
		var zero L
		return zero, false
	}
	v, _ := e.Value.(L)
	return v, true
}

// RightOk returns the right value and a flag indicating whether it
// was set.
func (e Either[L, R]) RightOk() (R, bool) {
	if e.Tag != 2 {
		var zero R
		return zero, false
	}
	v, _ := e.Value.(R)
	return v, true
}

// Fold applies onLeft or onRight depending on which arm is set, and
// returns the result. Equivalent to q.Match with q.OnType arms over
// L and R; reads "Either"-flavored.
func Fold[L, R, T any](e Either[L, R], onLeft func(L) T, onRight func(R) T) T {
	if e.Tag == 1 {
		v, _ := e.Value.(L)
		return onLeft(v)
	}
	v, _ := e.Value.(R)
	return onRight(v)
}

// Map applies f to the right arm, leaving the left arm untouched.
// Right-biased (the Scala convention): a left-arm Either passes
// through unchanged.
func Map[L, R, R2 any](e Either[L, R], f func(R) R2) Either[L, R2] {
	if e.Tag == 1 {
		return Either[L, R2]{Tag: 1, Value: e.Value}
	}
	v, _ := e.Value.(R)
	return Either[L, R2]{Tag: 2, Value: f(v)}
}

// FlatMap is Map's bind sibling: f's result is itself an Either, and
// FlatMap collapses the nested Either into a single layer. Lefts
// pass through; a right value is fed into f and its returned Either
// becomes the result.
func FlatMap[L, R, R2 any](e Either[L, R], f func(R) Either[L, R2]) Either[L, R2] {
	if e.Tag == 1 {
		return Either[L, R2]{Tag: 1, Value: e.Value}
	}
	v, _ := e.Value.(R)
	return f(v)
}

// MapLeft applies f to the left arm, leaving the right arm
// untouched. The mirror of Map for the failure / alternative side.
func MapLeft[L, R, L2 any](e Either[L, R], f func(L) L2) Either[L2, R] {
	if e.Tag == 2 {
		return Either[L2, R]{Tag: 2, Value: e.Value}
	}
	v, _ := e.Value.(L)
	return Either[L2, R]{Tag: 1, Value: f(v)}
}

// GetOrElse returns the right value when set, or fallback otherwise.
func GetOrElse[L, R any](e Either[L, R], fallback R) R {
	if e.Tag != 2 {
		return fallback
	}
	v, _ := e.Value.(R)
	return v
}

// Swap flips the left and right arms — Either[L, R] becomes
// Either[R, L]. Useful when you want to reuse a right-biased pipeline
// on the left arm: swap, run the pipeline, swap back.
func Swap[L, R any](e Either[L, R]) Either[R, L] {
	if e.Tag == 1 {
		return Either[R, L]{Tag: 2, Value: e.Value}
	}
	return Either[R, L]{Tag: 1, Value: e.Value}
}
