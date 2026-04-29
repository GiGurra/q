package preprocessor

// escape.go — resource-escape detection. Catches the use-after-close
// pattern where a function returns (or otherwise lets escape) a value
// it has marked for closure. Three "death events" are recognised:
//
//  1. q.Open(...).DeferCleanup(...) — auto-deferred close registered by
//     the rewriter. The bound value is dead from the assign line
//     onward, since the deferred cleanup fires when the function
//     returns. q.Open(...).NoDeferCleanup() is the explicit "caller takes
//     ownership" form and is never added to the dead set.
//
//  2. `defer <bind>.Close()` — explicit user-written defer. The
//     deferred Close fires at function exit, so the bound value is
//     dead at every return.
//
//  3. `<bind>.Close()` — synchronous close. The value is dead from
//     this line onward (in source order).
//
// `close(ch)` and `defer close(ch)` (the channel close builtin) are
// NOT death events. A closed channel is still usable for receives —
// the `close(ch); return ch` pattern is idiomatic for "factory of a
// finite channel". Detection of channel use-after-close would need
// type info plus distinguishing send-after-close from receive-after-
// close, which is outside the scope of this pass.
//
// One-hop alias tracking: a `c2 := c` (or `var c2 = c`) where c is in
// the dead set propagates the dead state to c2. Deeper indirection
// (passing through function calls, returning from inner closures) is
// out of scope.
//
// Escape positions flagged:
//   - return <bind>
//   - go <call>(<bind>)
//   - defer <call>(<bind>) — except the very call that made it dead
//   - field / global / map / index store: <expr> = <bind>
//   - channel send: ch <- <bind>
//
// Plain function calls (`process(<bind>)`) are NOT flagged — the call
// returns before the deferred close fires, and we don't try to track
// stashing through the callee. Documented false-positive frontier:
// any function COULD stash <bind> globally; we don't try to detect
// that case.
//
// Opt-out: a function with a `//q:no-escape-check` doc comment is
// skipped entirely. Primarily for tests of q.Open's mechanism that
// intentionally factory out a closed resource so the caller can
// probe its post-close state. Real users should not need this.

import (
	"fmt"
	"go/ast"
	"go/token"
)

// checkResourceEscapes runs the resource-escape detection pass over
// every function body in `files`. Returns a list of diagnostics for
// patterns that look like a use-after-close — see this file's header
// for the full set.
func checkResourceEscapes(fset *token.FileSet, files []*ast.File, shapes []callShape) []Diagnostic {
	autoCloseOpens := collectAutoCloseOpens(shapes)

	var diags []Diagnostic
	for _, file := range files {
		path := fileName(fset, file)
		// Top-level FuncDecls + every nested FuncLit. Stopping descent
		// at FuncDecl/FuncLit nodes keeps each function body scanned
		// exactly once — walkFuncBody recurses internally so an
		// inner FuncLit will be reached the next time ast.Inspect
		// visits it as a sibling node.
		ast.Inspect(file, func(n ast.Node) bool {
			switch fn := n.(type) {
			case *ast.FuncDecl:
				if fn.Body != nil && !hasNoEscapeCheckDirective(fn.Doc) {
					diags = append(diags, walkFuncBody(fset, path, fn.Body, autoCloseOpens)...)
				}
				return false
			case *ast.FuncLit:
				if fn.Body != nil {
					diags = append(diags, walkFuncBody(fset, path, fn.Body, autoCloseOpens)...)
				}
				return false
			}
			return true
		})
	}
	return diags
}

// hasNoEscapeCheckDirective reports whether the doc comment carries
// a `//q:no-escape-check` directive (anywhere, on any line). Used to
// opt a function out of resource-escape detection — see api/open.md.
// Primarily for tests of q's mechanism that intentionally factory-out
// a closed resource so the caller can probe its close-state.
func hasNoEscapeCheckDirective(doc *ast.CommentGroup) bool {
	if doc == nil {
		return false
	}
	for _, c := range doc.List {
		// Tolerate either `// q:no-escape-check` or `//q:no-escape-check`.
		text := c.Text
		if len(text) >= 2 {
			text = text[2:]
		}
		for _, ch := range []string{"q:no-escape-check", " q:no-escape-check"} {
			if len(text) >= len(ch) && text[:len(ch)] == ch {
				return true
			}
		}
	}
	return false
}

