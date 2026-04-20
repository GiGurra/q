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
	familyCheck   // q.Check(err) — void, always formDiscard
	familyCheckE  // q.CheckE(err).<method> — void chain, always formDiscard
	familyOpen    // q.Open(v, err).Release(cleanup) — value chain, always Release-terminated
	familyOpenE   // q.OpenE(v, err).<shape?>.Release(cleanup) — value chain with optional shape
)

// form is the syntactic position of a recognised q.* call:
//
//	formDefine  -> v   := q.Try(call())     (declares LHS via :=)
//	formAssign  -> v    = q.Try(call())     (assigns to existing LHS)
//	formDiscard ->        q.Try(call())     (ExprStmt; bubbles, drops T/p)
//	formReturn  -> return …, q.Try(…), …    (q.* anywhere in a result)
//	formHoist   -> v := f(q.Try(…), …)      (q.* nested inside a non-
//	                                         return statement's expr)
//
// formHoist is the general case: the rewriter binds each q.* call to
// its own `_qTmpN`, emits per-call bubble checks, then re-emits the
// original statement with each q.* span replaced by its temp. The
// direct-bind forms (formDefine/formAssign/formDiscard) are kept for
// the common simple shapes so the rewrite stays tight (one line
// instead of two for `v := q.Try(call())`).
type form int

const (
	formDefine form = iota
	formAssign
	formDiscard
	formReturn
	formHoist
)

// qSubCall captures the per-call-site pieces of a recognised q.*
// expression: which family/method, the inner (T, error) call or *T
// expression, the outer call span, and any chain-method arguments.
// One callShape holds one (non-return forms) or many (formReturn with
// multiple q.*s in the same expression) of these.
type qSubCall struct {
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

	// OuterCall is the q.* call expression (bare `q.Try(...)` or the
	// outer chain call `q.TryE(...).Method(...)`). For formReturn,
	// the rewriter splices `_qTmpN` into the reconstructed final
	// return in place of this call's source span.
	OuterCall ast.Expr

	// ReleaseArg is the cleanup function passed to .Release in the
	// Open family (familyOpen / familyOpenE). nil for every other
	// family. When non-nil, the rewriter emits a `defer
	// (<cleanup>)(<resultVar>)` line on the success path so the
	// cleanup fires when the enclosing function returns.
	ReleaseArg ast.Expr
}

// callShape describes one recognised q.* call site, captured at scan
// time so the rewriter can emit the inlined replacement without
// re-walking the AST.
type callShape struct {
	// Stmt is the enclosing statement in the function body. Its source
	// span is the unit the rewriter replaces; everything else inside
	// the function stays intact.
	Stmt ast.Stmt

	// Form is the syntactic position — define, assign, discard, return.
	Form form

	// LHSExpr is the AST node for the LHS in formDefine / formAssign;
	// nil for formDiscard and formReturn. Resolved to source text by
	// the rewriter using its source-byte buffer. For formDefine the
	// AST node is always *ast.Ident; for formAssign it can be any
	// addressable expression (ident, selector, index).
	LHSExpr ast.Expr

	// Calls holds the recognised q.* sub-calls inside this statement.
	// Always length 1 for non-return forms. formReturn can have
	// length >= 1 (e.g. `return q.Try(a()) * q.Try(b()), nil`).
	Calls []qSubCall

	// EnclosingFuncType is the signature of the nearest-enclosing
	// function — either the outer FuncDecl or an inner FuncLit. Its
	// Results give the rewriter the result types from which to
	// synthesize the zero-value tuple in the early return. Using the
	// nearest enclosing scope is critical for q.* inside closures:
	// an inner FuncLit can have a different result arity/types than
	// its outer FuncDecl.
	EnclosingFuncType *ast.FuncType
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

// qRuntimeHelpers is the set of q.* function names whose call sites
// are left untouched by the preprocessor — they have real bodies
// and execute at runtime. Scanner's "unsupported q.* shape"
// diagnostic path (findQReference / qCallRootPos) ignores these
// so a standalone `q.ToErr(...)` call doesn't trip the fallback
// flag.
var qRuntimeHelpers = map[string]bool{
	"ToErr": true,
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
		walkBlock(fset, path, fn.Body, alias, fn.Type, &shapes, &diags)
	}

	return shapes, diags, nil
}

