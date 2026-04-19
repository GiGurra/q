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
	familyNotNil
	familyNotNilE
)

// form is the syntactic position of a recognised q.* call:
//
//	formDefine  -> v   := q.Try(call())   (declares LHS via :=)
//	formAssign  -> v    = q.Try(call())   (assigns to existing LHS)
//	formDiscard -> 	      q.Try(call())   (ExprStmt; bubbles, drops T/p)
type form int

const (
	formDefine form = iota
	formAssign
	formDiscard
)

// callShape describes one recognised q.* call site, captured at scan
// time so the rewriter can emit the inlined replacement without
// re-walking the AST.
type callShape struct {
	// Stmt is the enclosing statement in the function body. Its source
	// span is the unit the rewriter replaces; everything else inside
	// the function stays intact.
	Stmt ast.Stmt

	// Form is the syntactic position — define, assign, or discard.
	Form form

	// LHSExpr is the AST node for the LHS in formDefine / formAssign;
	// nil for formDiscard. Resolved to source text by the rewriter
	// using its source-byte buffer (same flow as InnerExpr). For
	// formDefine the AST node is always *ast.Ident; for formAssign
	// it can be any addressable expression (ident, selector, index).
	LHSExpr ast.Expr

	// Family identifies the source-monad entry — Try, TryE, etc.
	Family family

	// Method is the chain method name on a TryE / NotNilE shape — Err,
	// ErrF, Catch, Wrap, Wrapf — or "" for a bare Try / NotNil.
	Method string

	// MethodArgs are the args passed to the chain method. nil when
	// Method is "".
	MethodArgs []ast.Expr

	// InnerExpr is the source expression handed to the q.* entry: a
	// (T, error)-returning call for the Try family, or any pointer-
	// returning expression for the NotNil family. The rewriter copies
	// its source span verbatim into the bind line.
	InnerExpr ast.Expr

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
		walkBlock(fset, path, fn.Body, alias, fn, &shapes, &diags)
	}

	return shapes, diags, nil
}

// walkBlock recursively scans every statement in the block (and every
// block nested inside it — if-bodies, else-bodies, for-bodies, switch
// case clauses, type-switch cases, select-comm-clauses, range bodies,
// and plain blocks). Each leaf statement is fed to matchStatement; any
// q.* reference in an unsupported position produces a diagnostic.
//
// Nested-scope rewrites are correct because each shape's replacement
// is a self-contained block (zero or one bind line, an if check, a
// return). The new statements live where the original q.* statement
// lived — same scope, same in-flow position — so visibility of the
// LHS variable to surrounding code is preserved.
func walkBlock(fset *token.FileSet, path string, block *ast.BlockStmt, alias string, fn *ast.FuncDecl, shapes *[]callShape, diags *[]Diagnostic) {
	if block == nil {
		return
	}
	for _, stmt := range block.List {
		shape, ok, err := matchStatement(fset, stmt, alias, fn)
		if err != nil {
			*diags = append(*diags, diagAt(fset, path, stmt.Pos(), err.Error()))
		} else if ok {
			*shapes = append(*shapes, shape)
		} else if pos := findQReference(stmt, alias); pos.IsValid() {
			*diags = append(*diags, diagAt(fset, path, pos,
				fmt.Sprintf("unsupported q.* call shape; supported: `v := %s.Try/NotNil(...)`, `v = %s.Try/NotNil(...)`, `%s.Try/NotNil(...)` (discard), with optional .Err / .ErrF / .Catch / .Wrap / .Wrapf chain methods on the *E entries", alias, alias, alias)))
		}
		walkChildBlocks(fset, path, stmt, alias, fn, shapes, diags)
	}
}