// collectAutoCloseOpens maps each q.Open(...).DeferCleanup(...) call site
// (statement position) to the LHS identifier name. Only formDefine
// shapes whose DeferCleanup leg registers a deferred close (explicit
// CleanupArg or InferCleanup) are included; .NoDeferCleanup() forms are
// skipped — they don't make the binding dead.
func collectAutoCloseOpens(shapes []callShape) map[token.Pos]string {
	out := map[token.Pos]string{}
	for _, sh := range shapes {
		if sh.Form != formDefine {
			continue
		}
		ident, ok := sh.LHSExpr.(*ast.Ident)
		if !ok {
			continue
		}
		for _, sc := range sh.Calls {
			if sc.Family != familyOpen && sc.Family != familyOpenE {
				continue
			}
			if sc.NoDeferCleanup {
				continue
			}
			if sc.ScopeArg != nil {
				// .WithScope hands the lifetime to the scope — the
				// resource may legitimately escape this function as
				// long as it stays alive only until the scope closes.
				continue
			}
			if sc.CleanupArg == nil && !sc.InferCleanup {
				continue
			}
			out[sh.Stmt.Pos()] = ident.Name
		}
	}
	return out
}

// fileName extracts the source file name for a parsed *ast.File.
func fileName(fset *token.FileSet, file *ast.File) string {
	if f := fset.File(file.Pos()); f != nil {
		return f.Name()
	}
	return ""
}

// walkFuncBody walks one function body in source order maintaining a
// dead-binding set, and flags references to any dead binding that
// appears in an escape position.
func walkFuncBody(fset *token.FileSet, path string, body *ast.BlockStmt, autoCloseOpens map[token.Pos]string) []Diagnostic {
	var diags []Diagnostic

	// dead[name] = description of the death event ("q.Open auto-defer",
	// "defer Close", "synchronous Close"). Presence ⇒ name is dead at
	// the current walking position.
	dead := map[string]string{}

	// deathSite[name] = the AST node that caused the death — used to
	// avoid flagging the death event's own call expression as an
	// escape (e.g. the `c` inside `c.Close()` is fine).
	deathSite := map[string]ast.Node{}

	walkStmts(fset, path, body.List, dead, deathSite, autoCloseOpens, &diags)
	return diags
}

// walkStmts processes a sequence of statements in source order. It
// recurses into nested blocks (if/for/switch/etc.) and FuncLits via
// the inner statement-shape handlers. The dead set is mutated as
// death events fire.
//
// Recursion model: for q.Open(...).DeferCleanup(...) the binding is dead
// the moment it's introduced — escape diagnostics fire on any
// reference anywhere in the remaining function body, including inside
// conditionals. The same applies to function-body-level
// `defer x.Close()` since the defer fires unconditionally at function
// exit. For synchronous `x.Close()` and conditional defers we'd need
// flow analysis to be precise, so this pass is intentionally
// conservative: a `Close` inside a conditional branch is recognised
// only when that branch is the body itself, not when nested deeper.
// Sticking to the always-dead cases keeps false positives near zero.
func walkStmts(fset *token.FileSet, path string, stmts []ast.Stmt, dead map[string]string, deathSite map[string]ast.Node, autoCloseOpens map[token.Pos]string, diags *[]Diagnostic) {
	for _, stmt := range stmts {
		// 1) Recognise death events first so subsequent statements
		//    see the updated dead set.
		recognizeDeath(stmt, dead, deathSite, autoCloseOpens)

		// 2) Then check this statement's identifiers for escapes.
		checkStmtForEscapes(fset, path, stmt, dead, deathSite, diags)

		// 3) Recurse into nested blocks / func lits inside this
		//    statement so we cover their statements too.
		recurseInto(fset, path, stmt, dead, deathSite, autoCloseOpens, diags)
	}
}

// recognizeDeath inspects a single statement for the three death-event
// shapes and updates the dead / deathSite maps.
func recognizeDeath(stmt ast.Stmt, dead map[string]string, deathSite map[string]ast.Node, autoCloseOpens map[token.Pos]string) {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		// q.Open(...).DeferCleanup(...) — keyed on stmt position.
		if name, ok := autoCloseOpens[stmt.Pos()]; ok {
			dead[name] = "q.Open(...).DeferCleanup(...) auto-defers cleanup"
			deathSite[name] = stmt
			return
		}
		// One-hop alias: `c2 := c` where c is dead.
		if (s.Tok == token.DEFINE || s.Tok == token.ASSIGN) &&
			len(s.Lhs) == 1 && len(s.Rhs) == 1 {
			rhsIdent, rhsOk := s.Rhs[0].(*ast.Ident)
			lhsIdent, lhsOk := s.Lhs[0].(*ast.Ident)
			if rhsOk && lhsOk {
				if reason, isDead := dead[rhsIdent.Name]; isDead && rhsIdent.Name != "_" {
					dead[lhsIdent.Name] = reason + " (alias of " + rhsIdent.Name + ")"
					deathSite[lhsIdent.Name] = stmt
				}
			}
		}
	case *ast.DeferStmt:
		if name := bindingClosedByCall(s.Call); name != "" {
			if _, alreadyDead := dead[name]; !alreadyDead {
				dead[name] = "deferred close registered here"
			}
			deathSite[name] = stmt
		}
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok {
			if name := bindingClosedByCall(call); name != "" {
				if _, alreadyDead := dead[name]; !alreadyDead {
					dead[name] = "synchronous close happened earlier in this function"
				}
				deathSite[name] = stmt
			}
		}
	}
}