// walkBlock recursively scans every statement in the block (and every
// block nested inside it — if-bodies, else-bodies, for-bodies, switch
// case clauses, type-switch cases, select-comm-clauses, range bodies,
// and plain blocks). Each leaf statement is fed to matchStatement; any
// q.* reference in an unsupported position produces a diagnostic.
//
// fnType is the signature of the nearest-enclosing function — the
// outer FuncDecl at the top level, or an inner FuncLit after we cross
// into a closure body via walkFuncLits. Each shape matched here
// records fnType as its EnclosingFuncType so the rewriter uses the
// correct result list for zero-value synthesis.
//
// Nested-scope rewrites are correct because each shape's replacement
// is a self-contained block (zero or one bind line, an if check, a
// return). The new statements live where the original q.* statement
// lived — same scope, same in-flow position — so visibility of the
// LHS variable to surrounding code is preserved.
func walkBlock(fset *token.FileSet, path string, block *ast.BlockStmt, alias string, fnType *ast.FuncType, shapes *[]callShape, diags *[]Diagnostic) {
	if block == nil {
		return
	}
	for _, stmt := range block.List {
		shape, ok, err := matchStatement(stmt, alias, fnType)
		if err != nil {
			*diags = append(*diags, diagAt(fset, path, stmt.Pos(), err.Error()))
		} else if ok {
			*shapes = append(*shapes, shape)
		} else if pos := findQReference(stmt, alias); pos.IsValid() {
			*diags = append(*diags, diagAt(fset, path, pos,
				fmt.Sprintf("unsupported q.* call shape; supported: `v := %s.Try/NotNil(...)`, `v = %s.Try/NotNil(...)`, `%s.Try/NotNil(...)` (discard), `return %s.Try/NotNil(...), …` (q.* as one top-level return result), with optional .Err / .ErrF / .Catch / .Wrap / .Wrapf chain methods on the *E entries", alias, alias, alias, alias)))
		}
		walkChildBlocks(fset, path, stmt, alias, fnType, shapes, diags)
		walkFuncLits(fset, path, stmt, alias, shapes, diags)
	}
}

// walkFuncLits finds *ast.FuncLit expressions reachable from stmt
// without crossing a nested *ast.BlockStmt boundary. For each FuncLit
// found, walkBlock recurses into the FuncLit's body with the
// FuncLit's own Type as the enclosing function scope — so q.* inside
// a closure bubbles according to the *closure's* result list, not the
// outer FuncDecl's.
//
// The BlockStmt-boundary guard prevents double-walking: FuncLits
// inside nested statements (e.g. an if-body) are reached via the
// recursive walkBlock call that walkChildBlocks triggers on that
// body. Stopping descent at FuncLits themselves after processing
// avoids walking a closure's body twice when it contains further
// closures; the recursive walkBlock on the outer closure will
// discover the inner one.
func walkFuncLits(fset *token.FileSet, path string, stmt ast.Stmt, alias string, shapes *[]callShape, diags *[]Diagnostic) {
	ast.Inspect(stmt, func(n ast.Node) bool {
		if blk, ok := n.(*ast.BlockStmt); ok && ast.Node(blk) != ast.Node(stmt) {
			return false
		}
		lit, ok := n.(*ast.FuncLit)
		if !ok {
			return true
		}
		walkBlock(fset, path, lit.Body, alias, lit.Type, shapes, diags)
		return false
	})
}

