package q

// from.go — q.Convert: Chimney-style auto-derived struct conversions.
//
// `q.Convert[Target](src, opts...)` is rewritten at compile time into
// a struct literal that copies matching exported fields from src and
// applies any manual overrides. No runtime reflection.
//
// Resolution order, per target field (exported only):
//
//   1. Override — q.Set(field, value) or q.SetFn(field, fn) supplies
//      the value explicitly. Wins over auto-derivation.
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
// (target-driven, like Chimney). Recursive type cycles between
// Source and Target are detected and rejected.
//
// Surface caveat: the original Chimney sketch was the chain form
// q.From(src).To[Target]() but Go forbids type parameters on
// methods, so we ship the type-arg-first single-function form.

// Convert produces a Target value populated from src's matching
// exported fields, with optional per-field overrides supplied via
// opts. Reads as: "Convert <to Target> from src".
//
//	type User    struct { ID int; Name, Email string; Internal bool }
//	type UserDTO struct { ID int; Name string; Email string; Source string }
//	dto := q.Convert[UserDTO](user,
//	    q.Set("Source", "v1"),
//	    q.SetFn("Email", func(u User) string { return strings.ToLower(u.Email) }),
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

// Set overrides the value of targetField with value. targetField MUST
// be a string literal — the preprocessor validates the field name
// against Target's exported fields at compile time, and the value's
// type against the target field's declared type.
//
//	q.Convert[UserDTO](u, q.Set("Source", "v1"))
//	// → UserDTO{..., Source: "v1"}
func Set[V any](targetField string, value V) ConvertOption {
	panicUnrewritten("q.Set")
	return ConvertOption{}
}

// SetFn overrides the value of targetField with the result of fn(src).
// targetField MUST be a string literal. fn's signature must be
// `func(Source) V` where V is types.AssignableTo the target field's
// type. The function literal is invoked with the source value at the
// rewritten call site — closures capture surrounding scope normally.
//
//	q.Convert[UserDTO](u, q.SetFn("FullName", func(u User) string {
//	    return u.First + " " + u.Last
//	}))
//	// → UserDTO{..., FullName: (func(u User) string { ... })(u)}
func SetFn[Source, V any](targetField string, fn func(Source) V) ConvertOption {
	panicUnrewritten("q.SetFn")
	return ConvertOption{}
}