// bindingClosedByCall reports the name of the binding closed by call,
// if call is a recognisable close shape. Returns "" otherwise.
//
// Recognised:
//   - <bind>.Close() (zero-arg method) → returns "bind"
//
// NOT recognised (intentional):
//   - close(ch) / defer close(ch) — closing a channel does NOT make it
//     unusable; receiving from a closed channel is idiomatic
//     "consume a finite stream". The pattern `close(ch); return ch`
//     is a legitimate finite-channel factory.
//   - cleanup(x) for arbitrary cleanup function names — too easy to
//     produce false positives without type info.
//
// Only the trivial single-ident-receiver shape is detected; closures
// over indirection (`fn(other.Field)`) are not matched.
func bindingClosedByCall(call *ast.CallExpr) string {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if sel.Sel != nil && sel.Sel.Name == "Close" {
			if recv, ok := sel.X.(*ast.Ident); ok && len(call.Args) == 0 {
				return recv.Name
			}
		}
	}
	return ""
}

// checkStmtForEscapes scans a single statement (without recursing
// into nested blocks) for references to a dead binding in escape
// positions, and appends diagnostics.
func checkStmtForEscapes(fset *token.FileSet, path string, stmt ast.Stmt, dead map[string]string, deathSite map[string]ast.Node, diags *[]Diagnostic) {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		for _, r := range s.Results {
			flagDirectIdent(fset, path, r, dead, "returned", diags)
		}
	case *ast.GoStmt:
		flagCallArgs(fset, path, s.Call, dead, "passed to a goroutine", diags)
	case *ast.DeferStmt:
		// Skip the very defer that made the binding dead.
		flagDeferEscape(fset, path, s, dead, deathSite, diags)
	case *ast.SendStmt:
		flagDirectIdent(fset, path, s.Value, dead, "sent on a channel", diags)
	case *ast.AssignStmt:
		// Stores to non-pure-local (selector / index / star) LHS.
		if s.Tok == token.ASSIGN || s.Tok == token.DEFINE {
			for i, rhs := range s.Rhs {
				if i >= len(s.Lhs) {
					break
				}
				if isLocalIdentLHS(s.Lhs[i]) {
					continue
				}
				flagDirectIdent(fset, path, rhs, dead, "stored into a non-local destination", diags)
			}
		}
	}
}

// isLocalIdentLHS reports whether the LHS expression is a plain
// identifier — i.e., a local-variable assignment or short-decl
// target. Selectors (struct field), index (map / slice element), and
// star-deref (pointer write) are non-local LHS shapes that the
// resource could outlive.
func isLocalIdentLHS(e ast.Expr) bool {
	_, ok := e.(*ast.Ident)
	return ok
}

// flagDirectIdent emits a diagnostic if e is a plain identifier that
// references a dead binding. Composite expressions like `c.Field`,
// `[]int{a, b}`, or `wrap(c)` are NOT flagged here — those would
// require tracking the value through field access / function calls,
// which is out of scope.
func flagDirectIdent(fset *token.FileSet, path string, e ast.Expr, dead map[string]string, action string, diags *[]Diagnostic) {
	id, ok := e.(*ast.Ident)
	if !ok {
		return
	}
	reason, isDead := dead[id.Name]
	if !isDead {
		return
	}
	*diags = append(*diags, diagAt(fset, path, id.Pos(), fmt.Sprintf(
		"%s is %s after its close was registered (%s); the caller would receive a closed/released resource", id.Name, action, reason)))
}

// flagCallArgs emits a diagnostic for any direct-ident argument
// passed to call when that ident is in the dead set.
func flagCallArgs(fset *token.FileSet, path string, call *ast.CallExpr, dead map[string]string, action string, diags *[]Diagnostic) {
	for _, a := range call.Args {
		flagDirectIdent(fset, path, a, dead, action, diags)
	}
}