// walkChildBlocks dispatches into every child *ast.BlockStmt that the
// given statement holds. Mirrors the shape of go/ast's nodes that
// carry blocks; new statement kinds added by future Go versions would
// need a case here.
func walkChildBlocks(fset *token.FileSet, path string, stmt ast.Stmt, alias string, fnType *ast.FuncType, shapes *[]callShape, diags *[]Diagnostic) {
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		walkBlock(fset, path, s, alias, fnType, shapes, diags)
	case *ast.IfStmt:
		walkBlock(fset, path, s.Body, alias, fnType, shapes, diags)
		if s.Else != nil {
			switch elseStmt := s.Else.(type) {
			case *ast.BlockStmt:
				walkBlock(fset, path, elseStmt, alias, fnType, shapes, diags)
			case *ast.IfStmt:
				walkChildBlocks(fset, path, elseStmt, alias, fnType, shapes, diags)
			}
		}
	case *ast.ForStmt:
		walkBlock(fset, path, s.Body, alias, fnType, shapes, diags)
	case *ast.RangeStmt:
		walkBlock(fset, path, s.Body, alias, fnType, shapes, diags)
	case *ast.SwitchStmt:
		walkBlock(fset, path, s.Body, alias, fnType, shapes, diags)
	case *ast.TypeSwitchStmt:
		walkBlock(fset, path, s.Body, alias, fnType, shapes, diags)
	case *ast.SelectStmt:
		walkBlock(fset, path, s.Body, alias, fnType, shapes, diags)
	case *ast.CaseClause:
		// case clause Body is a []ast.Stmt without its own BlockStmt
		// wrapper, so we walk it inline.
		for _, child := range s.Body {
			// Synthesise the same per-stmt logic walkBlock applies, but
			// without the BlockStmt wrapper.
			subShape, ok, err := matchStatement(child, alias, fnType)
			if err != nil {
				*diags = append(*diags, diagAt(fset, path, child.Pos(), err.Error()))
			} else if ok {
				*shapes = append(*shapes, subShape)
			} else if pos := findQReference(child, alias); pos.IsValid() {
				*diags = append(*diags, diagAt(fset, path, pos,
					fmt.Sprintf("unsupported q.* call shape; supported: `v := %s.Try/NotNil(...)`, `v = %s.Try/NotNil(...)`, `%s.Try/NotNil(...)` (discard), `return %s.Try/NotNil(...), …` (q.* as one top-level return result), with optional .Err / .ErrF / .Catch / .Wrap / .Wrapf chain methods on the *E entries", alias, alias, alias, alias)))
			}
			walkChildBlocks(fset, path, child, alias, fnType, shapes, diags)
			walkFuncLits(fset, path, child, alias, shapes, diags)
		}
	case *ast.CommClause:
		for _, child := range s.Body {
			subShape, ok, err := matchStatement(child, alias, fnType)
			if err != nil {
				*diags = append(*diags, diagAt(fset, path, child.Pos(), err.Error()))
			} else if ok {
				*shapes = append(*shapes, subShape)
			} else if pos := findQReference(child, alias); pos.IsValid() {
				*diags = append(*diags, diagAt(fset, path, pos,
					fmt.Sprintf("unsupported q.* call shape; supported: `v := %s.Try/NotNil(...)`, `v = %s.Try/NotNil(...)`, `%s.Try/NotNil(...)` (discard), `return %s.Try/NotNil(...), …` (q.* as one top-level return result), with optional .Err / .ErrF / .Catch / .Wrap / .Wrapf chain methods on the *E entries", alias, alias, alias, alias)))
			}
			walkChildBlocks(fset, path, child, alias, fnType, shapes, diags)
			walkFuncLits(fset, path, child, alias, shapes, diags)
		}
	case *ast.LabeledStmt:
		walkChildBlocks(fset, path, s.Stmt, alias, fnType, shapes, diags)
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
func matchStatement(stmt ast.Stmt, alias string, fnType *ast.FuncType) (callShape, bool, error) {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		if s.Tok != token.DEFINE && s.Tok != token.ASSIGN {
			return callShape{}, false, nil
		}
		// Direct-bind eligibility: single LHS, single RHS, RHS IS a
		// q.* call, LHS has no nested q.*. That's the tight one-line
		// shape: `v, _qErrN := inner`. Anything else falls through to
		// hoist.
		if len(s.Lhs) == 1 && len(s.Rhs) == 1 && !hasQRef(s.Lhs[0], alias) {
			lhsOK := true
			if s.Tok == token.DEFINE {
				if _, isIdent := s.Lhs[0].(*ast.Ident); !isIdent {
					lhsOK = false
				}
			}
			if lhsOK {
				sub, ok, err := classifyQCall(s.Rhs[0], alias)
				if err != nil {
					return callShape{}, false, err
				}
				// Direct-bind also requires the matched q.*'s own
				// InnerExpr / MethodArgs to be free of nested q.*s.
				// Otherwise the bind line would embed an unrewritten
				// q.* call — fall through to hoist, which handles
				// nesting by rendering innermost first and feeding
				// their `_qTmpN` into the outer's bind.
				if ok && !hasQRefInSub(sub, alias) {
					f := formDefine
					if s.Tok == token.ASSIGN {
						f = formAssign
					}
					return callShape{
						Stmt:              stmt,
						Form:              f,
						LHSExpr:           s.Lhs[0],
						Calls:             []qSubCall{sub},
						EnclosingFuncType: fnType,
					}, true, nil
				}
			}
		}
		// Hoist path: bind every q.* call inside LHS or RHS to a temp,
		// check each, then re-emit the statement with the q.* spans
		// substituted.
		return matchHoist(stmt, fnType, alias, append(append([]ast.Expr(nil), s.Lhs...), s.Rhs...))

	case *ast.ExprStmt:
		sub, ok, err := classifyQCall(s.X, alias)
		if err != nil {
			return callShape{}, false, err
		}
		if ok {
			return callShape{
				Stmt:              stmt,
				Form:              formDiscard,
				Calls:             []qSubCall{sub},
				EnclosingFuncType: fnType,
			}, true, nil
		}
		return matchHoist(stmt, fnType, alias, []ast.Expr{s.X})

	case *ast.ReturnStmt:
		// Find every q.* call anywhere inside the return's result
		// expressions — not just top-level. This makes shapes like
		// `return q.Try(a()) * q.Try(b()), nil` work: each q.* call
		// binds to its own `_qTmpN` with its own bubble check, and
		// the final return keeps the rest of the expression verbatim
		// with each `_qTmpN` spliced into its call's source span.
		subs, err := collectQCalls(s.Results, alias)
		if err != nil {
			return callShape{}, false, err
		}
		if len(subs) == 0 {
			return callShape{}, false, nil
		}
		return callShape{
			Stmt:              stmt,
			Form:              formReturn,
			Calls:             subs,
			EnclosingFuncType: fnType,
		}, true, nil
	}
	return callShape{}, false, nil
}

