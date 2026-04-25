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
	familyOk      // q.Ok(v, ok) — comma-ok bubble using ErrNotOk sentinel
	familyOkE     // q.OkE(v, ok).<method> — comma-ok chain
	familyTrace   // q.Trace(v, err) — bubble prefixed with call-site file:line
	familyTraceE  // q.TraceE(v, err).<method> — trace-prefixed chain
	familyLock        // q.Lock(l) — Lock + defer Unlock
	familyTODO        // q.TODO([msg]) — panic with file:line prefix
	familyUnreachable // q.Unreachable([msg]) — panic with file:line prefix
	familyRequire     // q.Require(cond, [msg]) — bubble an error when cond is false
	familyRecv        // q.Recv(ch) — channel receive with close bubble
	familyRecvE       // q.RecvE(ch).<method> — chain variant
	familyAs          // q.As[T](x) — type assertion with failure bubble
	familyAsE         // q.AsE[T](x).<method> — chain variant
	familyDebugPrintln  // q.DebugPrintln(v) — in-place rewrite to q.DebugPrintlnAt("label", v)
	familyDebugSlogAttr // q.DebugSlogAttr(v) — in-place rewrite to slog.Any("label", v)
	familySlogAttr      // q.SlogAttr(v) — in-place rewrite to slog.Any("<src>", v)
	familySlogFile      // q.SlogFile() — in-place rewrite to slog.Any("file", "<basename>")
	familySlogLine      // q.SlogLine() — in-place rewrite to slog.Any("line", <line-int>)
	familyAwait       // q.Await(f) — Try-like bubble using q.AwaitRaw as the source
	familyAwaitE      // q.AwaitE(f).<method> — TryE-like chain over q.AwaitRaw
	familyRecoverAuto  // defer q.Recover()       — inject &err from enclosing sig
	familyRecoverEAuto // defer q.RecoverE().M(x) — same, for the chain variant
	familyCheckCtx       // q.CheckCtx(ctx) — ctx.Err() checkpoint
	familyCheckCtxE      // q.CheckCtxE(ctx).<method> — chain variant
	familyRecvCtx      // q.RecvCtx(ctx, ch) — ctx-aware channel receive
	familyRecvCtxE     // q.RecvCtxE(ctx, ch).<method> — chain variant
	familyAwaitCtx     // q.AwaitCtx(ctx, f) — ctx-aware future await
	familyAwaitCtxE    // q.AwaitCtxE(ctx, f).<method> — chain variant
	familyTimeout      // ctx = q.Timeout(ctx, dur) — WithTimeout + defer cancel
	familyDeadline     // ctx = q.Deadline(ctx, t)  — WithDeadline + defer cancel
	familyAwaitAll     // q.AwaitAll(futures...) — fan-in, bubble first err
	familyAwaitAllE    // chain variant
	familyAwaitAllCtx  // q.AwaitAllCtx(ctx, futures...) — same with ctx cancel
	familyAwaitAllCtxE // chain variant
	familyAwaitAny     // q.AwaitAny(futures...) — first success wins
	familyAwaitAnyE    // chain variant
	familyAwaitAnyCtx  // q.AwaitAnyCtx(ctx, futures...) — same with ctx cancel
	familyAwaitAnyCtxE // chain variant
	familyRecvAny      // q.RecvAny(chans...) — first-value-wins multi-channel select
	familyRecvAnyE     // chain variant
	familyRecvAnyCtx   // q.RecvAnyCtx(ctx, chans...)
	familyRecvAnyCtxE  // chain variant
	familyDrainCtx     // q.DrainCtx(ctx, ch) — drain until close or cancel
	familyDrainCtxE    // chain variant
	familyDrainAllCtx  // q.DrainAllCtx(ctx, chans...)
	familyDrainAllCtxE // chain variant
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
	// family AND for the .NoRelease() variant. When non-nil, the
	// rewriter emits a `defer (<cleanup>)(<resultVar>)` line on the
	// success path so the cleanup fires when the enclosing function
	// returns.
	ReleaseArg ast.Expr

	// NoRelease is true when the Open chain terminates with the
	// zero-arg .NoRelease() instead of .Release(cleanup). Bubble
	// path is identical; the rewriter skips the defer-cleanup line.
	NoRelease bool

	// AutoRelease is true when the Open chain terminates with the
	// zero-arg .Release() form. The preprocessor infers the cleanup
	// from the resource's type at compile time (channel close, or
	// a Close() method). The typecheck pass populates AutoCleanup
	// with the inferred kind; the rewriter consults AutoCleanup
	// when emitting the defer line. Mutually exclusive with
	// NoRelease and with a non-nil ReleaseArg.
	AutoRelease bool

	// AutoCleanup is the cleanup form the typecheck pass inferred
	// for an AutoRelease=true call. Zero (cleanupUnknown) until
	// the typecheck pass has run; if still zero by rewriter time
	// the typecheck pass either skipped (no importcfg) or emitted
	// a diagnostic that aborted the build.
	AutoCleanup cleanupKind

	// RecoverSteps carries any leading .RecoverIs / .RecoverAs chain
	// methods that sit between the entry call and the terminal
	// method (in source order). Currently only the TryE chain
	// exposes these. Empty for every other chain shape.
	RecoverSteps []recoverStep

	// OkArgs is the raw argument list of the q.Ok / q.OkE entry
	// call. nil for every other family. Ok accepts either a single
	// (T, bool)-returning CallExpr or two separate expressions
	// (value, ok); the rewriter reads the source span from the
	// first arg's Pos to the last arg's End to produce an inner-text
	// that drops straight into a tuple bind (`v, _qOkN := <span>`).
	OkArgs []ast.Expr

	// AsType is the explicit type argument in a q.As[T] / q.AsE[T]
	// call; nil for every other family. The rewriter splices its
	// source text into the generated type-assertion `<x>.(<T>)`.
	AsType ast.Expr

	// EntryEllipsis is the position of the variadic spread `...` on
	// the entry call's last argument, or token.NoPos if the call is
	// not variadic-spread. Only meaningful for the AwaitAll / AwaitAny
	// families whose entry signatures accept `...Future[T]`. When
	// valid, the rewriter appends `...` to the raw-helper call it
	// emits so the variadic spread survives the rewrite.
	EntryEllipsis token.Pos
}

