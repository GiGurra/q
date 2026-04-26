package preprocessor

// atom.go — rewriter for q.A[T]() typed-string atoms.
//
// q.A[T]() rewrites in-place to T("<bare-name-of-T>") — a typed
// string conversion that Go folds to a constant at compile time.
//
// T can be:
//   - a bare identifier:        q.A[Pending]    -> Pending("Pending")
//   - a qualified identifier:   q.A[pkg.Atom]   -> pkg.Atom("Atom")
//
// Anonymous types (`q.A[struct{...}]`) and other non-named-type
// arguments are passed through verbatim — Go's own type checker
// rejects them via the `~string` constraint on q.A.

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
)

// resolveAtom validates the type argument for q.A[T]() and
// q.AtomOf[T](). Surfaces a diagnostic when T isn't a named type
// with string underlying — catches `q.A[string]()`, `q.A[int]()`,
// and other obvious misuse before the rewrite produces a Go cast
// that would either compile to nonsense or fail with a less
// directed error.
//
// Strict atom-chain validation (verifying T transitively derives
// from q.Atom in its TypeSpec definition) is a future extension —
// see TODO #91 follow-up. For now we accept any named string-typed
// type, which is the same set Go's `~string` constraint accepts.
func resolveAtom(fset *token.FileSet, sc *qSubCall, info *types.Info) (Diagnostic, bool) {
	if sc.AsType == nil {
		return Diagnostic{}, false
	}
	tv, ok := info.Types[sc.AsType]
	if !ok || tv.Type == nil {
		return Diagnostic{}, false
	}
	t := tv.Type
	pos := fset.Position(sc.OuterCall.Pos())
	entryName := "A"
	if sc.Family == familyAtomOf {
		entryName = "AtomOf"
	}
	named, ok := t.(*types.Named)
	if !ok {
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q.%s[T]: T must be a named type derived from q.Atom (e.g. `type Pending q.Atom`); got %s", entryName, t.String()),
		}, true
	}
	basic, ok := named.Underlying().(*types.Basic)
	if !ok || basic.Kind() != types.String {
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q.%s[T]: T's underlying type must be string (q.Atom is `type Atom string`); got %s with underlying %s", entryName, named.String(), named.Underlying().String()),
		}, true
	}
	// Populate the qualified name for the rewriter. Format:
	// "<package-import-path>.<bare-type-name>". Globally unique within
	// a binary, so two atoms with the same bare name in different
	// packages compare unequal at the q.Atom (string) level.
	obj := named.Obj()
	if obj != nil && obj.Pkg() != nil {
		sc.AtomQualifiedName = obj.Pkg().Path() + "." + obj.Name()
	} else if obj != nil {
		sc.AtomQualifiedName = obj.Name()
	}
	return Diagnostic{}, false
}

// buildAtomReplacement emits the typed-string cast for a q.A[T]() or
// q.AtomOf[T]() call.
//
//	q.A[T]()       -> T("<import-path>.<bare-type-name>")
//	q.AtomOf[T]()  -> q.Atom("<import-path>.<bare-type-name>")
//
// The fully-qualified value is populated by the typecheck pass into
// sub.AtomQualifiedName. When typecheck is skipped (rewriter_test
// path), the rewriter falls back to the bare name from the AST so
// the output still parses; production builds always go through
// typecheck and get the fully-qualified value.
//
// The qualified value guarantees cross-package collision safety: two
// packages with the same bare type name (e.g. `type Status q.Atom`
// in both pkg A and pkg B) produce distinct atom strings and so
// compare unequal at the q.Atom (parent) type level.
func buildAtomReplacement(fset *token.FileSet, src []byte, sub qSubCall, alias string) string {
	var bare, full string
	switch t := sub.AsType.(type) {
	case *ast.Ident:
		bare = t.Name
		full = t.Name
	case *ast.SelectorExpr:
		bare = t.Sel.Name
		full = exprText(fset, src, t)
	default:
		return `/* q.A: unsupported type argument */ ""`
	}
	value := sub.AtomQualifiedName
	if value == "" {
		// Typecheck was skipped (rewriter_test path) — fall back to
		// the bare name. Output still parses; the cross-package
		// collision guarantee only applies to production builds where
		// typecheck runs.
		value = bare
	}
	if sub.Family == familyAtomOf {
		return fmt.Sprintf("%s.Atom(%q)", alias, value)
	}
	return fmt.Sprintf("%s(%q)", full, value)
}