// matchHoist builds a formHoist callShape by collecting every q.*
// call reachable from exprs. Returns (zero, false, nil) when no q.*
// is present — the caller falls through to the findQReference
// diagnostic path.
func matchHoist(stmt ast.Stmt, fnType *ast.FuncType, alias string, exprs []ast.Expr) (callShape, bool, error) {
	subs, err := collectQCalls(exprs, alias)
	if err != nil {
		return callShape{}, false, err
	}
	if len(subs) == 0 {
		return callShape{}, false, nil
	}
	return callShape{
		Stmt:              stmt,
		Form:              formHoist,
		Calls:             subs,
		EnclosingFuncType: fnType,
	}, true, nil
}

// collectQCalls walks each expr's AST sub-tree with ast.Inspect and
// returns every recognised q.* call in source order, including those
// nested inside another matched q.*'s InnerExpr or MethodArgs. The
// rewriter handles nesting by rendering the innermost first and
// substituting each inner's `_qTmpN` into the outer's bind line.
//
// Descent stops at *ast.FuncLit boundaries — those belong to a
// nested function scope and are handled by walkFuncLits with the
// inner FuncType as their enclosing signature.
func collectQCalls(exprs []ast.Expr, alias string) ([]qSubCall, error) {
	var subs []qSubCall
	var walkErr error
	for _, e := range exprs {
		ast.Inspect(e, func(n ast.Node) bool {
			if walkErr != nil {
				return false
			}
			if _, isLit := n.(*ast.FuncLit); isLit {
				return false
			}
			expr, ok := n.(ast.Expr)
			if !ok {
				return true
			}
			sub, matched, err := classifyQCall(expr, alias)
			if err != nil {
				walkErr = err
				return false
			}
			if matched {
				subs = append(subs, sub)
			}
			return true
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}
	return subs, nil
}

// hasQRefInSub reports whether the sub's InnerExpr or any MethodArg
// contains a nested q.* reference. Used by the direct-bind
// eligibility check: if the matched q.* wraps more q.*s, hoist
// instead so the nesting gets rendered innermost-first.
func hasQRefInSub(sub qSubCall, alias string) bool {
	if hasQRef(sub.InnerExpr, alias) {
		return true
	}
	for _, a := range sub.MethodArgs {
		if hasQRef(a, alias) {
			return true
		}
	}
	return false
}

// hasQRef reports whether the expression's AST contains any selector
// rooted at the local q-alias identifier. Descent stops at FuncLits
// — those belong to a nested scope and are scanned separately.
func hasQRef(e ast.Expr, alias string) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if found {
			return false
		}
		if _, isLit := n.(*ast.FuncLit); isLit {
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
			found = true
			return false
		}
		return true
	})
	return found
}

