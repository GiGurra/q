package q

// oneof.go — discriminated unions ("sum types").
//
// Go shipped without sum types — the headline rejected proposal.
// q.OneOfN gives a real, type-safe discriminated union: declare the
// arms, construct via q.AsOneOf, dispatch via q.Match + q.OnType.
//
//	type Pending struct{}
//	type Done    struct{ At time.Time }
//	type Failed  struct{ Err error }
//
//	type Status q.OneOf3[Pending, Done, Failed]
//
//	s := q.AsOneOf[Status](Done{At: time.Now()})
//
//	// Payloadless / discarding the payload — q.Case reads cleanest:
//	desc := q.Match(s,
//	    q.Case(Pending{}, "waiting"),
//	    q.Case(Done{},    "done"),
//	    q.Case(Failed{},  "failed"),
//	)
//
//	// Need the typed variant's fields — q.OnType binds the payload:
//	desc := q.Match(s,
//	    q.Case(Pending{}, "waiting"),
//	    q.OnType(func(d Done) string   { return "done at " + d.At.String() }),
//	    q.OnType(func(f Failed) string { return "failed: " + f.Err.Error() }),
//	)
//
// Properties.
//
//   - Each OneOfN[…] is a real generic struct. User code typically
//     defines a named alias — `type Status q.OneOfN[…]` — so the type
//     reads at the use site instead of leaking the OneOfN spelling.
//   - Variants are positional. The 1-based position of the variant
//     becomes the runtime Tag; q.AsOneOf and q.OnType both resolve the
//     position from the type-arg list at compile time.
//   - q.AsOneOf[T](v) rewrites in-place to `T{Tag: <pos>, Value: v}`
//     after the typecheck pass validates v's type matches one of T's
//     arms. No reflection at runtime.
//   - q.OnType integrates with q.Match. When the matched value's type
//     is a OneOfN-derived sum, the typecheck pass enforces coverage:
//     every variant must have a matching q.OnType arm OR the match
//     must include a q.Default catch-all.
//   - The runtime cost per construction is one `any` interface box
//     (the wrapped variant value). Specialised non-`any` storage for
//     primitive variants is an open optimisation — see TODO #74.
//
// Tag is exported only because the preprocessor must construct
// instances at the user's call site (composite literals can't reach
// unexported fields across package boundaries). Direct construction
// — `Status{Tag: 1, Value: foo}` — bypasses variant validation; the
// resulting union is well-typed but may fail at q.OnType dispatch
// time. Always go through q.AsOneOf.

// OneOf2 is the binary discriminated union over arm types A and B.
// Tag is 1 for A, 2 for B (0 for the zero / unconstructed value).
//
// Don't construct directly. q.AsOneOf[T](v) is the only sanctioned
// surface — the preprocessor validates the variant and emits the
// composite-literal shape with the right Tag.
type OneOf2[A, B any] struct {
	Tag   uint8
	Value any
}

// OneOf3 is the ternary discriminated union over arm types A, B, C.
// Tag is 1 for A, 2 for B, 3 for C.
type OneOf3[A, B, C any] struct {
	Tag   uint8
	Value any
}

// OneOf4 is the 4-way discriminated union. See q.OneOf2 for details.
type OneOf4[A, B, C, D any] struct {
	Tag   uint8
	Value any
}

// OneOf5 is the 5-way discriminated union. See q.OneOf2 for details.
type OneOf5[A, B, C, D, E any] struct {
	Tag   uint8
	Value any
}

// OneOf6 is the 6-way discriminated union. See q.OneOf2 for details.
type OneOf6[A, B, C, D, E, F any] struct {
	Tag   uint8
	Value any
}

// AsOneOf wraps v into a sum type T whose underlying type is one of
// the q.OneOfN forms. The preprocessor validates v's type matches one
// of T's arm type-arguments, then rewrites the call site to
// `T{Tag: <position>, Value: v}` — a composite literal with the right
// 1-based tag.
//
// T must be a defined type whose underlying type is q.OneOfN[…];
// `q.AsOneOf[q.OneOf2[A,B]](a)` (no named alias) also works.
//
// Example:
//
//	type Status q.OneOf2[Pending, Done]
//	s := q.AsOneOf[Status](Done{...})  // → Status{Tag: 2, Value: Done{...}}
//
// Build-time errors:
//   - T isn't a OneOfN-derived type → diagnostic.
//   - v's type isn't identical to any of T's arm types → diagnostic
//     listing the accepted arms.
//   - T has duplicate arm types (e.g. q.OneOf2[int, int]) → diagnostic
//     (the variant would be ambiguous).
func AsOneOf[T any](v any) T {
	panicUnrewritten("q.AsOneOf")
	var zero T
	return zero
}

// q.Exhaustive on a OneOfN-derived value
//
// Plain q.Match isn't the only dispatch site — q.Exhaustive enforces
// coverage on a statement-level type switch over the unwrapped value:
//
//	switch v := q.Exhaustive(s.Value).(type) {
//	case Pending: // payload-less variant
//	case Done:    fmt.Println(v.At)
//	case Failed:  fmt.Println(v.Err)
//	}
//
// The build fails if any variant is missing. `default:` opts out of
// the missing-case rule but doesn't substitute for covering declared
// variants — same semantics as the const-enum form.
//
// q.Exhaustive(s.Value) is valid Go to gopls (q.Exhaustive is the
// identity on type T); the .(type) assertion works because s.Value's
// static type is `any`. The typecheck pass spots the OneOfN ancestor
// of `s.Value` and walks the variant list to drive the coverage check.
//
// OnType is a q.Match arm that fires when the matched OneOfN-derived
// value's runtime variant is T. handler receives the unwrapped typed
// variant value. R is inferred from handler's return type.
//
// Only valid as an argument to q.Match — used anywhere else the
// runtime stub panics. The matched value's type MUST be a OneOfN-
// derived sum (a defined type whose underlying type is q.OneOfN[…]),
// or `q.OneOfN[…]` directly.
//
// Coverage check: when q.Match has q.OnType arms, every variant of
// the matched sum type must have an OnType arm OR a q.Default arm
// must cover the rest. Build fails otherwise. Variants may also be
// covered indirectly by q.Default; mixing is allowed.
//
// Example:
//
//	type Status q.OneOf3[Pending, Done, Failed]
//
//	desc := q.Match(s,
//	    q.OnType(func(p Pending) string { return "waiting" }),
//	    q.OnType(func(d Done) string    { return "done" }),
//	    q.OnType(func(f Failed) string  { return "failed" }),
//	)
//
// Mixing q.OnType with q.Case in the same q.Match is rejected — the
// dispatch shape is incompatible (OnType dispatches by Tag; Case by
// value-equality / predicate). Use one or the other.
func OnType[R, T any](handler func(T) R) MatchArm[R] {
	panicUnrewritten("q.OnType")
	return MatchArm[R]{}
}
