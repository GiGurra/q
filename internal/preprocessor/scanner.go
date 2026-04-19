package preprocessor

// scanner.go — recognise q.* call expressions in user-package source
// files.
//
// Scope of v1 (this commit): only the smallest end-to-end shape is
// recognised, just enough to wire the rewriter through the compile
// pipeline:
//
//   v := q.Try(<inner-call>)
//
// where the LHS is a single identifier (or _), the := operator is
// short-var-decl, the RHS is exactly one call to the q.Try helper, and
// the inner call returns (T, error). All other shapes — chain methods,
// q.NotNil family, plain assignment, return-position, multi-LHS, etc.
// — are out of scope for this commit and will land in subsequent
// passes. Encountering an unrecognised q.* call produces a Diagnostic
// so a half-rewritten build does not happen silently.
//
// The scanner only resolves the import alias of github.com/GiGurra/q/pkg/q
// per file. It does not consult go/types — call expressions are matched
// purely on AST shape and the local alias.

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
)

// qPkgImportPath is the import path of pkg/q, the surface the
// preprocessor recognises.
const qPkgImportPath = "github.com/GiGurra/q/pkg/q"

// callShape describes one recognised q.* call site, captured at scan
// time so the rewriter can emit the inlined replacement without
// re-walking the AST.
type callShape struct {
	// Stmt is the enclosing statement in the function body. Its source
	// span is the unit the rewriter replaces; everything else inside
	// the function stays intact.
	Stmt ast.Stmt

	// LHSName is the single LHS identifier of the := assignment, or
	// "_" for the discard case (not yet supported in v1).
	LHSName string

	// InnerCall is the (T, error)-returning call passed to q.Try. The
	// rewriter copies its source span verbatim into the v, err :=
	// <inner> binding.
	InnerCall *ast.CallExpr

	// EnclosingFunc is the FuncDecl containing this call. Its
	// Type.Results gives the rewriter the result types from which to
	// synthesize the zero-value tuple in the early return.
	EnclosingFunc *ast.FuncDecl
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
			shape, ok, err := matchTryAssign(stmt, alias, fn)
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
					fmt.Sprintf("unsupported q.* call shape; v1 of the rewriter only handles `v := %s.Try(call(...))`", alias)))
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

// matchTryAssign tests whether stmt is the v1-recognised shape:
//
//	<ident> := <alias>.Try(<call-expr>)
//
// Returns the shape on a match. Returns (zero, false, nil) on a
// no-match without diagnostic. Returns (zero, false, err) when the
// statement *almost* matches but is malformed (e.g. q.Try with the
// wrong arity) — the caller turns these into diagnostics.
func matchTryAssign(stmt ast.Stmt, alias string, fn *ast.FuncDecl) (callShape, bool, error) {
	as, ok := stmt.(*ast.AssignStmt)
	if !ok || as.Tok != token.DEFINE || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
		return callShape{}, false, nil
	}
	id, ok := as.Lhs[0].(*ast.Ident)
	if !ok {
		return callShape{}, false, nil
	}
	call, ok := as.Rhs[0].(*ast.CallExpr)
	if !ok {
		return callShape{}, false, nil
	}
	if !isSelector(call.Fun, alias, "Try") {
		return callShape{}, false, nil
	}
	if len(call.Args) != 1 {
		return callShape{}, false, fmt.Errorf("q.Try must take exactly one argument (a (T, error)-returning call); got %d", len(call.Args))
	}
	inner, ok := call.Args[0].(*ast.CallExpr)
	if !ok {
		return callShape{}, false, fmt.Errorf("q.Try's argument must itself be a call expression returning (T, error)")
	}
	return callShape{
		Stmt:          stmt,
		LHSName:       id.Name,
		InnerCall:     inner,
		EnclosingFunc: fn,
	}, true, nil
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
