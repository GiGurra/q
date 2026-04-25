package q

// gen.go — package-level "directives" the preprocessor uses to
// synthesize companion method files. Written by the user as
//
//	var _ = q.GenStringer[Color]()
//	var _ = q.GenEnumJSONStrict[Color]()
//	var _ = q.GenEnumJSONLax[Status]()
//
// Each is a regular Go var declaration with a function-call
// initializer — valid Go that gopls / vet recognise. At preprocess
// time, the scanner detects these declarations, the typecheck pass
// resolves T's constants, and a separate file-synthesis pass writes
// a `_q_gen_*.go` companion alongside the user's package containing
// the appropriate method declarations. The rewriter additionally
// replaces each directive's call with `struct{}{}` so the var has
// a runtime-no-op initializer (rather than a panic).
//
// If the preprocessor is not active, the var initialization runs the
// panicUnrewritten body — same loud-failure principle as every
// other q.* helper. The toolexec link gate still catches the missing
// preprocessor at link time before the var decl runs.

// GenMarker is the value type the Gen* directives return. It carries
// no runtime data — its only purpose is to keep `var _ = q.GenX[T]()`
// type-checkable.
type GenMarker struct{}

// GenStringer synthesizes a `func (T) String() string` method on T
// using q.EnumName as the underlying lookup. T must have constants
// declared in its home package (same restriction as q.EnumValues).
//
//	type Color int
//	const (Red Color = iota; Green; Blue)
//	var _ = q.GenStringer[Color]()
//	// → companion file declares `func (c Color) String() string`
//
// String() returns the constant identifier name on a known value, or
// the empty string on unknown values. Pair with q.EnumValid at
// canonical sites if you need to detect unknowns.
func GenStringer[T comparable]() GenMarker {
	panicUnrewritten("q.GenStringer")
	return GenMarker{}
}

// GenEnumJSONStrict synthesizes name-based JSON marshallers that
// **error on unknown values**. Use when wire drift should crash
// loudly — newer producers introducing values your service doesn't
// know about will fail to deserialize. Compatible with q.Exhaustive
// (the type is closed: every value parsed in must be a declared
// constant).
//
//	type Color int
//	const (Red Color = iota; Green; Blue)
//	var _ = q.GenEnumJSONStrict[Color]()
//	// → MarshalJSON encodes "Red" / "Green" / "Blue"
//	// → UnmarshalJSON errors on `"Magenta"`
//
// Unknown names on unmarshal: returns an error wrapping
// q.ErrEnumUnknown. Marshal: every reachable value at runtime should
// already be a declared constant (since UnmarshalJSON would have
// rejected anything else); if a manually-cast Color(99) reaches
// MarshalJSON, the helper returns the unknown error.
func GenEnumJSONStrict[T comparable]() GenMarker {
	panicUnrewritten("q.GenEnumJSONStrict")
	return GenMarker{}
}

// GenEnumJSONLax synthesizes JSON marshallers that **preserve
// unknown values** for forward-compat. Wire format is the underlying
// type:
//
//   - For string-backed T: marshal/unmarshal as the underlying
//     string. Any string is accepted on unmarshal. Marshal emits
//     the underlying string verbatim.
//   - For int-backed T: marshal/unmarshal as the underlying int. Any
//     int is accepted; the wire value is preserved.
//
// Use when forward-compat matters more than canonical-shape:
// services receiving values your code doesn't yet understand will
// preserve them through round-trip. Combine with q.Exhaustive's
// `default:` clause to handle the genuinely-unknown values
// explicitly.
//
//	type Status string
//	const (Pending Status = "pending"; Done Status = "done")
//	var _ = q.GenEnumJSONLax[Status]()
//	// → Status("future_value") survives unmarshal+marshal unchanged
func GenEnumJSONLax[T comparable]() GenMarker {
	panicUnrewritten("q.GenEnumJSONLax")
	return GenMarker{}
}