// classifyQCall examines a single expression and reports whether it is
// one of the recognised q.* call shapes (bare or chain). The per-call
// fields are returned as a qSubCall; per-statement fields (Stmt,
// Form, LHSExpr, EnclosingFuncType) are the caller's responsibility.
func classifyQCall(expr ast.Expr, alias string) (qSubCall, bool, error) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return qSubCall{}, false, nil
	}

	// Bare q.Try / q.NotNil.
	if isSelector(call.Fun, alias, "Try") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.Try must take exactly one argument (a (T, error)-returning call); got %d", len(call.Args))
		}
		if _, ok := call.Args[0].(*ast.CallExpr); !ok {
			return qSubCall{}, false, fmt.Errorf("q.Try's argument must itself be a call expression returning (T, error)")
		}
		return qSubCall{Family: familyTry, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	if isSelector(call.Fun, alias, "NotNil") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.NotNil must take exactly one argument (a *T expression); got %d", len(call.Args))
		}
		return qSubCall{Family: familyNotNil, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Bare q.Check — error-only bubble, no chain.
	if isSelector(call.Fun, alias, "Check") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.Check must take exactly one argument (an error expression); got %d", len(call.Args))
		}
		return qSubCall{Family: familyCheck, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}

	// Chain on q.TryE / q.NotNilE / q.CheckE, or a q.Open / q.OpenE
	// chain terminated by .Release.
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		// .Release terminal — walk down through an optional shape
		// method to find q.Open / q.OpenE.
		if sel.Sel.Name == "Release" {
			return classifyOpenChain(call, sel, alias)
		}
		entry, isEntry := sel.X.(*ast.CallExpr)
		if !isEntry {
			return qSubCall{}, false, nil
		}
		switch {
		case isSelector(entry.Fun, alias, "TryE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.TryE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.TryE must take exactly one argument (a (T, error)-returning call); got %d", len(entry.Args))
			}
			if _, ok := entry.Args[0].(*ast.CallExpr); !ok {
				return qSubCall{}, false, fmt.Errorf("q.TryE's argument must itself be a call expression returning (T, error)")
			}
			return qSubCall{Family: familyTryE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "NotNilE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.NotNilE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.NotNilE must take exactly one argument (a *T expression); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyNotNilE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "CheckE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.CheckE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.CheckE must take exactly one argument (an error expression); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyCheckE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OuterCall: expr}, true, nil
		}
	}

	return qSubCall{}, false, nil
}

// classifyOpenChain recognises the q.Open / q.OpenE terminal Release
// shape, optionally with one intermediate shape method between the
// entry and Release:
//
//	q.Open(call()).Release(cleanup)
//	q.OpenE(call()).Release(cleanup)
//	q.OpenE(call()).<Shape>(args).Release(cleanup)    // Shape ∈ Err/ErrF/Wrap/Wrapf/Catch
//
// call is the outer Release CallExpr; sel is its .Fun SelectorExpr
// (sel.Sel.Name == "Release"). expr is the source expression
// (== call) used for OuterCall span.
func classifyOpenChain(call *ast.CallExpr, sel *ast.SelectorExpr, alias string) (qSubCall, bool, error) {
	expr := ast.Expr(call)
	if len(call.Args) != 1 {
		return qSubCall{}, false, fmt.Errorf("q.Open/OpenE(...).Release requires exactly one argument (a cleanup fn); got %d", len(call.Args))
	}
	releaseArg := call.Args[0]

	inner, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return qSubCall{}, false, nil
	}

	// Case 1: inner is q.Open(x) or q.OpenE(x) directly — no shape.
	if family, entry, ok := matchOpenEntry(inner, alias); ok {
		if len(entry.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.Open/OpenE must take exactly one argument (a (T, error)-returning call); got %d", len(entry.Args))
		}
		if _, isCall := entry.Args[0].(*ast.CallExpr); !isCall {
			return qSubCall{}, false, fmt.Errorf("q.Open/OpenE's argument must itself be a call expression returning (T, error)")
		}
		return qSubCall{
			Family:     family,
			InnerExpr:  entry.Args[0],
			OuterCall:  expr,
			ReleaseArg: releaseArg,
		}, true, nil
	}

	// Case 2: inner is a shape-method call on q.OpenE: q.OpenE(x).<Shape>(args).Release(...).
	shapeSel, ok := inner.Fun.(*ast.SelectorExpr)
	if !ok {
		return qSubCall{}, false, nil
	}
	if !chainMethods[shapeSel.Sel.Name] {
		return qSubCall{}, false, fmt.Errorf("q.OpenE shape method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", shapeSel.Sel.Name)
	}
	entryCall, ok := shapeSel.X.(*ast.CallExpr)
	if !ok {
		return qSubCall{}, false, nil
	}
	family, entry, ok := matchOpenEntry(entryCall, alias)
	if !ok || family != familyOpenE {
		// Shape methods are only valid on OpenE; bare Open's
		// type doesn't expose them. If we got familyOpen here,
		// the user's source won't type-check — let Go tell them.
		return qSubCall{}, false, nil
	}
	if len(entry.Args) != 1 {
		return qSubCall{}, false, fmt.Errorf("q.OpenE must take exactly one argument (a (T, error)-returning call); got %d", len(entry.Args))
	}
	if _, isCall := entry.Args[0].(*ast.CallExpr); !isCall {
		return qSubCall{}, false, fmt.Errorf("q.OpenE's argument must itself be a call expression returning (T, error)")
	}
	return qSubCall{
		Family:     familyOpenE,
		Method:     shapeSel.Sel.Name,
		MethodArgs: inner.Args,
		InnerExpr:  entry.Args[0],
		OuterCall:  expr,
		ReleaseArg: releaseArg,
	}, true, nil
}