// flagDeferEscape flags a `defer <call>(<bind>)` where <bind> is dead
// — UNLESS the defer is the very statement that registered <bind>'s
// death (in which case the defer IS the cleanup, not an escape).
func flagDeferEscape(fset *token.FileSet, path string, s *ast.DeferStmt, dead map[string]string, deathSite map[string]ast.Node, diags *[]Diagnostic) {
	closedHere := bindingClosedByCall(s.Call)
	for _, a := range s.Call.Args {
		id, ok := a.(*ast.Ident)
		if !ok {
			continue
		}
		if id.Name == closedHere && deathSite[id.Name] == s {
			continue // this defer IS the cleanup for id
		}
		reason, isDead := dead[id.Name]
		if !isDead {
			continue
		}
		*diags = append(*diags, diagAt(fset, path, id.Pos(), fmt.Sprintf(
			"%s is captured by a deferred call after its close was registered (%s); the deferred call would run on a closed/released resource", id.Name, reason)))
	}
	// Receiver-form `defer x.Method(...)` where x is dead.
	if sel, ok := s.Call.Fun.(*ast.SelectorExpr); ok {
		if recv, ok := sel.X.(*ast.Ident); ok {
			if recv.Name == closedHere && deathSite[recv.Name] == s {
				return
			}
			if reason, isDead := dead[recv.Name]; isDead {
				*diags = append(*diags, diagAt(fset, path, recv.Pos(), fmt.Sprintf(
					"%s is the receiver of a deferred method call after its close was registered (%s); the deferred call would run on a closed/released resource", recv.Name, reason)))
			}
		}
	}
}

// recurseInto descends into the nested blocks / FuncLits of stmt so
// the walk covers their bodies too. Each nested FuncLit gets its own
// fresh dead-set (handled at the FuncLit case in checkResourceEscapes
// via the outer walk loop) — closures don't inherit the outer dead
// set since their scope is independent.
func recurseInto(fset *token.FileSet, path string, stmt ast.Stmt, dead map[string]string, deathSite map[string]ast.Node, autoCloseOpens map[token.Pos]string, diags *[]Diagnostic) {
	// FuncLits inside this stmt get their own walk via the outer
	// ast.Inspect dispatch (in checkResourceEscapes); we shouldn't
	// re-walk them here. So short-circuit nested FuncLits.

	switch s := stmt.(type) {
	case *ast.BlockStmt:
		walkStmts(fset, path, s.List, dead, deathSite, autoCloseOpens, diags)
	case *ast.IfStmt:
		if s.Init != nil {
			walkStmts(fset, path, []ast.Stmt{s.Init}, dead, deathSite, autoCloseOpens, diags)
		}
		if s.Body != nil {
			walkStmts(fset, path, s.Body.List, dead, deathSite, autoCloseOpens, diags)
		}
		if s.Else != nil {
			switch e := s.Else.(type) {
			case *ast.BlockStmt:
				walkStmts(fset, path, e.List, dead, deathSite, autoCloseOpens, diags)
			case *ast.IfStmt:
				recurseInto(fset, path, e, dead, deathSite, autoCloseOpens, diags)
			}
		}
	case *ast.ForStmt:
		if s.Init != nil {
			walkStmts(fset, path, []ast.Stmt{s.Init}, dead, deathSite, autoCloseOpens, diags)
		}
		if s.Post != nil {
			walkStmts(fset, path, []ast.Stmt{s.Post}, dead, deathSite, autoCloseOpens, diags)
		}
		if s.Body != nil {
			walkStmts(fset, path, s.Body.List, dead, deathSite, autoCloseOpens, diags)
		}
	case *ast.RangeStmt:
		if s.Body != nil {
			walkStmts(fset, path, s.Body.List, dead, deathSite, autoCloseOpens, diags)
		}
	case *ast.SwitchStmt:
		if s.Body != nil {
			for _, c := range s.Body.List {
				if cc, ok := c.(*ast.CaseClause); ok {
					walkStmts(fset, path, cc.Body, dead, deathSite, autoCloseOpens, diags)
				}
			}
		}
	case *ast.TypeSwitchStmt:
		if s.Body != nil {
			for _, c := range s.Body.List {
				if cc, ok := c.(*ast.CaseClause); ok {
					walkStmts(fset, path, cc.Body, dead, deathSite, autoCloseOpens, diags)
				}
			}
		}
	case *ast.SelectStmt:
		if s.Body != nil {
			for _, c := range s.Body.List {
				if cc, ok := c.(*ast.CommClause); ok {
					walkStmts(fset, path, cc.Body, dead, deathSite, autoCloseOpens, diags)
				}
			}
		}
	case *ast.LabeledStmt:
		if s.Stmt != nil {
			walkStmts(fset, path, []ast.Stmt{s.Stmt}, dead, deathSite, autoCloseOpens, diags)
		}
	}
}
