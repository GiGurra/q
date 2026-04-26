package q

// from.go — q.Convert: Chimney-style auto-derived struct conversions.
//
// `q.Convert[Target](src, opts...)` is rewritten at compile time into
// a struct literal that copies matching exported fields from src and
// applies any manual overrides. No runtime reflection.
//
// Resolution order, per target field (exported only):
//
//   1. Override — q.Set(Target{}.Field, value) or q.SetFn(Target{}.Field, fn)
//      supplies the value explicitly. Wins over auto-derivation.
//   2. Direct copy — same-named source field whose type is
//      types.AssignableTo the target field's type.
//   3. Nested derivation — same-named source field that is itself a
//      struct, recursively converted using the same algorithm. The
//      nested call inherits no overrides (overrides apply only to the
//      Target type at the top of the chain).
//   4. Diagnostic — target field has no source counterpart, no
//      assignable copy, no nested derivation. Build fails.
//
// Source fields with no Target counterpart are silently dropped
// (target-driven, like Chimney). Source/Target type cycles are
// detected and rejected.
//
// The override field reference is a typed Go selector expression:
//
//	q.Set(UserDTO{}.Source, "v1")        // not q.Set("Source", "v1")
//	q.SetFn(UserDTO{}.Email, fn)
//
// `UserDTO{}.Source` is a valid Go selector expression — it evaluates
// to the zero value at runtime, but Go type-checks the field
// reference at compile time. Rename UserDTO.Source → SourceTag and
// every override call site fails to compile. Strings would silently
// stale-mismatch — that's why we don't take them.
//
// Surface caveat: the original Chimney sketch was the chain form
// q.From(src).To[Target]() but Go forbids type parameters on
// methods, so we ship the type-arg-first single-function form.

// Convert produces a Target value populated from src's matching
// exported fields, with optional per-field overrides supplied via
// opts. Reads as: "Convert <to Target> from src".
//
//	type User    struct { ID int; First, Last, Email string; Internal bool }
//	type UserDTO struct { ID int; FullName, Email, Source string }
//	dto := q.Convert[UserDTO](user,
//	    q.Set(UserDTO{}.Source, "v1"),
//	    q.SetFn(UserDTO{}.Email, func(u User) string {
//	        return strings.ToLower(u.Email)
//	    }),
//	    q.SetFn(UserDTO{}.FullName, func(u User) string {
//	        return u.First + " " + u.Last
//	    }),
//	)
//
// The runtime body is unreachable in a successful build.
func Convert[Target, Source any](src Source, opts ...ConvertOption) Target {
	panicUnrewritten("q.Convert")
	var zero Target
	return zero
}

// ConvertOption is the sealed marker type for q.Convert overrides.
// Constructed only via q.Set / q.SetFn; the rewriter parses each
// option's call AST at compile time and erases the runtime call.
type ConvertOption struct {
	_ struct{} //nolint:unused // chain contract; the rewriter erases this
}

// Set overrides the value of targetField with value.
//
// targetField MUST be a Target{}.<FieldName> selector expression so
// the rewriter can extract the field path and so Go's own type-checker
// validates the field reference. The generic param V is unified with
// both the field's type and the override value's type — assignability
// is enforced by Go itself.
//
//	q.Convert[UserDTO](u, q.Set(UserDTO{}.Source, "v1"))
//	// → UserDTO{..., Source: "v1"}
//
//	// Refactor-safe: rename UserDTO.Source → SourceTag and Go's
//	// compiler flags this call site immediately.
func Set[V any](targetField V, value V) ConvertOption {
	panicUnrewritten("q.Set")
	return ConvertOption{}
}

// SetFn overrides the value of targetField with the result of fn(src).
//
// targetField MUST be a Target{}.<FieldName> selector expression
// (same shape as q.Set). The generic param V unifies the field's
// type with the function's return type; Source unifies with the
// surrounding q.Convert call's source type. The function is
// invoked with the source value at the rewritten call site —
// closures capture surrounding scope normally.
//
//	q.Convert[UserDTO](u, q.SetFn(UserDTO{}.FullName, func(u User) string {
//	    return u.First + " " + u.Last
//	}))
//	// → UserDTO{..., FullName: (func(u User) string { ... })(u)}
func SetFn[Source, V any](targetField V, fn func(Source) V) ConvertOption {
	panicUnrewritten("q.SetFn")
	return ConvertOption{}
}