// matchOpenEntry reports whether c is a direct q.Open / q.OpenE call
// under the local alias, and which family it belongs to.
func matchOpenEntry(c *ast.CallExpr, alias string) (family, *ast.CallExpr, bool) {
	if isSelector(c.Fun, alias, "Open") {
		return familyOpen, c, true
	}
	if isSelector(c.Fun, alias, "OpenE") {
		return familyOpenE, c, true
	}
	return 0, nil, false
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
// any q.* CALL in an unsupported position, or an invalid token.Pos
// if none is found. Only calls are flagged — plain value references
// to exported q identifiers (e.g. `errors.Is(err, q.ErrNil)`) are
// legitimate uses that don't need rewriting.
//
// A call is "rooted at q" if its Fun chain (through any number of
// `.<method>` selectors on sub-CallExprs) eventually resolves to an
// Ident named alias. That catches both the bare form `q.Try(x)` and
// the chain form `q.TryE(x).Method(y)` whose outer Fun's leftmost
// ident is still q.
//
// Bounded: descent stops at any nested *ast.BlockStmt or FuncLit.
// Nested blocks are scanned separately by walkChildBlocks; FuncLit
// bodies are scanned by walkFuncLits with their own scope. Without
// these bounds, a recognised `v := q.Try(call())` inside an if-body
// or a closure would also be flagged as "unsupported" against the
// enclosing container.
func findQReference(stmt ast.Stmt, alias string) token.Pos {
	var found token.Pos
	ast.Inspect(stmt, func(n ast.Node) bool {
		if blk, ok := n.(*ast.BlockStmt); ok && ast.Node(blk) != ast.Node(stmt) {
			return false
		}
		if _, isLit := n.(*ast.FuncLit); isLit {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if pos := qCallRootPos(call, alias); pos.IsValid() {
			found = pos
			return false
		}
		return true
	})
	return found
}

// qCallRootPos reports the position of call's outer selector when
// call is rooted at the q alias (directly or through a chain),
// or token.NoPos otherwise.
//
// Calls whose entry name (the segment immediately after the alias)
// is in qRuntimeHelpers are treated as non-q for flagging purposes
// — they are plain runtime helpers the preprocessor never rewrites,
// so a standalone `q.ToErr(...)` should not trip the
// "unsupported shape" diagnostic.
func qCallRootPos(call *ast.CallExpr, alias string) token.Pos {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return token.NoPos
	}
	// entryName tracks the segment immediately after the alias.
	// For bare `q.Try(...)`, entryName == sel.Sel.Name. For a
	// chain like `q.TryE(...).Wrap(...)`, entryName is picked up
	// when the walk reaches the inner call (q.TryE) whose .X is
	// the alias ident.
	entryName := sel.Sel.Name
	root := sel.X
	for {
		switch v := root.(type) {
		case *ast.Ident:
			if v.Name != alias {
				return token.NoPos
			}
			if qRuntimeHelpers[entryName] {
				return token.NoPos
			}
			return sel.Pos()
		case *ast.CallExpr:
			innerSel, ok := v.Fun.(*ast.SelectorExpr)
			if !ok {
				return token.NoPos
			}
			entryName = innerSel.Sel.Name
			root = innerSel.X
		default:
			return token.NoPos
		}
	}
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
