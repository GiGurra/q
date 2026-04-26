package q

// atom.go — q.Atom: typed-string atoms with type-derived values.
//
// Inspired by Erlang atoms — symbolic constants whose identity IS
// their name — adapted to Go's type system.
//
// The shape:
//
//	type Pending q.Atom    // user declares each atom as a distinct type
//	type Done    q.Atom
//	type Failed  q.Atom
//
//	// In package "github.com/me/proj":
//	p := q.A[Pending]()    // p is Pending("github.com/me/proj.Pending")
//	d := q.A[Done]()       // d is Done("github.com/me/proj.Done")
//
// Properties.
//
//   - Each atom is its own TYPE. The Go type system protects against
//     mixing: you can't assign Pending to Done or pass one where the
//     other is expected.
//   - The value is the declaring package's import path + the type's
//     bare name (canonical form via go/types) — globally unique
//     across the binary, no central registry needed.
//   - Equality across instances of the same atom type is plain string
//     equality (free).
//   - The preprocessor rewrites q.A[T]() to T("<importPath>.<TypeName>"),
//     so each call site folds to a typed-string constant — zero
//     runtime cost. The runtime body is link-gated.
//
// Compared to Erlang atoms, this gives you the type-distinct value
// (Erlang atoms are all one type) but keeps the "no central
// declaration" win: each atom needs only `type X q.Atom` at its
// point of use, no const block, no shared type list to maintain.
//
// JSON / wire formats: the qualified value is the load-bearing
// identity, but it's rarely the right thing to put on a wire. See
// docs/api/atom.md for the rationale and the recommended patterns
// (custom MarshalJSON per atom type, or closed-set enums via
// q.GenEnumJSONStrict / q.GenEnumJSONLax for wire-bound enums).

// Atom is the parent typed-string type from which user-declared atom
// types derive: `type MyAtom q.Atom`. The underlying string value of
// each atom is its declaring package's import path + the type's bare
// name, set by q.A[T]() via the preprocessor rewrite.
type Atom string

// A summons an instance of atom type T. The preprocessor rewrites
// each call site to T("<importPath>.<TypeName>") — a typed-string
// cast that folds at compile time. The runtime body is unreachable
// in a successful build.
//
// Example:
//
//	type Pending q.Atom
//
//	if status == q.A[Pending]() {
//	    // …
//	}
//
// T's underlying type must be string (i.e. T = q.Atom, or T = string,
// or any type derived from one of those). The preprocessor and Go's
// own type checker enforce this together — q.A's `~string` constraint
// rejects non-string types, and the rewritten T("name") cast fails
// compilation if T isn't string-compatible.
func A[T ~string]() T {
	panicUnrewritten("q.A")
	var zero T
	return zero
}

// AtomOf is q.A's case-friendly sibling: it returns the value cast as
// the parent q.Atom type, so it slots into a `switch a q.Atom { case … }`
// directly without the boilerplate q.Atom(...) wrap. The preprocessor
// rewrites each call site to q.Atom("<importPath>.<TypeName>").
//
// Compare:
//
//	case q.Atom(q.A[Done]()):  // verbose — wrap to bridge typed atom -> q.Atom
//	case q.AtomOf[Done]():     // identical, less noise
//
// Use q.A when you want T-typed values that the type system protects
// (function params typed `func(p Pending)`, struct fields, returns).
// Use q.AtomOf when you're dealing with the atom in its erased
// q.Atom form — most commonly in switch cases on a q.Atom-typed value.
func AtomOf[T ~string]() Atom {
	panicUnrewritten("q.AtomOf")
	return ""
}