// cleanupKind is the inferred cleanup form for q.Open(...).Release()
// (zero-arg, auto-inferred). The typecheck pass populates it on
// each AutoRelease qSubCall; the rewriter dispatches on it when
// emitting the defer line.
type cleanupKind int

const (
	cleanupUnknown   cleanupKind = iota // not inferred yet (or typecheck skipped)
	cleanupChanClose                    // channel type → defer close(v)
	cleanupCloseVoid                    // T has Close() → defer v.Close()
	cleanupCloseErr                     // T has Close() error → defer func() { _ = v.Close() }()
)

// recoverKind selects between the errors.Is and errors.As variants
// of the chain-continuing recovery methods.
type recoverKind int

const (
	recoverKindIs recoverKind = iota // .RecoverIs(sentinel, value)
	recoverKindAs                    // .RecoverAs(typedNil, value)
)

// recoverStep encodes one .RecoverIs(sentinel, value) or
// .RecoverAs(typedNil, value) chain step. Stored on qSubCall and
// rendered before the terminal bubble check.
type recoverStep struct {
	// Kind selects between errors.Is (sentinel) and errors.As (type).
	Kind recoverKind
	// MatchArg is the first arg to RecoverIs / RecoverAs:
	// - For Is: the sentinel error expression.
	// - For As: a typed-nil literal whose type the rewriter extracts
	//   at compile time (e.g. `(*MyErr)(nil)`).
	MatchArg ast.Expr
	// ValueArg is the second arg — the recovery value of type T.
	ValueArg ast.Expr
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
	"ToErr":           true,
	"Const":           true,
	"DebugPrintlnAt":  true,
	"Async":           true,
	"AwaitRaw":        true,
	"AwaitRawCtx":     true,
	"AwaitAllRaw":     true,
	"AwaitAllRawCtx":  true,
	"AwaitAnyRaw":     true,
	"AwaitAnyRawCtx":  true,
	"RecvRawCtx":      true,
	"RecvAnyRaw":      true,
	"RecvAnyRawCtx":   true,
	"Drain":           true,
	"DrainAll":        true,
	"DrainRawCtx":     true,
	"DrainAllRawCtx":  true,
	"Recover":         true,
	"RecoverE":        true,
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
		} else if !isContainerStmt(stmt) {
			if pos := findQReference(stmt, alias); pos.IsValid() {
				*diags = append(*diags, diagAt(fset, path, pos,
					fmt.Sprintf("unsupported q.* call shape; supported: `v := %s.Try/NotNil(...)`, `v = %s.Try/NotNil(...)`, `%s.Try/NotNil(...)` (discard), `return %s.Try/NotNil(...), …` (q.* as one top-level return result), with optional .Err / .ErrF / .Catch / .Wrap / .Wrapf chain methods on the *E entries", alias, alias, alias, alias)))
			}
		}
		walkChildBlocks(fset, path, stmt, alias, fnType, shapes, diags)
		walkFuncLits(fset, path, stmt, alias, shapes, diags)
	}
}

