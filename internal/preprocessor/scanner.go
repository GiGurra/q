package preprocessor

// scanner.go — recognise q.* call expressions in user-package source
// files.
//
// Recognised shapes (all in statement-position inside a function body):
//
//	v := q.Try(<inner-call>)                              [Family=Try, no Method]
//	v := q.TryE(<inner-call>).<Method>(<args>...)         [Family=TryE]
//
// where <Method> is one of Err, ErrF, Catch, Wrap, Wrapf. The LHS is
// always a single identifier and the operator is the short-var-decl
// `:=`. Discard form, plain `=` assignment, multi-LHS, and the whole
// q.NotNil / q.NotNilE family are out of scope for now — each emits a
// diagnostic when encountered, so half-rewritten builds never happen
// silently.
//
// The scanner only resolves the local import alias of pkg/q per file.
// It does not consult go/types — call expressions are matched purely
// on AST shape and the local alias.

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
)

// qPkgImportPath is the import path of pkg/q, the surface the
// preprocessor recognises.
const qPkgImportPath = "github.com/GiGurra/q/pkg/q"

// family enumerates the source-monad entries the rewriter knows how
// to handle. Bare and chain entries within the same family share zero-
// value emission and rewrite-shape skeletons; the method (when present)
// picks the right way to spell the bubbled error.
type family int

const (
	familyTry family = iota
	familyTryE
	// familyNotNil and familyNotNilE will land alongside the q.NotNil
	// rewriter; declared here only to keep the enum stable when they do.
)

// callShape describes one recognised q.* call site, captured at scan
// time so the rewriter can emit the inlined replacement without
// re-walking the AST.
type callShape struct {
	// Stmt is the enclosing statement in the function body. Its source
	// span is the unit the rewriter replaces; everything else inside
	// the function stays intact.
	Stmt ast.Stmt

	// LHSName is the single LHS identifier of the := assignment.
	LHSName string

	// Family identifies the source-monad entry — Try, TryE, etc.
	Family family

	// Method is the chain method name on a TryE / NotNilE shape — Err,
	// ErrF, Catch, Wrap, Wrapf — or "" for a bare Try / NotNil.
	Method string

	// MethodArgs are the args passed to the chain method. nil when
	// Method is "".
	MethodArgs []ast.Expr

	// InnerCall is the (T, error)-returning call passed to q.Try /
	// q.TryE. The rewriter copies its source span verbatim into the
	// `v, err := <inner>` binding.
	InnerCall *ast.CallExpr

	// EnclosingFunc is the FuncDecl containing this call. Its
	// Type.Results gives the rewriter the result types from which to
	// synthesize the zero-value tuple in the early return.
	EnclosingFunc *ast.FuncDecl
}

// chainMethods is the set of recognised TryE chain method names.
// NotNilE shares the same vocabulary; the receiver type is what
// distinguishes them.
var chainMethods = map[string]bool{
	"Err":   true,
	"ErrF":  true,
	"Catch": true,
	"Wrap":  true,
	"Wrapf": true,
}

// scanFile walks one parsed source file and returns the list of
// recognised q.* call sites it contains, plus diagnostics for any
// q.* calls that did not match a recognised shape.
//
// If the file does not import pkg/q, scanFile returns (nil, nil, nil)
// — no work to do.
func scanFile(fset *token.FileSet, path string, file *ast.File) ([]callShape, []Diagnostic, error) {
	alias := qImportAlias(file)
	if alias == "" {
		return nil, nil, nil
	}

	var shapes []callShape
	var diags []Diagnostic

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		for _, stmt := range fn.Body.List {
			shape, ok, err := matchAssignStmt(stmt, alias, fn)
			if err != nil {
				diags = append(diags, diagAt(fset, path, stmt.Pos(), err.Error()))
				continue
			}
			if ok {
				shapes = append(shapes, shape)
				continue
			}

			// Statement was not a recognised shape. If it nonetheless
			// contains a q.* reference, that's an error: the user
			// reached for q in an unsupported position. Walk the
			// statement looking for any selector with q's alias and
			// flag it.
			if pos := findQReference(stmt, alias); pos.IsValid() {
				diags = append(diags, diagAt(fset, path, pos,
					fmt.Sprintf("unsupported q.* call shape; the rewriter currently handles `v := %s.Try(call(...))` and `v := %s.TryE(call(...)).<Method>(...)`", alias, alias)))
			}
		}
	}

	return shapes, diags, nil
}

