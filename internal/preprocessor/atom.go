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
	return Diagnostic{}, false
}

// buildAtomReplacement emits the typed-string cast for a q.A[T]() or
// q.AtomOf[T]() call.
//
//	q.A[T]()       -> T("name-of-T")
//	q.AtomOf[T]()  -> q.Atom("name-of-T") (alias is the local q-import name)
//
// For qualified types like `pkg.MyAtom`, the cast keeps the full name
// (`pkg.MyAtom`) but the string literal is the bare name only
// (`"MyAtom"`) — that's the natural answer to "what's the atom's name".
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
		return fmt.Sprintf("/* q.A: unsupported type argument */ \"\"")
	}
	if sub.Family == familyAtomOf {
		return fmt.Sprintf("%s.Atom(%q)", alias, bare)
	}
	return fmt.Sprintf("%s(%q)", full, bare)
}
