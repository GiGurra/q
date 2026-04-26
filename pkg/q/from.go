package q

// from.go — q.Convert: Chimney-style auto-derived struct conversions.
//
// `q.Convert[Target](src)` is rewritten at compile time into a struct
// literal that copies matching exported fields from src — no runtime
// reflection. Unmappable fields surface as build-time diagnostics
// with the per-field gap; gopls / IDEs see the original call as
// plain Go (one generic function with one inferred type param).
//
// The original Chimney sketch was a chain — `q.From(src).To[Target]()`
// — but Go forbids type parameters on methods, so we ship the
// type-arg-first single-function form. Reads as: "Convert <to
// Target> from src".
//
// v1 scope:
//
//   - Source and Target must both be struct types (named types with
//     a struct underlying are fine; aliases too). Pointers, scalars,
//     interfaces are rejected at scan time.
//   - Field matching is exported-name-only.
//   - Each (Source.F, Target.F) pair must satisfy
//     types.AssignableTo. No implicit lifting (int→int64, *T→T,
//     T→Option[T]) yet; those are follow-up work.
//   - Source fields with no counterpart on Target are silently
//     dropped (target-driven, like Chimney).
//   - Target fields with no source counterpart, or with mismatched
//     types, fail the build.
//   - No nested-struct recursion in v1: if Target.Inner is itself a
//     struct, the source must produce an assignable value of the
//     same struct type. A nested q.Convert call inside the source
//     expression is the workaround.

// Convert produces a Target value populated from src's exported
// fields. The preprocessor rewrites the call site to a struct literal
// — no runtime reflection, no closures, zero overhead. Field gaps
// surface as compile-time errors.
//
//	type User    struct { ID int; Name string; Internal bool }
//	type UserDTO struct { ID int; Name string }
//	dto := q.Convert[UserDTO](user)
//	// rewrites to: UserDTO{ID: user.ID, Name: user.Name}
//
// The runtime body is unreachable in a successful build.
func Convert[Target any, Source any](src Source) Target {
	panicUnrewritten("q.Convert")
	var zero Target
	return zero
}