// walkChildBlocks dispatches into every child *ast.BlockStmt that the
// given statement holds. Mirrors the shape of go/ast's nodes that
// carry blocks; new statement kinds added by future Go versions would
// need a case here.
func walkChildBlocks(fset *token.FileSet, path string, stmt ast.Stmt, alias string, fn *ast.FuncDecl, shapes *[]callShape, diags *[]Diagnostic) {
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		walkBlock(fset, path, s, alias, fn, shapes, diags)
	case *ast.IfStmt:
		walkBlock(fset, path, s.Body, alias, fn, shapes, diags)
		if s.Else != nil {
			switch elseStmt := s.Else.(type) {
			case *ast.BlockStmt:
				walkBlock(fset, path, elseStmt, alias, fn, shapes, diags)
			case *ast.IfStmt:
				walkChildBlocks(fset, path, elseStmt, alias, fn, shapes, diags)
			}
		}
	case *ast.ForStmt:
		walkBlock(fset, path, s.Body, alias, fn, shapes, diags)
	case *ast.RangeStmt:
		walkBlock(fset, path, s.Body, alias, fn, shapes, diags)
	case *ast.SwitchStmt:
		walkBlock(fset, path, s.Body, alias, fn, shapes, diags)
	case *ast.TypeSwitchStmt:
		walkBlock(fset, path, s.Body, alias, fn, shapes, diags)
	case *ast.SelectStmt:
		walkBlock(fset, path, s.Body, alias, fn, shapes, diags)
	case *ast.CaseClause:
		// case clause Body is a []ast.Stmt without its own BlockStmt
		// wrapper, so we walk it inline.
		for _, child := range s.Body {
			// Synthesise the same per-stmt logic walkBlock applies, but
			// without the BlockStmt wrapper.
			subShape, ok, err := matchStatement(fset, child, alias, fn)
			if err != nil {
				*diags = append(*diags, diagAt(fset, path, child.Pos(), err.Error()))
			} else if ok {
				*shapes = append(*shapes, subShape)
			} else if pos := findQReference(child, alias); pos.IsValid() {
				*diags = append(*diags, diagAt(fset, path, pos,
					fmt.Sprintf("unsupported q.* call shape; supported: `v := %s.Try/NotNil(...)`, `v = %s.Try/NotNil(...)`, `%s.Try/NotNil(...)` (discard), with optional .Err / .ErrF / .Catch / .Wrap / .Wrapf chain methods on the *E entries", alias, alias, alias)))
			}
			walkChildBlocks(fset, path, child, alias, fn, shapes, diags)
		}
	case *ast.CommClause:
		for _, child := range s.Body {
			subShape, ok, err := matchStatement(fset, child, alias, fn)
			if err != nil {
				*diags = append(*diags, diagAt(fset, path, child.Pos(), err.Error()))
			} else if ok {
				*shapes = append(*shapes, subShape)
			} else if pos := findQReference(child, alias); pos.IsValid() {
				*diags = append(*diags, diagAt(fset, path, pos,
					fmt.Sprintf("unsupported q.* call shape; supported: `v := %s.Try/NotNil(...)`, `v = %s.Try/NotNil(...)`, `%s.Try/NotNil(...)` (discard), with optional .Err / .ErrF / .Catch / .Wrap / .Wrapf chain methods on the *E entries", alias, alias, alias)))
			}
			walkChildBlocks(fset, path, child, alias, fn, shapes, diags)
		}
	case *ast.LabeledStmt:
		walkChildBlocks(fset, path, s.Stmt, alias, fn, shapes, diags)
	}
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

// matchStatement dispatches on the statement form (assign vs expr stmt)
// and looks for one of the recognised q.* call shapes. Recognised
// shapes:
//
//	<ident>      := <alias>.Try(<call>)                       formDefine
//	<ident>       = <alias>.Try(<call>)                       formAssign
//	             	<alias>.Try(<call>)                        formDiscard
//
//	<ident>      := <alias>.TryE(<call>).<Method>(<args>...)  formDefine
//	<ident>       = <alias>.TryE(<call>).<Method>(<args>...)  formAssign
//	             	<alias>.TryE(<call>).<Method>(<args>...)   formDiscard
//
// (Same set mirrored for q.NotNil / q.NotNilE with a *T expression in
// place of the inner call.)
//
// Returns the shape on a match, (zero, false, nil) on a no-match, and
// (zero, false, err) when the statement is *almost* a match but
// malformed — the caller turns these into diagnostics.
func matchStatement(fset *token.FileSet, stmt ast.Stmt, alias string, fn *ast.FuncDecl) (callShape, bool, error) {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		if (s.Tok != token.DEFINE && s.Tok != token.ASSIGN) || len(s.Lhs) != 1 || len(s.Rhs) != 1 {
			return callShape{}, false, nil
		}
		// For DEFINE the LHS must be a fresh identifier; for ASSIGN it
		// can be any addressable expression (ident, selector, index).
		if s.Tok == token.DEFINE {
			if _, ok := s.Lhs[0].(*ast.Ident); !ok {
				return callShape{}, false, nil
			}
		}
		f := formDefine
		if s.Tok == token.ASSIGN {
			f = formAssign
		}
		shape, ok, err := classifyQCall(s.Rhs[0], alias)
		if err != nil || !ok {
			return callShape{}, ok, err
		}
		shape.Stmt = stmt
		shape.Form = f
		shape.LHSExpr = s.Lhs[0]
		shape.EnclosingFunc = fn
		return shape, true, nil

	case *ast.ExprStmt:
		shape, ok, err := classifyQCall(s.X, alias)
		if err != nil || !ok {
			return callShape{}, ok, err
		}
		shape.Stmt = stmt
		shape.Form = formDiscard
		shape.EnclosingFunc = fn
		return shape, true, nil
	}
	return callShape{}, false, nil
}

