package q

// from.go — q.ConvertTo: Chimney-style auto-derived struct
// conversions.
//
// `q.ConvertTo[Target](src, opts...)` is rewritten at compile time
// into a struct literal that copies matching exported fields from
// src and applies any manual overrides. No runtime reflection.
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
// "ConvertTo" reads as the natural verb-direction phrase: "convert
// to UserDTO from user".

// ConvertTo produces a Target value populated from src's matching
// exported fields, with optional per-field overrides supplied via
// opts. Reads as: "Convert to Target from src".
//
//	type User    struct { ID int; First, Last, Email string; Internal bool }
//	type UserDTO struct { ID int; FullName, Email, Source string }
//	dto := q.ConvertTo[UserDTO](user,
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
func ConvertTo[Target, Source any](src Source, opts ...ConvertOption) Target {
	panicUnrewritten("q.ConvertTo")
	var zero Target
	return zero
}

// ConvertOption is the sealed marker type for q.ConvertTo overrides.
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
//	q.ConvertTo[UserDTO](u, q.Set(UserDTO{}.Source, "v1"))
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
// surrounding q.ConvertTo call's source type. The function is
// invoked with the source value at the rewritten call site —
// closures capture surrounding scope normally.
//
//	q.ConvertTo[UserDTO](u, q.SetFn(UserDTO{}.FullName, func(u User) string {
//	    return u.First + " " + u.Last
//	}))
//	// → UserDTO{..., FullName: (func(u User) string { ... })(u)}
//
// For fallible derivations (calling a database, a remote service,
// parsing input that might be invalid), use q.SetFnE inside
// q.ConvertToE instead.
func SetFn[Source, V any](targetField V, fn func(Source) V) ConvertOption {
	panicUnrewritten("q.SetFn")
	return ConvertOption{}
}

// ConvertToE is the fallible sibling of q.ConvertTo: it returns
// (Target, error) instead of just Target, so per-field overrides may
// fail and short-circuit the whole conversion. Use it when a field
// derivation calls something that can fail — fetching from a database,
// hitting a remote service, parsing an upstream blob — and you'd
// rather bubble the error than recover or panic.
//
//	dto, err := q.ConvertToE[UserDTO](u,
//	    q.SetFnE(UserDTO{}.Email, func(u User) (string, error) {
//	        return lookupEmail(ctx, u.ID)
//	    }),
//	    q.Set(UserDTO{}.Source, "v1"),
//	)
//	if err != nil { return err }
//
// All standard overrides (q.Set, q.SetFn) work in q.ConvertToE too.
// The error path only fires when a q.SetFnE override returns a
// non-nil error; the first such error wins, in target-field
// declaration order. Pair with q.Try for the bubble-flat shape:
//
//	dto := q.Try(q.ConvertToE[UserDTO](u, q.SetFnE(...)))
//
// q.SetFnE is rejected by the rewriter if used inside q.ConvertTo
// (no error slot to bubble to).
//
// The runtime body is unreachable in a successful build.
func ConvertToE[Target, Source any](src Source, opts ...ConvertOption) (Target, error) {
	panicUnrewritten("q.ConvertToE")
	var zero Target
	return zero, nil
}

// SetFnE is the fallible variant of q.SetFn: the function returns
// (V, error). A non-nil error short-circuits the whole conversion
// and propagates out of q.ConvertToE.
//
// Only valid inside q.ConvertToE (the bare q.ConvertTo has no error
// slot). The rewriter rejects q.SetFnE inside q.ConvertTo with a
// build-time diagnostic.
//
//	q.SetFnE(UserDTO{}.Email, func(u User) (string, error) {
//	    return db.LookupEmail(u.ID)
//	})
func SetFnE[Source, V any](targetField V, fn func(Source) (V, error)) ConvertOption {
	panicUnrewritten("q.SetFnE")
	return ConvertOption{}
}