// qImportAlias returns the local name under which pkg/q is imported in
// the file, "q" by default, "" if pkg/q is not imported.
func qImportAlias(file *ast.File) string {
	for _, imp := range file.Imports {
		path, err := unquote(imp.Path.Value)
		if err != nil || path != qPkgImportPath {
			continue
		}
		if imp.Name != nil {
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				// Blank or dot imports do not yield a usable selector
				// alias for the rewriter.
				return ""
			}
			return imp.Name.Name
		}
		return "q"
	}
	return ""
}

// matchAssignStmt tests whether stmt is one of the recognised shapes:
//
//	<ident> := <alias>.Try(<call-expr>)
//	<ident> := <alias>.TryE(<call-expr>).<Method>(<args>...)
//
// Returns the shape on a match. Returns (zero, false, nil) on a
// no-match without diagnostic. Returns (zero, false, err) when the
// statement *almost* matches but is malformed (e.g. q.Try with the
// wrong arity) — the caller turns these into diagnostics.
func matchAssignStmt(stmt ast.Stmt, alias string, fn *ast.FuncDecl) (callShape, bool, error) {
	as, ok := stmt.(*ast.AssignStmt)
	if !ok || as.Tok != token.DEFINE || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
		return callShape{}, false, nil
	}
	id, ok := as.Lhs[0].(*ast.Ident)
	if !ok {
		return callShape{}, false, nil
	}
	rhs, ok := as.Rhs[0].(*ast.CallExpr)
	if !ok {
		return callShape{}, false, nil
	}

	// Bare q.Try shape: rhs is q.Try(inner).
	if isSelector(rhs.Fun, alias, "Try") {
		if len(rhs.Args) != 1 {
			return callShape{}, false, fmt.Errorf("q.Try must take exactly one argument (a (T, error)-returning call); got %d", len(rhs.Args))
		}
		inner, ok := rhs.Args[0].(*ast.CallExpr)
		if !ok {
			return callShape{}, false, fmt.Errorf("q.Try's argument must itself be a call expression returning (T, error)")
		}
		return callShape{
			Stmt: stmt, LHSName: id.Name, Family: familyTry,
			InnerCall: inner, EnclosingFunc: fn,
		}, true, nil
	}

	// Chain q.TryE shape: rhs is <q.TryE(inner)>.<Method>(args...).
	if sel, ok := rhs.Fun.(*ast.SelectorExpr); ok {
		if entry, isEntry := sel.X.(*ast.CallExpr); isEntry && isSelector(entry.Fun, alias, "TryE") {
			if !chainMethods[sel.Sel.Name] {
				return callShape{}, false, fmt.Errorf("q.TryE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return callShape{}, false, fmt.Errorf("q.TryE must take exactly one argument (a (T, error)-returning call); got %d", len(entry.Args))
			}
			inner, ok := entry.Args[0].(*ast.CallExpr)
			if !ok {
				return callShape{}, false, fmt.Errorf("q.TryE's argument must itself be a call expression returning (T, error)")
			}
			return callShape{
				Stmt: stmt, LHSName: id.Name, Family: familyTryE,
				Method: sel.Sel.Name, MethodArgs: rhs.Args,
				InnerCall: inner, EnclosingFunc: fn,
			}, true, nil
		}
	}

	return callShape{}, false, nil
}

// isSelector reports whether expr has the shape `<alias>.<name>`.
func isSelector(expr ast.Expr, alias, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return x.Name == alias && sel.Sel.Name == name
}

// findQReference walks a statement's AST and returns the position of
// any selector whose root identifier matches the q alias, or an
// invalid token.Pos if none is found. Used to flag statements that
// contain q.* calls in unsupported positions.
func findQReference(stmt ast.Stmt, alias string) token.Pos {
	var found token.Pos
	ast.Inspect(stmt, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if x.Name == alias {
			found = sel.Pos()
			return false
		}
		return true
	})
	return found
}

// unquote strips the surrounding quotes from a Go string literal as
// found in *ast.BasicLit.Value. Avoids the strconv.Unquote dependency
// for the simple ASCII-only import-path case.
func unquote(lit string) (string, error) {
	if len(lit) < 2 || lit[0] != '"' || lit[len(lit)-1] != '"' {
		return "", fmt.Errorf("invalid string literal %q", lit)
	}
	return lit[1 : len(lit)-1], nil
}

// diagAt builds a Diagnostic for a position in the named file.
func diagAt(fset *token.FileSet, path string, pos token.Pos, msg string) Diagnostic {
	p := fset.Position(pos)
	if p.Filename == "" {
		p.Filename = path
	}
	return Diagnostic{
		File: p.Filename,
		Line: p.Line,
		Col:  p.Column,
		Msg:  "q: " + strings.TrimPrefix(msg, "q: "),
	}
}