// classifyQCall examines a single expression and reports whether it is
// one of the recognised q.* call shapes (bare or chain). Sets the
// per-shape fields on the returned callShape but leaves Stmt, Form,
// LHSText, and EnclosingFunc to the caller.
func classifyQCall(expr ast.Expr, alias string) (callShape, bool, error) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return callShape{}, false, nil
	}

	// Bare q.Try / q.NotNil.
	if isSelector(call.Fun, alias, "Try") {
		if len(call.Args) != 1 {
			return callShape{}, false, fmt.Errorf("q.Try must take exactly one argument (a (T, error)-returning call); got %d", len(call.Args))
		}
		if _, ok := call.Args[0].(*ast.CallExpr); !ok {
			return callShape{}, false, fmt.Errorf("q.Try's argument must itself be a call expression returning (T, error)")
		}
		return callShape{Family: familyTry, InnerExpr: call.Args[0]}, true, nil
	}
	if isSelector(call.Fun, alias, "NotNil") {
		if len(call.Args) != 1 {
			return callShape{}, false, fmt.Errorf("q.NotNil must take exactly one argument (a *T expression); got %d", len(call.Args))
		}
		return callShape{Family: familyNotNil, InnerExpr: call.Args[0]}, true, nil
	}

	// Chain on q.TryE / q.NotNilE.
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		entry, isEntry := sel.X.(*ast.CallExpr)
		if !isEntry {
			return callShape{}, false, nil
		}
		switch {
		case isSelector(entry.Fun, alias, "TryE"):
			if !chainMethods[sel.Sel.Name] {
				return callShape{}, false, fmt.Errorf("q.TryE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return callShape{}, false, fmt.Errorf("q.TryE must take exactly one argument (a (T, error)-returning call); got %d", len(entry.Args))
			}
			if _, ok := entry.Args[0].(*ast.CallExpr); !ok {
				return callShape{}, false, fmt.Errorf("q.TryE's argument must itself be a call expression returning (T, error)")
			}
			return callShape{Family: familyTryE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0]}, true, nil
		case isSelector(entry.Fun, alias, "NotNilE"):
			if !chainMethods[sel.Sel.Name] {
				return callShape{}, false, fmt.Errorf("q.NotNilE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return callShape{}, false, fmt.Errorf("q.NotNilE must take exactly one argument (a *T expression); got %d", len(entry.Args))
			}
			return callShape{Family: familyNotNilE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0]}, true, nil
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
//
// Bounded: descent stops at any nested *ast.BlockStmt. The walker in
// walkChildBlocks recurses into those blocks separately and runs
// matchStatement on each statement inside, so a q.* call inside a
// nested block is the responsibility of the recursive descent — not
// of the outer container statement's diagnostic. Without this bound,
// a recognised `v := q.Try(call())` inside an if-body would also be
// flagged as "unsupported" against the enclosing if-statement.
func findQReference(stmt ast.Stmt, alias string) token.Pos {
	var found token.Pos
	ast.Inspect(stmt, func(n ast.Node) bool {
		// Skip the root if it IS a BlockStmt — only stop descent for
		// nested ones (the inspector starts at stmt itself).
		if blk, ok := n.(*ast.BlockStmt); ok && ast.Node(blk) != ast.Node(stmt) {
			return false
		}
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