// isContainerStmt reports whether stmt's role is to hold further
// statements rather than to be one itself. Such statements should
// not trigger the "unsupported q.* shape" fallback — walkChildBlocks
// descends into them and matches their contents properly. Missing
// this check for CaseClause / CommClause causes findQReference to
// false-positive on every q.* call inside a switch default.
func isContainerStmt(stmt ast.Stmt) bool {
	switch stmt.(type) {
	case *ast.BlockStmt, *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt,
		*ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt,
		*ast.CaseClause, *ast.CommClause, *ast.LabeledStmt:
		return true
	}
	return false
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
			subShape, ok, err := matchStatement(child, alias, fnType)
			if err != nil {
				*diags = append(*diags, diagAt(fset, path, child.Pos(), err.Error()))
			} else if ok {
				*shapes = append(*shapes, subShape)
			} else if !isContainerStmt(child) {
				if pos := findQReference(child, alias); pos.IsValid() {
					*diags = append(*diags, diagAt(fset, path, pos,
						fmt.Sprintf("unsupported q.* call shape; supported: `v := %s.Try/NotNil(...)`, `v = %s.Try/NotNil(...)`, `%s.Try/NotNil(...)` (discard), `return %s.Try/NotNil(...), …` (q.* as one top-level return result), with optional .Err / .ErrF / .Catch / .Wrap / .Wrapf chain methods on the *E entries", alias, alias, alias, alias)))
				}
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
			} else if !isContainerStmt(child) {
				if pos := findQReference(child, alias); pos.IsValid() {
					*diags = append(*diags, diagAt(fset, path, pos,
						fmt.Sprintf("unsupported q.* call shape; supported: `v := %s.Try/NotNil(...)`, `v = %s.Try/NotNil(...)`, `%s.Try/NotNil(...)` (discard), `return %s.Try/NotNil(...), …` (q.* as one top-level return result), with optional .Err / .ErrF / .Catch / .Wrap / .Wrapf chain methods on the *E entries", alias, alias, alias, alias)))
				}
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

	case *ast.DeferStmt:
		sub, ok, err := classifyDeferredRecover(s.Call, alias)
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
		return callShape{}, false, nil

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
	// Bare q.Await — blocks on Future, bubbles err.
	if isSelector(call.Fun, alias, "Await") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.Await must take exactly one argument (a Future); got %d", len(call.Args))
		}
		return qSubCall{Family: familyAwait, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Bare q.DebugPrintln — in-place rewrite to q.DebugPrintlnAt
	// with an auto-generated label carrying call-site file:line
	// and the source text of the argument expression.
	if isSelector(call.Fun, alias, "DebugPrintln") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.DebugPrintln must take exactly one argument (the value to print); got %d", len(call.Args))
		}
		return qSubCall{Family: familyDebugPrintln, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Bare q.DebugSlogAttr — in-place rewrite to slog.Any with a
	// label carrying call-site file:line and the source text of
	// the argument expression. Returns slog.Attr (no pass-through).
	if isSelector(call.Fun, alias, "DebugSlogAttr") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.DebugSlogAttr must take exactly one argument (the value to wrap as a slog.Attr); got %d", len(call.Args))
		}
		return qSubCall{Family: familyDebugSlogAttr, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Bare q.SlogAttr — in-place rewrite to slog.Any keyed by the
	// argument's source text only (no file:line prefix). The
	// production-grade slog helper.
	if isSelector(call.Fun, alias, "SlogAttr") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.SlogAttr must take exactly one argument (the value to wrap as a slog.Attr); got %d", len(call.Args))
		}
		return qSubCall{Family: familySlogAttr, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Bare q.SlogFile — zero-arg in-place rewrite to
	// slog.Any("file", "<basename>"). The file basename is captured
	// at compile time from OuterCall's position.
	if isSelector(call.Fun, alias, "SlogFile") {
		if len(call.Args) != 0 {
			return qSubCall{}, false, fmt.Errorf("q.SlogFile takes no arguments; got %d", len(call.Args))
		}
		return qSubCall{Family: familySlogFile, OuterCall: expr}, true, nil
	}
	// Bare q.SlogLine — zero-arg in-place rewrite to
	// slog.Any("line", <line-int>).
	if isSelector(call.Fun, alias, "SlogLine") {
		if len(call.Args) != 0 {
			return qSubCall{}, false, fmt.Errorf("q.SlogLine takes no arguments; got %d", len(call.Args))
		}
		return qSubCall{Family: familySlogLine, OuterCall: expr}, true, nil
	}
	// Bare q.Recv — channel receive with close bubble.
	if isSelector(call.Fun, alias, "Recv") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.Recv must take exactly one argument (a channel); got %d", len(call.Args))
		}
		return qSubCall{Family: familyRecv, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Bare q.As[T](x) — type assertion with failure bubble.
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "As"); ok {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.As[T] must take exactly one argument (the value to assert); got %d", len(call.Args))
		}
		return qSubCall{Family: familyAs, InnerExpr: call.Args[0], AsType: typeArg, OuterCall: expr}, true, nil
	}
	// Bare q.CheckCtx — ctx.Err() checkpoint. Statement-only (discard).
	if isSelector(call.Fun, alias, "CheckCtx") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.CheckCtx must take exactly one argument (a context.Context); got %d", len(call.Args))
		}
		return qSubCall{Family: familyCheckCtx, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Bare q.RecvCtx(ctx, ch) — ctx-aware receive.
	if isSelector(call.Fun, alias, "RecvCtx") {
		if len(call.Args) != 2 {
			return qSubCall{}, false, fmt.Errorf("q.RecvCtx must take exactly two arguments (ctx, ch); got %d", len(call.Args))
		}
		return qSubCall{Family: familyRecvCtx, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr}, true, nil
	}
	// Bare q.AwaitCtx(ctx, future) — ctx-aware await.
	if isSelector(call.Fun, alias, "AwaitCtx") {
		if len(call.Args) != 2 {
			return qSubCall{}, false, fmt.Errorf("q.AwaitCtx must take exactly two arguments (ctx, future); got %d", len(call.Args))
		}
		return qSubCall{Family: familyAwaitCtx, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr}, true, nil
	}
	// Bare q.AwaitAll(futures...) — fan-in, bubble first err.
	if isSelector(call.Fun, alias, "AwaitAll") {
		// InnerExpr is unused by the Try-like-with-inner renderers
		// (the inner text is built from OkArgs directly), but
		// commonRenderInputs still calls exprTextSubst on it, so we
		// must hand it a non-nil expression. Use call.Fun as a
		// syntactically-valid placeholder; the returned text is
		// discarded.
		return qSubCall{Family: familyAwaitAll, InnerExpr: call.Fun, OkArgs: call.Args, OuterCall: expr, EntryEllipsis: call.Ellipsis}, true, nil
	}
	// Bare q.AwaitAllCtx(ctx, futures...) — same with ctx cancel.
	if isSelector(call.Fun, alias, "AwaitAllCtx") {
		if len(call.Args) < 1 {
			return qSubCall{}, false, fmt.Errorf("q.AwaitAllCtx must take at least one argument (ctx); got %d", len(call.Args))
		}
		return qSubCall{Family: familyAwaitAllCtx, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr, EntryEllipsis: call.Ellipsis}, true, nil
	}
	// Bare q.AwaitAny(futures...) — first success wins.
	if isSelector(call.Fun, alias, "AwaitAny") {
		return qSubCall{Family: familyAwaitAny, InnerExpr: call.Fun, OkArgs: call.Args, OuterCall: expr, EntryEllipsis: call.Ellipsis}, true, nil
	}
	// Bare q.AwaitAnyCtx(ctx, futures...) — same with ctx cancel.
	if isSelector(call.Fun, alias, "AwaitAnyCtx") {
		if len(call.Args) < 1 {
			return qSubCall{}, false, fmt.Errorf("q.AwaitAnyCtx must take at least one argument (ctx); got %d", len(call.Args))
		}
		return qSubCall{Family: familyAwaitAnyCtx, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr, EntryEllipsis: call.Ellipsis}, true, nil
	}
	// Bare q.RecvAny(chans...) — multi-channel first-value-wins select.
	if isSelector(call.Fun, alias, "RecvAny") {
		return qSubCall{Family: familyRecvAny, InnerExpr: call.Fun, OkArgs: call.Args, OuterCall: expr, EntryEllipsis: call.Ellipsis}, true, nil
	}
	// Bare q.RecvAnyCtx(ctx, chans...).
	if isSelector(call.Fun, alias, "RecvAnyCtx") {
		if len(call.Args) < 1 {
			return qSubCall{}, false, fmt.Errorf("q.RecvAnyCtx must take at least one argument (ctx); got %d", len(call.Args))
		}
		return qSubCall{Family: familyRecvAnyCtx, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr, EntryEllipsis: call.Ellipsis}, true, nil
	}
	// Bare q.DrainCtx(ctx, ch) — drain until close or cancel.
	if isSelector(call.Fun, alias, "DrainCtx") {
		if len(call.Args) != 2 {
			return qSubCall{}, false, fmt.Errorf("q.DrainCtx must take exactly two arguments (ctx, ch); got %d", len(call.Args))
		}
		return qSubCall{Family: familyDrainCtx, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr}, true, nil
	}
	// Bare q.DrainAllCtx(ctx, chans...).
	if isSelector(call.Fun, alias, "DrainAllCtx") {
		if len(call.Args) < 1 {
			return qSubCall{}, false, fmt.Errorf("q.DrainAllCtx must take at least one argument (ctx); got %d", len(call.Args))
		}
		return qSubCall{Family: familyDrainAllCtx, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr, EntryEllipsis: call.Ellipsis}, true, nil
	}
	// q.Timeout(ctx, dur) / q.Deadline(ctx, t) — define/assign shapes.
	if isSelector(call.Fun, alias, "Timeout") {
		if len(call.Args) != 2 {
			return qSubCall{}, false, fmt.Errorf("q.Timeout must take exactly two arguments (ctx, dur); got %d", len(call.Args))
		}
		return qSubCall{Family: familyTimeout, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr}, true, nil
	}
	if isSelector(call.Fun, alias, "Deadline") {
		if len(call.Args) != 2 {
			return qSubCall{}, false, fmt.Errorf("q.Deadline must take exactly two arguments (ctx, t); got %d", len(call.Args))
		}
		return qSubCall{Family: familyDeadline, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr}, true, nil
	}
	// Statement-only helpers with no chain — panic/defer shapes.
	if isSelector(call.Fun, alias, "Lock") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.Lock must take exactly one argument (a sync.Locker); got %d", len(call.Args))
		}
		return qSubCall{Family: familyLock, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	if isSelector(call.Fun, alias, "TODO") {
		if len(call.Args) > 1 {
			return qSubCall{}, false, fmt.Errorf("q.TODO takes at most one argument (an optional message string); got %d", len(call.Args))
		}
		return qSubCall{Family: familyTODO, MethodArgs: call.Args, OuterCall: expr}, true, nil
	}
	if isSelector(call.Fun, alias, "Unreachable") {
		if len(call.Args) > 1 {
			return qSubCall{}, false, fmt.Errorf("q.Unreachable takes at most one argument (an optional message string); got %d", len(call.Args))
		}
		return qSubCall{Family: familyUnreachable, MethodArgs: call.Args, OuterCall: expr}, true, nil
	}
	if isSelector(call.Fun, alias, "Require") {
		if len(call.Args) < 1 || len(call.Args) > 2 {
			return qSubCall{}, false, fmt.Errorf("q.Require takes 1 or 2 arguments (cond, [msg]); got %d", len(call.Args))
		}
		return qSubCall{Family: familyRequire, InnerExpr: call.Args[0], MethodArgs: call.Args[1:], OuterCall: expr}, true, nil
	}
	// Bare q.Trace — Try-shape with file:line-prefixed bubble.
	if isSelector(call.Fun, alias, "Trace") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.Trace must take exactly one argument (a (T, error)-returning call); got %d", len(call.Args))
		}
		if _, ok := call.Args[0].(*ast.CallExpr); !ok {
			return qSubCall{}, false, fmt.Errorf("q.Trace's argument must itself be a call expression returning (T, error)")
		}
		return qSubCall{Family: familyTrace, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Bare q.Ok — comma-ok bubble. Two valid arg shapes:
	//   q.Ok(fn())       — one CallExpr returning (T, bool)
	//   q.Ok(v, okExpr)  — two exprs, a T and a bool
	// The rewriter handles both by binding from the joined source span.
	if isSelector(call.Fun, alias, "Ok") {
		if err := validateOkArgs("q.Ok", call.Args); err != nil {
			return qSubCall{}, false, err
		}
		return qSubCall{Family: familyOk, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr}, true, nil
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
		// .Release / .NoRelease terminal — walk down through an
		// optional shape method to find q.Open / q.OpenE.
		if sel.Sel.Name == "Release" || sel.Sel.Name == "NoRelease" {
			return classifyOpenChain(call, sel, alias)
		}
		// Reject .RecoverIs / .RecoverAs as the outer (terminal)
		// method — they continue the chain and must be followed by
		// a real terminal that bubbles. Standalone use leaves the
		// captured err silently swallowed.
		if sel.Sel.Name == "RecoverIs" || sel.Sel.Name == "RecoverAs" {
			return qSubCall{}, false, fmt.Errorf("%s must be followed by a terminal method (Err, ErrF, Wrap, Wrapf, Catch); standalone use would silently swallow the bubble", sel.Sel.Name)
		}
		entry, isEntry := sel.X.(*ast.CallExpr)
		if !isEntry {
			return qSubCall{}, false, nil
		}
		// Peel any leading .RecoverIs / .RecoverAs steps off `entry`
		// before dispatching on the underlying entry name. Currently
		// only the q.TryE chain accepts these intermediates.
		actualEntry, recoverSteps, err := peelRecovers(entry)
		if err != nil {
			return qSubCall{}, false, err
		}
		entry = actualEntry
		if len(recoverSteps) > 0 && !isSelector(entry.Fun, alias, "TryE") {
			return qSubCall{}, false, fmt.Errorf("RecoverIs / RecoverAs are only supported on the q.TryE chain; chain entry must be q.TryE(...) for these intermediates to apply")
		}
		switch {
		case isSelector(entry.Fun, alias, "TryE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.TryE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf, RecoverIs, RecoverAs", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.TryE must take exactly one argument (a (T, error)-returning call); got %d", len(entry.Args))
			}
			if _, ok := entry.Args[0].(*ast.CallExpr); !ok {
				return qSubCall{}, false, fmt.Errorf("q.TryE's argument must itself be a call expression returning (T, error)")
			}
			return qSubCall{Family: familyTryE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OuterCall: expr, RecoverSteps: recoverSteps}, true, nil
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
		case isSelector(entry.Fun, alias, "TraceE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.TraceE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.TraceE must take exactly one argument (a (T, error)-returning call); got %d", len(entry.Args))
			}
			if _, ok := entry.Args[0].(*ast.CallExpr); !ok {
				return qSubCall{}, false, fmt.Errorf("q.TraceE's argument must itself be a call expression returning (T, error)")
			}
			return qSubCall{Family: familyTraceE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "AwaitE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.AwaitE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.AwaitE must take exactly one argument (a Future); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyAwaitE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "RecvE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.RecvE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.RecvE must take exactly one argument (a channel); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyRecvE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "CheckCtxE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.CheckCtxE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.CheckCtxE must take exactly one argument (a context.Context); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyCheckCtxE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "RecvCtxE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.RecvCtxE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 2 {
				return qSubCall{}, false, fmt.Errorf("q.RecvCtxE must take exactly two arguments (ctx, ch); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyRecvCtxE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OkArgs: entry.Args, OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "AwaitCtxE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.AwaitCtxE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 2 {
				return qSubCall{}, false, fmt.Errorf("q.AwaitCtxE must take exactly two arguments (ctx, future); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyAwaitCtxE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OkArgs: entry.Args, OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "AwaitAllE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.AwaitAllE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			return qSubCall{Family: familyAwaitAllE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Fun, OkArgs: entry.Args, OuterCall: expr, EntryEllipsis: entry.Ellipsis}, true, nil
		case isSelector(entry.Fun, alias, "AwaitAllCtxE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.AwaitAllCtxE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) < 1 {
				return qSubCall{}, false, fmt.Errorf("q.AwaitAllCtxE must take at least one argument (ctx); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyAwaitAllCtxE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OkArgs: entry.Args, OuterCall: expr, EntryEllipsis: entry.Ellipsis}, true, nil
		case isSelector(entry.Fun, alias, "AwaitAnyE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.AwaitAnyE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			return qSubCall{Family: familyAwaitAnyE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Fun, OkArgs: entry.Args, OuterCall: expr, EntryEllipsis: entry.Ellipsis}, true, nil
		case isSelector(entry.Fun, alias, "AwaitAnyCtxE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.AwaitAnyCtxE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) < 1 {
				return qSubCall{}, false, fmt.Errorf("q.AwaitAnyCtxE must take at least one argument (ctx); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyAwaitAnyCtxE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OkArgs: entry.Args, OuterCall: expr, EntryEllipsis: entry.Ellipsis}, true, nil
		case isSelector(entry.Fun, alias, "RecvAnyE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.RecvAnyE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			return qSubCall{Family: familyRecvAnyE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Fun, OkArgs: entry.Args, OuterCall: expr, EntryEllipsis: entry.Ellipsis}, true, nil
		case isSelector(entry.Fun, alias, "RecvAnyCtxE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.RecvAnyCtxE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) < 1 {
				return qSubCall{}, false, fmt.Errorf("q.RecvAnyCtxE must take at least one argument (ctx); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyRecvAnyCtxE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OkArgs: entry.Args, OuterCall: expr, EntryEllipsis: entry.Ellipsis}, true, nil
		case isSelector(entry.Fun, alias, "DrainCtxE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.DrainCtxE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 2 {
				return qSubCall{}, false, fmt.Errorf("q.DrainCtxE must take exactly two arguments (ctx, ch); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyDrainCtxE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OkArgs: entry.Args, OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "DrainAllCtxE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.DrainAllCtxE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) < 1 {
				return qSubCall{}, false, fmt.Errorf("q.DrainAllCtxE must take at least one argument (ctx); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyDrainAllCtxE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OkArgs: entry.Args, OuterCall: expr, EntryEllipsis: entry.Ellipsis}, true, nil
		}
		// AsE needs a dedicated check because its entry.Fun is an
		// IndexExpr carrying the type argument, not a plain selector
		// that the switch cases above cover.
		if typeArg, ok := isIndexedSelector(entry.Fun, alias, "AsE"); ok {
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.AsE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.AsE[T] must take exactly one argument (the value to assert); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyAsE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], AsType: typeArg, OuterCall: expr}, true, nil
		}
		switch {
		case isSelector(entry.Fun, alias, "OkE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.OkE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if err := validateOkArgs("q.OkE", entry.Args); err != nil {
				return qSubCall{}, false, err
			}
			return qSubCall{Family: familyOkE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OkArgs: entry.Args, OuterCall: expr}, true, nil
		}
	}

	return qSubCall{}, false, nil
}

// peelRecovers walks down through any leading
// .RecoverIs(sentinel, value) / .RecoverAs(typedNil, value) chain
// calls on `entry`, returning the underlying entry call and the
// recover steps in source order. If `entry` itself is not a chain
// (i.e. its .Fun is not a SelectorExpr selecting RecoverIs/RecoverAs),
// returns entry and nil.
//
// `entry` is the outer terminal's `.X` — what would otherwise be
// the entry call directly. If RecoverIs/RecoverAs sit between the
// entry and the terminal, this peels them off.
func peelRecovers(entry *ast.CallExpr) (*ast.CallExpr, []recoverStep, error) {
	var steps []recoverStep
	cur := entry
	for {
		sel, ok := cur.Fun.(*ast.SelectorExpr)
		if !ok {
			return cur, steps, nil
		}
		var kind recoverKind
		switch sel.Sel.Name {
		case "RecoverIs":
			kind = recoverKindIs
		case "RecoverAs":
			kind = recoverKindAs
		default:
			// Not a Recover step — done peeling.
			return cur, steps, nil
		}
		if len(cur.Args) != 2 {
			return nil, nil, fmt.Errorf("q.TryE(...).%s requires exactly two arguments (match target, recovery value); got %d", sel.Sel.Name, len(cur.Args))
		}
		// Prepend so steps end up in source order
		// (innermost-first walked, so build in reverse).
		steps = append([]recoverStep{{
			Kind:     kind,
			MatchArg: cur.Args[0],
			ValueArg: cur.Args[1],
		}}, steps...)
		next, ok := sel.X.(*ast.CallExpr)
		if !ok {
			return nil, nil, fmt.Errorf("q.TryE(...).%s applied to a non-call expression; the chain must reach a q.TryE entry call", sel.Sel.Name)
		}
		cur = next
	}
}

// classifyOpenChain recognises the q.Open / q.OpenE terminal
// Release / NoRelease shape, optionally with one intermediate shape
// method between the entry and the terminal:
//
//	q.Open(call()).Release(cleanup)
//	q.Open(call()).NoRelease()                       // explicit no-cleanup
//	q.OpenE(call()).Release(cleanup)
//	q.OpenE(call()).NoRelease()
//	q.OpenE(call()).<Shape>(args).Release(cleanup)   // Shape ∈ Err/ErrF/Wrap/Wrapf/Catch
//	q.OpenE(call()).<Shape>(args).NoRelease()
//
// call is the outer Release/NoRelease CallExpr; sel is its .Fun
// SelectorExpr (sel.Sel.Name ∈ {"Release", "NoRelease"}). expr is
// the source expression (== call) used for OuterCall span.
func classifyOpenChain(call *ast.CallExpr, sel *ast.SelectorExpr, alias string) (qSubCall, bool, error) {
	expr := ast.Expr(call)
	noRelease := sel.Sel.Name == "NoRelease"

	var (
		releaseArg  ast.Expr
		autoRelease bool
	)
	switch {
	case noRelease:
		if len(call.Args) != 0 {
			return qSubCall{}, false, fmt.Errorf("q.Open/OpenE(...).NoRelease takes no arguments; got %d", len(call.Args))
		}
	case len(call.Args) == 0:
		// .Release() with no args — preprocessor infers the cleanup
		// from the resource type at compile time.
		autoRelease = true
	case len(call.Args) == 1:
		releaseArg = call.Args[0]
	default:
		return qSubCall{}, false, fmt.Errorf("q.Open/OpenE(...).Release accepts at most one cleanup function; got %d", len(call.Args))
	}

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
			Family:      family,
			InnerExpr:   entry.Args[0],
			OuterCall:   expr,
			ReleaseArg:  releaseArg,
			NoRelease:   noRelease,
			AutoRelease: autoRelease,
		}, true, nil
	}

	// Case 2: inner is a shape-method call on q.OpenE: q.OpenE(x).<Shape>(args).<Release|NoRelease>().
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
		Family:      familyOpenE,
		Method:      shapeSel.Sel.Name,
		MethodArgs:  inner.Args,
		InnerExpr:   entry.Args[0],
		OuterCall:   expr,
		ReleaseArg:  releaseArg,
		NoRelease:   noRelease,
		AutoRelease: autoRelease,
	}, true, nil
}

// validateOkArgs enforces Ok / OkE's two valid arg shapes: one
// CallExpr returning (T, bool), or two separate expressions (T, bool).
// The rewriter reads the source span from Args[0].Pos() to
// Args[last].End(), so either shape drops straight into a tuple-
// binding `<LHS>, _qOkN := <span>` line.
func validateOkArgs(name string, args []ast.Expr) error {
	switch len(args) {
	case 1:
		if _, ok := args[0].(*ast.CallExpr); !ok {
			return fmt.Errorf("%s's single argument must be a call expression returning (T, bool); pass two separate arguments (value, ok) otherwise", name)
		}
		return nil
	case 2:
		return nil
	default:
		return fmt.Errorf("%s must take one (T, bool)-returning call or two arguments (value, ok); got %d", name, len(args))
	}
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

// recoverEChainMethods enumerates the chain methods the rewriter
// knows how to splice &err into for `defer q.RecoverE().X(args)`.
// Superset of chainMethods — RecoverE has .Map which the bubble
// families do not.
var recoverEChainMethods = map[string]bool{
	"Map":   true,
	"Err":   true,
	"ErrF":  true,
	"Wrap":  true,
	"Wrapf": true,
}

// classifyDeferredRecover recognises the two auto-Recover shapes:
//
//	q.Recover()                     — no args  → familyRecoverAuto
//	q.RecoverE().<Method>(args...)  — no args on RecoverE, valid chain method → familyRecoverEAuto
//
// Returns (_, false, nil) when the call doesn't match either shape;
// the caller treats that as "not our form" and falls back to the
// existing runtime-helper path (qRuntimeHelpers skip).
func classifyDeferredRecover(call *ast.CallExpr, alias string) (qSubCall, bool, error) {
	// Bare form: defer q.Recover()
	if isSelector(call.Fun, alias, "Recover") && len(call.Args) == 0 {
		return qSubCall{Family: familyRecoverAuto, OuterCall: call}, true, nil
	}
	// Chain form: defer q.RecoverE().Method(args)
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return qSubCall{}, false, nil
	}
	entry, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return qSubCall{}, false, nil
	}
	if !isSelector(entry.Fun, alias, "RecoverE") || len(entry.Args) != 0 {
		return qSubCall{}, false, nil
	}
	if !recoverEChainMethods[sel.Sel.Name] {
		return qSubCall{}, false, fmt.Errorf("q.RecoverE chain method %q not recognised; valid: Map, Err, ErrF, Wrap, Wrapf", sel.Sel.Name)
	}
	return qSubCall{
		Family:     familyRecoverEAuto,
		Method:     sel.Sel.Name,
		MethodArgs: call.Args,
		OuterCall:  call,
	}, true, nil
}

// isIndexedSelector reports whether expr has the shape
// `<alias>.<name>[<typeArg>]` (a generic call with an explicit type
// argument). Returns the type-argument expression plus ok=true on
// match, nil + false otherwise. Handles the single-type-arg case
// only — q.As[T](x), q.AsE[T](x).
func isIndexedSelector(expr ast.Expr, alias, name string) (ast.Expr, bool) {
	ix, ok := expr.(*ast.IndexExpr)
	if !ok {
		return nil, false
	}
	if !isSelector(ix.X, alias, name) {
		return nil, false
	}
	return ix.Index, true
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
