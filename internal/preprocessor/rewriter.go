package preprocessor

// rewriter.go — emit replacement text for one recognised q.* call
// site, then splice all replacements into a copy of the source bytes
// to produce a rewritten file.
//
// The rewriter is purely textual once the AST scan has classified the
// shape: source bytes outside the matched statement spans stay
// byte-identical, so non-rewritten regions preserve gofmt-style
// formatting, and column offsets in any compile error from
// non-rewritten code remain accurate.
//
// Zero values come from the universal `*new(T)` form: `new(T)` returns
// a *T regardless of T, and `*` dereferences to T's zero value. This
// avoids per-type knowledge of zero-value spellings (`0` for ints, `""`
// for strings, `nil` for pointers, etc.) and works for user-defined
// types, generic types, and interfaces without special cases. The Go
// compiler folds `*new(T)` to a constant zero — the generated machine
// code is identical to a hand-written zero literal.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
)

// rewriteFile applies every shape's replacement to a copy of src and
// returns the rewritten bytes. Replacements are applied bottom-up so
// earlier offsets stay valid as later ones change the file length.
//
// alias is the local import name of pkg/q in this file; the rewriter
// appends `var _ = <alias>.ErrNil` at the end so the import does not
// become unused after the rewrites erase the only call sites that
// referenced it. The needsFmt / needsErrors flags accumulate over all
// rendered shapes so the import-injection passes run at most once per
// package; the returned addedImports lists the packages the rewriter
// actually injected (so the caller can extend the compile's
// -importcfg accordingly).
func rewriteFile(fset *token.FileSet, file *ast.File, src []byte, shapes []callShape, alias, origPath string) ([]byte, []string, error) {
	type edit struct {
		start, end int
		text       string
	}

	edits := make([]edit, 0, len(shapes))
	counter := 0
	needsFmt, needsErrors, needsContext := false, false, false
	for _, sh := range shapes {
		text, fmtUsed, errorsUsed, contextUsed, err := renderShape(fset, src, sh, &counter, alias)
		if err != nil {
			return nil, nil, err
		}
		if fmtUsed {
			needsFmt = true
		}
		if errorsUsed {
			needsErrors = true
		}
		if contextUsed {
			needsContext = true
		}
		start := fset.Position(sh.Stmt.Pos()).Offset
		end := fset.Position(sh.Stmt.End()).Offset
		// Append a //line directive after the replacement so that
		// source AFTER the rewritten statement maps to the correct
		// original line again. Without this, the extra lines the
		// rewrite injects would shift every downstream line number
		// in DWARF — breaking debugger breakpoints set against the
		// original source.
		//
		// Trailing newline is essential: the bytes immediately after
		// the rewritten stmt can include a same-line trailing
		// comment (`q.Check(...) // note`). Without the newline the
		// user's `// note` would end up on the same physical line as
		// the `//line` directive, making it "invalid line number"
		// to the go parser.
		if origPath != "" {
			afterLine := fset.Position(sh.Stmt.End()).Line + 1
			text = text + "\n//line " + origPath + ":" + strconv.Itoa(afterLine) + "\n"
		}
		edits = append(edits, edit{start: start, end: end, text: text})
	}

	// Signature rewrites for auto-Recover: one per unique enclosing
	// function. Keyed by *ast.FuncType pointer — every shape the
	// scanner produced for the same function shares the same
	// FuncType pointer.
	sigSeen := map[*ast.FuncType]bool{}
	for _, sh := range shapes {
		if !shapeNeedsSigCheck(sh) {
			continue
		}
		if sigSeen[sh.EnclosingFuncType] {
			continue
		}
		sigSeen[sh.EnclosingFuncType] = true
		_, newResultsText, needsRewrite, err := analyzeErrorReturn(fset, src, sh.EnclosingFuncType)
		if err != nil {
			return nil, nil, err
		}
		if !needsRewrite {
			continue
		}
		sigStart, sigEnd, ok := resultsSpan(fset, sh.EnclosingFuncType.Results)
		if !ok {
			continue
		}
		edits = append(edits, edit{start: sigStart, end: sigEnd, text: newResultsText})
	}

	// Apply statement-span edits bottom-up so earlier offsets do not
	// shift while later ones rewrite the file.
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })

	out := append([]byte(nil), src...)
	for _, e := range edits {
		out = append(out[:e.start], append([]byte(e.text), out[e.end:]...)...)
	}

	// log/slog is injected by syntactic detection — q.DebugSlogAttr
	// rewrites to a literal slog.Any call, so any shape containing
	// that family forces the import.
	needsSlog := false
	for _, sh := range shapes {
		for _, c := range sh.Calls {
			switch c.Family {
			case familyDebugSlogAttr, familySlogAttr, familySlogFile, familySlogLine, familySlogFileLine:
				needsSlog = true
			}
			if needsSlog {
				break
			}
		}
		if needsSlog {
			break
		}
	}

	var addedImports []string
	if needsFmt && !hasImport(file, "fmt") {
		out = ensureImport(file, fset, out, "fmt")
		addedImports = append(addedImports, "fmt")
	}
	if needsErrors && !hasImport(file, "errors") {
		out = ensureImport(file, fset, out, "errors")
		addedImports = append(addedImports, "errors")
	}
	if needsContext && !hasImport(file, "context") {
		out = ensureImport(file, fset, out, "context")
		addedImports = append(addedImports, "context")
	}
	if needsSlog && !hasImport(file, "log/slog") {
		out = ensureImport(file, fset, out, "log/slog")
		addedImports = append(addedImports, "log/slog")
	}

	if alias != "" {
		sentinel := fmt.Sprintf("\n\nvar _ = %s.ErrNil\n", alias)
		out = append(out, []byte(sentinel)...)
	}

	// Prepend a file-level //line directive so the compiler records
	// the user's original source path in DWARF rather than the
	// preprocessor's tempdir. Without this, IDE breakpoints set
	// against the user's file don't match the binary's debug info
	// and never fire. The directive says "the next physical line is
	// line 1 of origPath", so physical line 2 of the rewritten file
	// becomes logical line 1 of the original, and so on. Per-edit
	// //line directives after each rewrite keep the mapping aligned
	// where rewrites expand one logical line into several.
	if origPath != "" {
		prefix := []byte("//line " + origPath + ":1\n")
		out = append(prefix, out...)
	}

	return out, addedImports, nil
}

// renderShape produces the replacement text for one matched
// statement. It iterates sh.Calls — one sub-call for non-return
// forms, potentially many for formReturn (`return q.Try(a()) *
// q.Try(b()), nil`) — rendering each as a bind + bubble block and
// joining them with a newline + indent. For formReturn it then
// appends the reconstructed final return with every q.* call span
// substituted by its `_qTmp<N>` (or for in-place families, by their
// rewritten expression — DebugPrintln expands to DebugPrintlnAt(...)
// and DebugSlogAttr expands to slog.Any(...); neither produces a
// bubble and so doesn't allocate a temp).
//
// *counter is the running per-file counter source. renderShape
// allocates one increment per sub-call so every temp name
// (`_qErrN`, `_qTmpN`, `_qValN`, `_qRetN`) stays globally unique.
// Returns flags indicating whether fmt / errors are used by the
// replacement (the caller injects imports if so).
func renderShape(fset *token.FileSet, src []byte, sh callShape, counter *int, alias string) (string, bool, bool, bool, error) {
	if len(sh.Calls) == 0 {
		return "", false, false, false, fmt.Errorf("renderShape: shape has no sub-calls")
	}
	indent := indentOf(src, fset.Position(sh.Stmt.Pos()).Offset)

	// Render innermost-first: an outer q.* call's bind line embeds
	// the inner's `_qTmpN`, so the inner must be defined first.
	order := orderInnermostFirst(fset, sh.Calls)

	// Allocate counter values in render order so the generated code
	// reads top-to-bottom with _qTmp1, _qTmp2, … regardless of where
	// the original q.* calls sat in the source.
	counters := make([]int, len(sh.Calls))
	for _, idx := range order {
		*counter++
		counters[idx] = *counter
	}

	// Pre-compute each sub's replacement text so substituteSpans
	// (used by exprTextSubst, MethodArg text, finalStmtSuffix) can
	// slice directly. Most families map to "_qTmp<N>"; DebugPrintln
	// and DebugSlogAttr map to in-place expression replacements
	// (`q.DebugPrintlnAt(...)` / `slog.Any(...)`) so their spans
	// vanish without a temp.
	subTexts := make([]string, len(sh.Calls))
	for i := range sh.Calls {
		subTexts[i] = "_qTmp" + strconv.Itoa(counters[i])
	}
	// Second pass for in-place families so innerText can already
	// substitute non-in-place children.
	for i := range sh.Calls {
		switch sh.Calls[i].Family {
		case familyDebugPrintln:
			subTexts[i] = buildDebugPrintlnReplacement(fset, src, sh.Calls[i], sh.Calls, subTexts, alias)
		case familyDebugSlogAttr:
			subTexts[i] = buildDebugSlogAttrReplacement(fset, src, sh.Calls[i], sh.Calls, subTexts)
		case familySlogAttr:
			subTexts[i] = buildSlogAttrReplacement(fset, src, sh.Calls[i], sh.Calls, subTexts)
		case familySlogFile:
			subTexts[i] = buildSlogFileReplacement(fset, sh.Calls[i])
		case familySlogLine:
			subTexts[i] = buildSlogLineReplacement(fset, sh.Calls[i])
		case familySlogFileLine:
			subTexts[i] = buildSlogFileLineReplacement(fset, sh.Calls[i])
		case familyFile:
			subTexts[i] = buildFileReplacement(fset, sh.Calls[i])
		case familyLine:
			subTexts[i] = buildLineReplacement(fset, sh.Calls[i])
		case familyFileLine:
			subTexts[i] = buildFileLineReplacement(fset, sh.Calls[i])
		case familyExpr:
			subTexts[i] = buildExprReplacement(fset, src, sh.Calls[i])
		case familyEnumValues:
			subTexts[i] = buildEnumValuesReplacement(sh.Calls[i])
		case familyEnumNames:
			subTexts[i] = buildEnumNamesReplacement(sh.Calls[i])
		case familyEnumName:
			subTexts[i] = buildEnumNameReplacement(fset, src, sh.Calls[i], sh.Calls, subTexts)
		case familyEnumParse:
			subTexts[i] = buildEnumParseReplacement(fset, src, sh.Calls[i], sh.Calls, subTexts, alias)
		case familyEnumValid:
			subTexts[i] = buildEnumValidReplacement(fset, src, sh.Calls[i], sh.Calls, subTexts)
		case familyEnumOrdinal:
			subTexts[i] = buildEnumOrdinalReplacement(fset, src, sh.Calls[i], sh.Calls, subTexts)
		}
	}

	var (
		blocks                           []string
		fmtUsed, errorsUsed, contextUsed bool
	)
	allInPlace := true
	for _, idx := range order {
		if !isInPlaceFamily(sh.Calls[idx].Family) {
			allInPlace = false
		}
		block, fu, eu, cu, err := renderSubCall(fset, src, sh, idx, sh.Calls, counters, subTexts, alias)
		if err != nil {
			return "", false, false, false, err
		}
		if fu {
			fmtUsed = true
		}
		if eu {
			errorsUsed = true
		}
		if cu {
			contextUsed = true
		}
		if block != "" {
			blocks = append(blocks, block)
		}
	}
	text := joinWith(blocks, "\n"+indent)
	if sh.Form == formReturn || sh.Form == formHoist || allInPlace {
		if len(blocks) > 0 {
			text += finalStmtSuffix(fset, src, sh, subTexts)
		} else {
			// All-in-place shape in a value-producing form: emit
			// only the substituted statement body with the
			// original indent, no extra newline prefix.
			start := fset.Position(sh.Stmt.Pos()).Offset
			end := fset.Position(sh.Stmt.End()).Offset
			text = substituteSpans(fset, src, start, end, sh.Calls, subTexts)
		}
	}
	return text, fmtUsed, errorsUsed, contextUsed, nil
}

// renderAwait produces the replacement for bare q.Await. Identical
// to renderTry except the bind's RHS is `q.AwaitRaw(<fExpr>)` — the
// runtime helper that blocks on the Future and returns its
// captured (T, error) tuple.
func renderAwait(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, alias string, subs []qSubCall, subTexts []string) (string, error) {
	zeros, indent, errVar, _, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", err
	}
	fExpr := exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	innerText := fmt.Sprintf("%s.AwaitRaw(%s)", alias, fExpr)
	bindLine := tryBindLine(fset, src, sh, errVar, innerText, indent, counter)
	zeros[len(zeros)-1] = errVar
	return assembleErrBlock(bindLine, errVar, indent, zeros), nil
}

// renderAwaitE produces the replacement for q.AwaitE chains. Mirrors
// renderTryE's method dispatch with the AwaitRaw-wrapped inner text.
func renderAwaitE(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, alias string, subs []qSubCall, subTexts []string) (string, bool, error) {
	zeros, indent, errVar, _, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", false, err
	}
	fExpr := exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	innerText := fmt.Sprintf("%s.AwaitRaw(%s)", alias, fExpr)
	bindLine := tryBindLine(fset, src, sh, errVar, innerText, indent, counter)

	switch sub.Method {
	case "Err":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.AwaitE(...).Err requires exactly one argument (the replacement error); got %d", len(sub.MethodArgs))
		}
		zeros[len(zeros)-1] = exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		return assembleErrBlock(bindLine, errVar, indent, zeros), false, nil
	case "ErrF":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.AwaitE(...).ErrF requires exactly one argument (an error-transform fn); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf("(%s)(%s)", fn, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), false, nil
	case "Wrap":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.AwaitE(...).Wrap requires exactly one argument (the message string); got %d", len(sub.MethodArgs))
		}
		msg := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf(`fmt.Errorf("%%s: %%w", %s, %s)`, msg, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), true, nil
	case "Wrapf":
		if len(sub.MethodArgs) < 1 {
			return "", false, fmt.Errorf("q.AwaitE(...).Wrapf requires at least one argument (the format string); got %d", len(sub.MethodArgs))
		}
		formatExpr, ok := sub.MethodArgs[0].(*ast.BasicLit)
		if !ok || formatExpr.Kind != token.STRING {
			return "", false, fmt.Errorf("q.AwaitE(...).Wrapf's first argument must be a string literal so the rewriter can splice in `: %%w`")
		}
		raw := formatExpr.Value
		formatWithW := raw[:len(raw)-1] + `: %w` + `"`
		argParts := []string{formatWithW}
		for _, a := range sub.MethodArgs[1:] {
			argParts = append(argParts, exprTextSubst(fset, src, a, subs, subTexts))
		}
		argParts = append(argParts, errVar)
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s)", joinWith(argParts, ", "))
		return assembleErrBlock(bindLine, errVar, indent, zeros), true, nil
	case "Catch":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.AwaitE(...).Catch requires exactly one argument (a (T, error)-returning fn); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		retErrVar := fmt.Sprintf("_qRet%d", counter)
		zeros[len(zeros)-1] = retErrVar
		recoveryLHS := lhsTextOrUnderscore(fset, src, sh, counter)
		return assembleCatchErrBlock(bindLine, recoveryLHS, errVar, retErrVar, fn, indent, zeros), false, nil
	}
	return "", false, fmt.Errorf("renderAwaitE: unknown method %q", sub.Method)
}

// shapeNeedsSigCheck reports whether any sub-call in sh is an auto-
// Recover form that might require the enclosing function's result
// list to be renamed. Used to seed the per-function signature
// rewrite pass.
func shapeNeedsSigCheck(sh callShape) bool {
	for _, sub := range sh.Calls {
		if sub.Family == familyRecoverAuto || sub.Family == familyRecoverEAuto {
			return true
		}
	}
	return false
}

// resultsSpan returns the byte offsets of the source span that the
// signature rewrite should replace. Two shapes:
//
//   - Parenthesised: `func f() (a, b int, error)` — start at `(`,
//     end just past `)`.
//   - Unparenthesised (single unnamed result): `func f() error` —
//     start at the type's Pos, end at its End.
//
// ok=false when there is no result list to rewrite (shouldn't
// happen: analyzeErrorReturn would have errored earlier).
func resultsSpan(fset *token.FileSet, results *ast.FieldList) (start, end int, ok bool) {
	if results == nil || len(results.List) == 0 {
		return 0, 0, false
	}
	if results.Opening.IsValid() && results.Closing.IsValid() {
		return fset.Position(results.Opening).Offset, fset.Position(results.Closing).Offset + 1, true
	}
	// No parens — single unnamed field.
	field := results.List[0]
	return fset.Position(field.Type.Pos()).Offset, fset.Position(field.Type.End()).Offset, true
}

// renderRecoverAuto produces the replacement for `defer q.Recover()`.
// Looks up the enclosing function's error-slot name (user-supplied
// or generated), emits `defer <alias>.Recover(&<name>)`. The sub
// arg carries no useful data for this family — all information is
// pulled from the enclosing FuncType — so it's underscored.
func renderRecoverAuto(fset *token.FileSet, src []byte, sh callShape, _ qSubCall, alias string) (string, error) {
	errName, _, _, err := analyzeErrorReturn(fset, src, sh.EnclosingFuncType)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("defer %s.Recover(&%s)", alias, errName), nil
}

// renderRecoverEAuto produces the replacement for
// `defer q.RecoverE().Method(args)`. Rebuilds the chain with
// `&<errName>` as the RecoverE argument; method args are spliced
// verbatim (with nested q.* substitutions applied).
func renderRecoverEAuto(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, alias string, subs []qSubCall, subTexts []string) (string, error) {
	errName, _, _, err := analyzeErrorReturn(fset, src, sh.EnclosingFuncType)
	if err != nil {
		return "", err
	}
	var argParts []string
	for _, a := range sub.MethodArgs {
		argParts = append(argParts, exprTextSubst(fset, src, a, subs, subTexts))
	}
	return fmt.Sprintf("defer %s.RecoverE(&%s).%s(%s)", alias, errName, sub.Method, joinWith(argParts, ", ")), nil
}

// analyzeErrorReturn inspects the enclosing function's Results list
// and returns (errName, newResultsText, needsSigRewrite, err):
//
//   - errName: the name the deferred call should pass via &errName.
//     Reuses the user's name if they already wrote one; otherwise
//     "_qErr".
//   - newResultsText: the full "(…)" replacement to splice into the
//     signature when needsSigRewrite is true. Empty when no rewrite
//     is needed (all slots already named).
//   - needsSigRewrite: true when at least one result is unnamed —
//     Go requires all-or-nothing on named results, so we must name
//     every slot if the error slot wasn't named by the user.
//
// Returns a diagnostic error when the enclosing function has no
// return list, or when the last return isn't exactly the builtin
// `error` interface (passing `&err` of any other type to
// q.Recover's `*error` parameter would be a type mismatch).
func analyzeErrorReturn(fset *token.FileSet, src []byte, fnType *ast.FuncType) (errName, newResultsText string, needsSigRewrite bool, err error) {
	results := fnType.Results
	if results == nil || results.NumFields() == 0 {
		return "", "", false, fmt.Errorf("q.Recover()/RecoverE() used in a function with no return values; place it in a function returning `error` (or `(T, error)`)")
	}
	lastField := results.List[len(results.List)-1]
	if !isBuiltinErrorType(lastField.Type) {
		return "", "", false, fmt.Errorf("q.Recover()/RecoverE() requires the last return parameter to be the built-in `error`; got %s", exprText(fset, src, lastField.Type))
	}
	// Already has a name? Reuse. The last field is the error slot;
	// if it has Names, use the last entry (covers the unusual
	// `(a, b error)` shape too).
	if n := len(lastField.Names); n > 0 {
		return lastField.Names[n-1].Name, "", false, nil
	}
	// No name on the error slot → Go requires every slot to be
	// named or none to be named, so rewrite the entire Results.
	// Generate `_qRetN` for unnamed non-error slots and `_qErr` for
	// the error slot. Preserve existing names verbatim where
	// already supplied (this field path is only reachable when the
	// error slot is unnamed, which in valid Go means all slots are
	// unnamed — but we preserve any existing names defensively).
	var parts []string
	slotIdx := 0
	for fieldIdx, f := range results.List {
		isLast := fieldIdx == len(results.List)-1
		typeText := exprText(fset, src, f.Type)
		if len(f.Names) == 0 {
			var name string
			if isLast {
				name = "_qErr"
			} else {
				name = fmt.Sprintf("_qRet%d", slotIdx)
			}
			parts = append(parts, name+" "+typeText)
			slotIdx++
		} else {
			names := make([]string, len(f.Names))
			for i, n := range f.Names {
				names[i] = n.Name
			}
			parts = append(parts, joinWith(names, ", ")+" "+typeText)
			slotIdx += len(f.Names)
		}
	}
	return "_qErr", "(" + joinWith(parts, ", ") + ")", true, nil
}

// isBuiltinErrorType reports whether t is the plain identifier
// `error` (the universe-scope interface). Concrete error types
// (`MyErr`, `*MyErr`, etc.) are rejected — `&err` of any non-error
// type would be a type mismatch against q.Recover's `*error`
// parameter, and the typed-nil-interface pitfall would also apply.
func isBuiltinErrorType(t ast.Expr) bool {
	id, ok := t.(*ast.Ident)
	return ok && id.Name == "error"
}

// debugLabel constructs the auto-generated label string used by
// q.DebugPrintln / q.DebugSlogAttr — `<file>:<line> <src>` with the
// argument's source text appended after the call-site location.
// Returns the Go-quoted label literal ready to splice into a call.
func debugLabel(fset *token.FileSet, src []byte, sub qSubCall) string {
	innerStart := fset.Position(sub.InnerExpr.Pos()).Offset
	innerEnd := fset.Position(sub.InnerExpr.End()).Offset
	srcText := string(src[innerStart:innerEnd])
	prefix := tracePrefix(fset, sub.OuterCall.Pos())
	return strconv.Quote(prefix + " " + srcText)
}

// buildDebugPrintlnReplacement is the per-sub replacement text for a
// q.DebugPrintln call: `<alias>.DebugPrintlnAt("<label>", <innerText>)`.
// innerText is computed by substituteSpans so any non-in-place q.*
// nested inside DebugPrintln's argument already maps to its `_qTmpN`.
func buildDebugPrintlnReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string, alias string) string {
	innerStart := fset.Position(sub.InnerExpr.Pos()).Offset
	innerEnd := fset.Position(sub.InnerExpr.End()).Offset
	innerText := substituteSpans(fset, src, innerStart, innerEnd, subs, subTexts)
	return fmt.Sprintf("%s.DebugPrintlnAt(%s, %s)", alias, debugLabel(fset, src, sub), innerText)
}

// buildDebugSlogAttrReplacement is the per-sub replacement text for
// a q.DebugSlogAttr call: `slog.Any("<label>", <innerText>)`.
// Expands directly to stdlib slog.Any — no q runtime helper is
// involved on the value path. The rewriter injects the `log/slog`
// import elsewhere when this family appears.
func buildDebugSlogAttrReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string) string {
	innerStart := fset.Position(sub.InnerExpr.Pos()).Offset
	innerEnd := fset.Position(sub.InnerExpr.End()).Offset
	innerText := substituteSpans(fset, src, innerStart, innerEnd, subs, subTexts)
	return fmt.Sprintf("slog.Any(%s, %s)", debugLabel(fset, src, sub), innerText)
}

// buildSlogAttrReplacement is the per-sub replacement for q.SlogAttr:
// `slog.Any("<src>", <innerText>)` — keyed by the argument's literal
// source text only, no file:line prefix. Use it for production-style
// structured logging where the call site location isn't part of the
// log record.
func buildSlogAttrReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string) string {
	innerStart := fset.Position(sub.InnerExpr.Pos()).Offset
	innerEnd := fset.Position(sub.InnerExpr.End()).Offset
	srcText := string(src[innerStart:innerEnd])
	innerText := substituteSpans(fset, src, innerStart, innerEnd, subs, subTexts)
	return fmt.Sprintf("slog.Any(%s, %s)", strconv.Quote(srcText), innerText)
}

// buildSlogFileReplacement is the per-sub replacement for q.SlogFile:
// `slog.Any("file", "<basename>")`. The basename is captured from
// OuterCall's source position at compile time.
func buildSlogFileReplacement(fset *token.FileSet, sub qSubCall) string {
	pos := fset.Position(sub.OuterCall.Pos())
	return fmt.Sprintf("slog.Any(%s, %s)", strconv.Quote("file"), strconv.Quote(filepath.Base(pos.Filename)))
}

// buildSlogLineReplacement is the per-sub replacement for q.SlogLine:
// `slog.Any("line", <line-int>)`. The line is captured from
// OuterCall's source position at compile time.
func buildSlogLineReplacement(fset *token.FileSet, sub qSubCall) string {
	pos := fset.Position(sub.OuterCall.Pos())
	return fmt.Sprintf("slog.Any(%s, %d)", strconv.Quote("line"), pos.Line)
}

// buildSlogFileLineReplacement is the per-sub replacement for
// q.SlogFileLine: `slog.Any("file", "<basename>:<line>")`. Both
// pieces come from OuterCall's source position; the value is the
// concatenation that's standard in Go error / log output.
func buildSlogFileLineReplacement(fset *token.FileSet, sub qSubCall) string {
	pos := fset.Position(sub.OuterCall.Pos())
	value := fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line)
	return fmt.Sprintf("slog.Any(%s, %s)", strconv.Quote("file"), strconv.Quote(value))
}

// buildFileReplacement is the per-sub replacement for q.File: a
// raw string literal naming the basename of the call site's
// source file. Returns a Go-quoted literal ready to substitute.
func buildFileReplacement(fset *token.FileSet, sub qSubCall) string {
	pos := fset.Position(sub.OuterCall.Pos())
	return strconv.Quote(filepath.Base(pos.Filename))
}

// buildLineReplacement is the per-sub replacement for q.Line: the
// integer line number of the call site.
func buildLineReplacement(fset *token.FileSet, sub qSubCall) string {
	pos := fset.Position(sub.OuterCall.Pos())
	return strconv.Itoa(pos.Line)
}

// buildFileLineReplacement is the per-sub replacement for
// q.FileLine: a raw string literal of the form "basename:line".
func buildFileLineReplacement(fset *token.FileSet, sub qSubCall) string {
	pos := fset.Position(sub.OuterCall.Pos())
	return strconv.Quote(fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line))
}

// buildExprReplacement is the per-sub replacement for q.Expr: a
// Go-quoted string literal of the argument's literal source text.
// The argument's runtime value is discarded.
func buildExprReplacement(fset *token.FileSet, src []byte, sub qSubCall) string {
	innerStart := fset.Position(sub.InnerExpr.Pos()).Offset
	innerEnd := fset.Position(sub.InnerExpr.End()).Offset
	return strconv.Quote(string(src[innerStart:innerEnd]))
}

// isInPlaceFamily reports whether a family rewrites the call
// expression in place (no bind/check block, no return) so the
// substituted statement body is the entire output. Used to short-
// circuit block-emission and to decide between the bind-then-stmt
// shape and the substitute-only shape.
func isInPlaceFamily(f family) bool {
	switch f {
	case familyDebugPrintln, familyDebugSlogAttr,
		familySlogAttr, familySlogFile, familySlogLine, familySlogFileLine,
		familyFile, familyLine, familyFileLine, familyExpr,
		familyEnumValues, familyEnumNames, familyEnumName,
		familyEnumParse, familyEnumValid, familyEnumOrdinal:
		return true
	}
	return false
}

// orderInnermostFirst returns indices into subs ordered so that
// deeper-nested q.* calls come before their containers. Ties (subs
// at the same nesting depth) are broken by source position so the
// output reads in natural order.
func orderInnermostFirst(fset *token.FileSet, subs []qSubCall) []int {
	depth := make([]int, len(subs))
	for i, si := range subs {
		for j, sj := range subs {
			if i == j {
				continue
			}
			if spanContains(fset, sj.OuterCall, si.OuterCall) {
				depth[i]++
			}
		}
	}
	order := make([]int, len(subs))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ai, bi := order[a], order[b]
		if depth[ai] != depth[bi] {
			return depth[ai] > depth[bi]
		}
		return fset.Position(subs[ai].OuterCall.Pos()).Offset <
			fset.Position(subs[bi].OuterCall.Pos()).Offset
	})
	return order
}

// spanContains reports whether outer strictly contains inner (same
// span counts as non-containment).
func spanContains(fset *token.FileSet, outer, inner ast.Expr) bool {
	os := fset.Position(outer.Pos()).Offset
	oe := fset.Position(outer.End()).Offset
	is := fset.Position(inner.Pos()).Offset
	ie := fset.Position(inner.End()).Offset
	if os == is && oe == ie {
		return false
	}
	return os <= is && ie <= oe
}

// substituteSpans returns the source text of [start, end] with every
// sub whose OuterCall is an immediate child of that range replaced
// by subTexts[i] (its pre-computed replacement string). "Immediate
// child" means: contained by [start, end] and not contained by any
// other sub also contained by [start, end]. A sub whose span equals
// the [start, end] range exactly counts as a child (e.g. when an
// outer q.Try's InnerExpr IS the inner q.EnumParse call directly —
// composition like `q.Try(q.EnumParse[Color](s))`). Children are
// applied bottom-up in offset-descending order so earlier offsets
// stay valid.
//
// subTexts[i] is the replacement string for subs[i]. Callers
// pre-compute this in renderShape — most families map to
// "_qTmp<counter>", in-place families map to their rewritten
// expression (DebugPrintln → q.DebugPrintlnAt(...), DebugSlogAttr
// → slog.Any(...), q.EnumName → an IIFE switch).
func substituteSpans(fset *token.FileSet, src []byte, start, end int, subs []qSubCall, subTexts []string) string {
	var contained []int
	for i, sub := range subs {
		cs := fset.Position(sub.OuterCall.Pos()).Offset
		ce := fset.Position(sub.OuterCall.End()).Offset
		if cs < start || ce > end {
			continue
		}
		contained = append(contained, i)
	}
	var children []int
	for _, i := range contained {
		si := subs[i]
		isb := fset.Position(si.OuterCall.Pos()).Offset
		ieb := fset.Position(si.OuterCall.End()).Offset
		containedByOther := false
		for _, j := range contained {
			if i == j {
				continue
			}
			sj := subs[j]
			jsb := fset.Position(sj.OuterCall.Pos()).Offset
			jeb := fset.Position(sj.OuterCall.End()).Offset
			if jsb <= isb && ieb <= jeb && (jsb != isb || jeb != ieb) {
				containedByOther = true
				break
			}
		}
		if !containedByOther {
			children = append(children, i)
		}
	}
	sort.Slice(children, func(a, b int) bool {
		return fset.Position(subs[children[a]].OuterCall.Pos()).Offset >
			fset.Position(subs[children[b]].OuterCall.Pos()).Offset
	})
	text := []byte(string(src[start:end]))
	for _, i := range children {
		sub := subs[i]
		cs := fset.Position(sub.OuterCall.Pos()).Offset - start
		ce := fset.Position(sub.OuterCall.End()).Offset - start
		replacement := subTexts[i]
		text = append(text[:cs], append([]byte(replacement), text[ce:]...)...)
	}
	return string(text)
}

// exprTextSubst is exprText with nested q.* substitutions applied —
// used wherever a user-supplied expression might contain q.* calls
// that have been hoisted into their own binds. For locations known
// to be q.*-free (e.g. LHS on the direct-bind path), exprText
// suffices.
func exprTextSubst(fset *token.FileSet, src []byte, e ast.Expr, subs []qSubCall, subTexts []string) string {
	start := fset.Position(e.Pos()).Offset
	end := fset.Position(e.End()).Offset
	return substituteSpans(fset, src, start, end, subs, subTexts)
}

// renderSubCall dispatches one sub-call to the family-specific
// renderer. subs/subTexts are threaded through so each renderer can
// substitute nested q.* spans inside its own InnerExpr / MethodArgs
// text; counters carries the per-sub N so each renderer can name
// its own _qErrN / _qOkN / _qTmpN / etc.
func renderSubCall(fset *token.FileSet, src []byte, sh callShape, subIdx int, subs []qSubCall, counters []int, subTexts []string, alias string) (string, bool, bool, bool, error) {
	sub := subs[subIdx]
	counter := counters[subIdx]
	switch sub.Family {
	case familyTry:
		text, err := renderTry(fset, src, sh, sub, counter, subs, subTexts)
		return text, false, false, false, err
	case familyTryE:
		text, fmtUsed, errorsUsed, err := renderTryE(fset, src, sh, sub, counter, subs, subTexts)
		return text, fmtUsed, errorsUsed, false, err
	case familyNotNil:
		text, err := renderNotNil(fset, src, sh, sub, counter, alias, subs, subTexts)
		return text, false, false, false, err
	case familyNotNilE:
		text, fmtUsed, errorsUsed, err := renderNotNilE(fset, src, sh, sub, counter, subs, subTexts)
		return text, fmtUsed, errorsUsed, false, err
	case familyCheck:
		text, err := renderCheck(fset, src, sh, sub, counter, subs, subTexts)
		return text, false, false, false, err
	case familyCheckE:
		text, fmtUsed, err := renderCheckE(fset, src, sh, sub, counter, subs, subTexts)
		return text, fmtUsed, false, false, err
	case familyOpen, familyOpenE:
		text, fmtUsed, err := renderOpen(fset, src, sh, sub, counter, subs, subTexts)
		return text, fmtUsed, false, false, err
	case familyOk:
		text, err := renderOk(fset, src, sh, sub, counter, alias, subs, subTexts)
		return text, false, false, false, err
	case familyOkE:
		text, fmtUsed, errorsUsed, err := renderOkE(fset, src, sh, sub, counter, subs, subTexts)
		return text, fmtUsed, errorsUsed, false, err
	case familyTrace:
		text, err := renderTrace(fset, src, sh, sub, counter, subs, subTexts)
		return text, true, false, false, err
	case familyTraceE:
		text, err := renderTraceE(fset, src, sh, sub, counter, subs, subTexts)
		return text, true, false, false, err
	case familyLock:
		text, err := renderLock(fset, src, sh, sub, counter, subs, subTexts)
		return text, false, false, false, err
	case familyTODO:
		text, err := renderPanicMarker(fset, src, sh, sub, "q.TODO", subs, subTexts)
		return text, false, false, false, err
	case familyUnreachable:
		text, err := renderPanicMarker(fset, src, sh, sub, "q.Unreachable", subs, subTexts)
		return text, false, false, false, err
	case familyRequire:
		text, err := renderRequire(fset, src, sh, sub, counter, alias, subs, subTexts)
		return text, true, false, false, err
	case familyRecv:
		text, err := renderRecv(fset, src, sh, sub, counter, alias, subs, subTexts)
		return text, false, false, false, err
	case familyRecvE:
		text, fmtUsed, errorsUsed, err := renderRecvE(fset, src, sh, sub, counter, subs, subTexts)
		return text, fmtUsed, errorsUsed, false, err
	case familyAs:
		text, err := renderAs(fset, src, sh, sub, counter, alias, subs, subTexts)
		return text, false, false, false, err
	case familyAsE:
		text, fmtUsed, errorsUsed, err := renderAsE(fset, src, sh, sub, counter, subs, subTexts)
		return text, fmtUsed, errorsUsed, false, err
	case familyDebugPrintln, familyDebugSlogAttr,
		familySlogAttr, familySlogFile, familySlogLine, familySlogFileLine,
		familyFile, familyLine, familyFileLine, familyExpr,
		familyEnumValues, familyEnumNames, familyEnumName,
		familyEnumValid, familyEnumOrdinal:
		// In-place expression transforms — the replacement text
		// lives in subTexts[subIdx] and is applied when
		// substituteSpans rebuilds the final stmt. No bind/check
		// block to emit here.
		return "", false, false, false, nil
	case familyEnumParse:
		// Same as the other in-place families, but the rewritten
		// expression contains `fmt.Errorf(...)` for the unknown-name
		// branch, so we flip fmtUsed to trigger the import injection.
		return "", true, false, false, nil
	case familyAwait:
		text, err := renderAwait(fset, src, sh, sub, counter, alias, subs, subTexts)
		return text, false, false, false, err
	case familyAwaitE:
		text, fmtUsed, err := renderAwaitE(fset, src, sh, sub, counter, alias, subs, subTexts)
		return text, fmtUsed, false, false, err
	case familyRecoverAuto:
		text, err := renderRecoverAuto(fset, src, sh, sub, alias)
		return text, false, false, false, err
	case familyRecoverEAuto:
		text, err := renderRecoverEAuto(fset, src, sh, sub, alias, subs, subTexts)
		return text, false, false, false, err
	case familyCheckCtx:
		text, err := renderCheckCtx(fset, src, sh, sub, counter, subs, subTexts)
		return text, false, false, false, err
	case familyCheckCtxE:
		text, fmtUsed, err := renderCheckCtxE(fset, src, sh, sub, counter, subs, subTexts)
		return text, fmtUsed, false, false, err
	case familyRecvCtx:
		text, err := renderRecvCtx(fset, src, sh, sub, counter, alias, subs, subTexts)
		return text, false, false, false, err
	case familyRecvCtxE:
		text, fmtUsed, err := renderRecvCtxE(fset, src, sh, sub, counter, alias, subs, subTexts)
		return text, fmtUsed, false, false, err
	case familyAwaitCtx:
		text, err := renderAwaitCtx(fset, src, sh, sub, counter, alias, subs, subTexts)
		return text, false, false, false, err
	case familyAwaitCtxE:
		text, fmtUsed, err := renderAwaitCtxE(fset, src, sh, sub, counter, alias, subs, subTexts)
		return text, fmtUsed, false, false, err
	case familyTimeout:
		text, err := renderTimeoutDeadline(fset, src, sh, sub, counter, subs, subTexts, "WithTimeout")
		return text, false, false, true, err
	case familyDeadline:
		text, err := renderTimeoutDeadline(fset, src, sh, sub, counter, subs, subTexts, "WithDeadline")
		return text, false, false, true, err
	case familyAwaitAll:
		text, err := renderTryLikeWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "AwaitAllRaw", sub, subs, subTexts))
		return text, false, false, false, err
	case familyAwaitAllE:
		text, fmtUsed, err := renderTryLikeEWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "AwaitAllRaw", sub, subs, subTexts), "q.AwaitAllE", subs, subTexts)
		return text, fmtUsed, false, false, err
	case familyAwaitAllCtx:
		text, err := renderTryLikeWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "AwaitAllRawCtx", sub, subs, subTexts))
		return text, false, false, false, err
	case familyAwaitAllCtxE:
		text, fmtUsed, err := renderTryLikeEWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "AwaitAllRawCtx", sub, subs, subTexts), "q.AwaitAllCtxE", subs, subTexts)
		return text, fmtUsed, false, false, err
	case familyAwaitAny:
		text, err := renderTryLikeWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "AwaitAnyRaw", sub, subs, subTexts))
		return text, false, false, false, err
	case familyAwaitAnyE:
		text, fmtUsed, err := renderTryLikeEWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "AwaitAnyRaw", sub, subs, subTexts), "q.AwaitAnyE", subs, subTexts)
		return text, fmtUsed, false, false, err
	case familyAwaitAnyCtx:
		text, err := renderTryLikeWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "AwaitAnyRawCtx", sub, subs, subTexts))
		return text, false, false, false, err
	case familyAwaitAnyCtxE:
		text, fmtUsed, err := renderTryLikeEWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "AwaitAnyRawCtx", sub, subs, subTexts), "q.AwaitAnyCtxE", subs, subTexts)
		return text, fmtUsed, false, false, err
	case familyRecvAny:
		text, err := renderTryLikeWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "RecvAnyRaw", sub, subs, subTexts))
		return text, false, false, false, err
	case familyRecvAnyE:
		text, fmtUsed, err := renderTryLikeEWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "RecvAnyRaw", sub, subs, subTexts), "q.RecvAnyE", subs, subTexts)
		return text, fmtUsed, false, false, err
	case familyRecvAnyCtx:
		text, err := renderTryLikeWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "RecvAnyRawCtx", sub, subs, subTexts))
		return text, false, false, false, err
	case familyRecvAnyCtxE:
		text, fmtUsed, err := renderTryLikeEWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "RecvAnyRawCtx", sub, subs, subTexts), "q.RecvAnyCtxE", subs, subTexts)
		return text, fmtUsed, false, false, err
	case familyDrainCtx:
		text, err := renderTryLikeWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "DrainRawCtx", sub, subs, subTexts))
		return text, false, false, false, err
	case familyDrainCtxE:
		text, fmtUsed, err := renderTryLikeEWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "DrainRawCtx", sub, subs, subTexts), "q.DrainCtxE", subs, subTexts)
		return text, fmtUsed, false, false, err
	case familyDrainAllCtx:
		text, err := renderTryLikeWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "DrainAllRawCtx", sub, subs, subTexts))
		return text, false, false, false, err
	case familyDrainAllCtxE:
		text, fmtUsed, err := renderTryLikeEWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "DrainAllRawCtx", sub, subs, subTexts), "q.DrainAllCtxE", subs, subTexts)
		return text, fmtUsed, false, false, err
	}
	return "", false, false, false, fmt.Errorf("renderSubCall: unknown family %v", sub.Family)
}

// renderCheck produces the replacement for bare q.Check. Bind line
// is `_qErrN := <inner>` (no T to bind) and the bubble is the
// captured err itself.
func renderCheck(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string) (string, error) {
	zeros, indent, errVar, innerText, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", err
	}
	bindLine := fmt.Sprintf("%s := %s", errVar, innerText)
	zeros[len(zeros)-1] = errVar
	return assembleErrBlock(bindLine, errVar, indent, zeros), nil
}

// renderCheckE produces the replacement for the q.CheckE chain. Same
// bubble-expression vocabulary as TryE, but no value is threaded —
// the bind line captures only the error.
func renderCheckE(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string) (string, bool, error) {
	zeros, indent, errVar, innerText, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", false, err
	}
	bindLine := fmt.Sprintf("%s := %s", errVar, innerText)

	switch sub.Method {
	case "Err":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.CheckE(...).Err requires exactly one argument (the replacement error); got %d", len(sub.MethodArgs))
		}
		zeros[len(zeros)-1] = exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		return assembleErrBlock(bindLine, errVar, indent, zeros), false, nil
	case "ErrF":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.CheckE(...).ErrF requires exactly one argument (an error-transform fn); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf("(%s)(%s)", fn, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), false, nil
	case "Wrap":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.CheckE(...).Wrap requires exactly one argument (the message string); got %d", len(sub.MethodArgs))
		}
		msg := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf(`fmt.Errorf("%%s: %%w", %s, %s)`, msg, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), true, nil
	case "Wrapf":
		if len(sub.MethodArgs) < 1 {
			return "", false, fmt.Errorf("q.CheckE(...).Wrapf requires at least one argument (the format string); got %d", len(sub.MethodArgs))
		}
		formatExpr, ok := sub.MethodArgs[0].(*ast.BasicLit)
		if !ok || formatExpr.Kind != token.STRING {
			return "", false, fmt.Errorf("q.CheckE(...).Wrapf's first argument must be a string literal so the rewriter can splice in `: %%w`")
		}
		raw := formatExpr.Value
		formatWithW := raw[:len(raw)-1] + `: %w` + `"`
		argParts := []string{formatWithW}
		for _, a := range sub.MethodArgs[1:] {
			argParts = append(argParts, exprTextSubst(fset, src, a, subs, subTexts))
		}
		argParts = append(argParts, errVar)
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s)", joinWith(argParts, ", "))
		return assembleErrBlock(bindLine, errVar, indent, zeros), true, nil
	case "Catch":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.CheckE(...).Catch requires exactly one argument (a func(error) error); got %d", len(sub.MethodArgs))
		}
		// Catch for Check: fn returns error alone. nil = suppress
		// (fall through past the block), non-nil = bubble.
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		retErrVar := fmt.Sprintf("_qRet%d", counter)
		zeros[len(zeros)-1] = retErrVar
		var b bytes.Buffer
		b.WriteString(bindLine)
		b.WriteByte('\n')
		fmt.Fprintf(&b, "%sif %s != nil {\n", indent, errVar)
		fmt.Fprintf(&b, "%s\t%s := (%s)(%s)\n", indent, retErrVar, fn, errVar)
		fmt.Fprintf(&b, "%s\tif %s != nil {\n", indent, retErrVar)
		fmt.Fprintf(&b, "%s\t\treturn %s\n", indent, joinWith(zeros, ", "))
		fmt.Fprintf(&b, "%s\t}\n", indent)
		fmt.Fprintf(&b, "%s}", indent)
		return b.String(), false, nil
	}
	return "", false, fmt.Errorf("renderCheckE: unknown method %q", sub.Method)
}

// renderOpen produces the replacement for q.Open / q.OpenE chains
// terminated by .Release. Shares the TryE shape-method vocabulary
// for the bubble branch, and appends a `defer (cleanup)(resource)`
// line on the success path so the cleanup fires when the enclosing
// function returns.
func renderOpen(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string) (string, bool, error) {
	zeros, indent, errVar, innerText, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", false, err
	}
	bindLine := openBindLine(fset, src, sh, errVar, innerText, indent, counter)
	valueVar := openValueVar(fset, src, sh, counter)

	var (
		block   string
		fmtUsed bool
	)
	switch sub.Method {
	case "":
		zeros[len(zeros)-1] = errVar
		block = assembleErrBlock(bindLine, errVar, indent, zeros)
	case "Err":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.OpenE(...).Err requires exactly one argument (the replacement error); got %d", len(sub.MethodArgs))
		}
		zeros[len(zeros)-1] = exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		block = assembleErrBlock(bindLine, errVar, indent, zeros)
	case "ErrF":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.OpenE(...).ErrF requires exactly one argument (an error-transform fn); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf("(%s)(%s)", fn, errVar)
		block = assembleErrBlock(bindLine, errVar, indent, zeros)
	case "Wrap":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.OpenE(...).Wrap requires exactly one argument (the message string); got %d", len(sub.MethodArgs))
		}
		msg := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf(`fmt.Errorf("%%s: %%w", %s, %s)`, msg, errVar)
		fmtUsed = true
		block = assembleErrBlock(bindLine, errVar, indent, zeros)
	case "Wrapf":
		if len(sub.MethodArgs) < 1 {
			return "", false, fmt.Errorf("q.OpenE(...).Wrapf requires at least one argument (the format string); got %d", len(sub.MethodArgs))
		}
		formatExpr, ok := sub.MethodArgs[0].(*ast.BasicLit)
		if !ok || formatExpr.Kind != token.STRING {
			return "", false, fmt.Errorf("q.OpenE(...).Wrapf's first argument must be a string literal so the rewriter can splice in `: %%w`")
		}
		raw := formatExpr.Value
		formatWithW := raw[:len(raw)-1] + `: %w` + `"`
		argParts := []string{formatWithW}
		for _, a := range sub.MethodArgs[1:] {
			argParts = append(argParts, exprTextSubst(fset, src, a, subs, subTexts))
		}
		argParts = append(argParts, errVar)
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s)", joinWith(argParts, ", "))
		fmtUsed = true
		block = assembleErrBlock(bindLine, errVar, indent, zeros)
	case "Catch":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.OpenE(...).Catch requires exactly one argument (a func(error) (T, error)); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		retErrVar := fmt.Sprintf("_qRet%d", counter)
		zeros[len(zeros)-1] = retErrVar
		// Recovery rebinds valueVar so the deferred cleanup
		// later fires on the recovered value.
		block = assembleCatchErrBlock(bindLine, valueVar, errVar, retErrVar, fn, indent, zeros)
	default:
		return "", false, fmt.Errorf("renderOpen: unknown method %q", sub.Method)
	}

	if sub.NoRelease {
		// .NoRelease() — bubble check only, no defer cleanup.
		return block, fmtUsed, nil
	}
	var deferLine string
	if sub.AutoRelease {
		text, err := autoReleaseDeferLine(sub, valueVar)
		if err != nil {
			return "", false, err
		}
		deferLine = text
	} else {
		cleanupText := exprTextSubst(fset, src, sub.ReleaseArg, subs, subTexts)
		deferLine = fmt.Sprintf("defer (%s)(%s)", cleanupText, valueVar)
	}
	return block + "\n" + indent + deferLine, fmtUsed, nil
}

// autoReleaseDeferLine returns the defer line for a zero-arg
// .Release() call, dispatching on the cleanup form the typecheck
// pass inferred. cleanupUnknown is the "typecheck didn't run"
// (no importcfg) fallback: emit no defer line. In real builds the
// typecheck pass either runs and resolves a kind, or surfaces a
// diagnostic that aborts before the rewriter is invoked.
func autoReleaseDeferLine(sub qSubCall, valueVar string) (string, error) {
	switch sub.AutoCleanup {
	case cleanupChanClose:
		return fmt.Sprintf("defer close(%s)", valueVar), nil
	case cleanupCloseVoid:
		return fmt.Sprintf("defer %s.Close()", valueVar), nil
	case cleanupCloseErr:
		return fmt.Sprintf("defer func() { _ = %s.Close() }()", valueVar), nil
	}
	return "", nil
}

// openBindLine mirrors tryBindLine but always binds to a named
// variable for formDiscard — Open needs a target to pass to the
// deferred cleanup, so `_, _qErrN := …` (which Try uses) won't do.
func openBindLine(fset *token.FileSet, src []byte, sh callShape, errVar, innerText, indent string, counter int) string {
	switch sh.Form {
	case formDefine:
		if isBlankIdent(sh.LHSExpr) {
			// `_ := q.Open(...)` — bind to a real temp so the
			// defer-cleanup line has something to reference.
			return fmt.Sprintf("_qTmp%d, %s := %s", counter, errVar, innerText)
		}
		return fmt.Sprintf("%s, %s := %s", exprText(fset, src, sh.LHSExpr), errVar, innerText)
	case formAssign:
		if isBlankIdent(sh.LHSExpr) {
			return fmt.Sprintf("_qTmp%d, %s := %s", counter, errVar, innerText)
		}
		return fmt.Sprintf("var %s error\n%s%s, %s = %s", errVar, indent, exprText(fset, src, sh.LHSExpr), errVar, innerText)
	case formDiscard, formReturn, formHoist:
		return fmt.Sprintf("_qTmp%d, %s := %s", counter, errVar, innerText)
	}
	return fmt.Sprintf("/* unsupported form %v */", sh.Form)
}

// openValueVar returns the name of the bound resource variable for
// this Open sub-call. Used to spell the deferred cleanup arg and
// (for Catch) the recovery LHS.
//
// formDefine / formAssign normally reuse the user-visible LHS. The
// one exception is `_ = q.Open(...)` (assign-to-blank): `_` is not
// a usable identifier in `defer cleanup(_)`. Fall through to the
// temp-var path for that case so the defer wires to a real local.
func openValueVar(fset *token.FileSet, src []byte, sh callShape, counter int) string {
	switch sh.Form {
	case formDefine, formAssign:
		if isBlankIdent(sh.LHSExpr) {
			return fmt.Sprintf("_qTmp%d", counter)
		}
		return exprText(fset, src, sh.LHSExpr)
	default:
		return fmt.Sprintf("_qTmp%d", counter)
	}
}

// isBlankIdent reports whether expr is the bare blank identifier `_`.
func isBlankIdent(expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == "_"
}

// finalStmtSuffix builds the `\n<indent><reconstructed-stmt>` tail
// for a formReturn or formHoist shape. The reconstructed statement
// is the original statement's source text with every outermost q.*
// span replaced by its corresponding `subTexts[i]`. Nested q.*
// spans are already covered by their enclosing outermost span, so
// we only substitute immediate children of the statement — which
// is exactly what substituteSpans does.
func finalStmtSuffix(fset *token.FileSet, src []byte, sh callShape, subTexts []string) string {
	indent := indentOf(src, fset.Position(sh.Stmt.Pos()).Offset)
	start := fset.Position(sh.Stmt.Pos()).Offset
	end := fset.Position(sh.Stmt.End()).Offset
	return "\n" + indent + substituteSpans(fset, src, start, end, sh.Calls, subTexts)
}

// renderTry produces the replacement for bare q.Try across all
// forms. The returned text always ends with `if <errVar> != nil { return … }`.
// The bubbled error is the captured err itself (no wrapping for bare).
func renderTry(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string) (string, error) {
	zeros, indent, errVar, innerText, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", err
	}
	bindLine := tryBindLine(fset, src, sh, errVar, innerText, indent, counter)
	zeros[len(zeros)-1] = errVar
	return assembleErrBlock(bindLine, errVar, indent, zeros), nil
}

// renderTryE produces the replacement for q.TryE chains across all
// four forms. The chain method picks how the bubbled error is
// shaped; the form picks the bind line. RecoverIs / RecoverAs
// intermediates (if any) emit a per-step `if errors.Is/As(...) { v,
// _qErr = value, nil }` block between the bind and the terminal
// bubble check.
func renderTryE(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string) (string, bool, bool, error) {
	zeros, indent, errVar, innerText, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", false, false, err
	}
	bindLine := tryBindLine(fset, src, sh, errVar, innerText, indent, counter)
	recoveryLHS := lhsTextOrUnderscore(fset, src, sh, counter)
	recoverPrelude, recoverErr := renderRecoverSteps(fset, src, sub, counter, indent, errVar, recoveryLHS, subs, subTexts)
	if recoverErr != nil {
		return "", false, false, recoverErr
	}
	bindLine = bindLine + recoverPrelude
	// Recover steps emit errors.Is / errors.As, which require the
	// errors import. Flag for the import-injection pass.
	errorsUsed := len(sub.RecoverSteps) > 0

	switch sub.Method {
	case "Err":
		if len(sub.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("q.TryE(...).Err requires exactly one argument (the replacement error); got %d", len(sub.MethodArgs))
		}
		zeros[len(zeros)-1] = exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		return assembleErrBlock(bindLine, errVar, indent, zeros), false, errorsUsed, nil

	case "ErrF":
		if len(sub.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("q.TryE(...).ErrF requires exactly one argument (an error-transform fn); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf("(%s)(%s)", fn, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), false, errorsUsed, nil

	case "Wrap":
		if len(sub.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("q.TryE(...).Wrap requires exactly one argument (the message string); got %d", len(sub.MethodArgs))
		}
		msg := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf(`fmt.Errorf("%%s: %%w", %s, %s)`, msg, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), true, errorsUsed, nil

	case "Wrapf":
		if len(sub.MethodArgs) < 1 {
			return "", false, false, fmt.Errorf("q.TryE(...).Wrapf requires at least one argument (the format string); got %d", len(sub.MethodArgs))
		}
		formatExpr, ok := sub.MethodArgs[0].(*ast.BasicLit)
		if !ok || formatExpr.Kind != token.STRING {
			return "", false, false, fmt.Errorf("q.TryE(...).Wrapf's first argument must be a string literal so the rewriter can splice in `: %%w`")
		}
		raw := formatExpr.Value
		formatWithW := raw[:len(raw)-1] + `: %w` + `"`
		argParts := []string{formatWithW}
		for _, a := range sub.MethodArgs[1:] {
			argParts = append(argParts, exprTextSubst(fset, src, a, subs, subTexts))
		}
		argParts = append(argParts, errVar)
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s)", joinWith(argParts, ", "))
		return assembleErrBlock(bindLine, errVar, indent, zeros), true, errorsUsed, nil

	case "Catch":
		if len(sub.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("q.TryE(...).Catch requires exactly one argument (a (T, error)-returning fn); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		retErrVar := fmt.Sprintf("_qRet%d", counter)
		zeros[len(zeros)-1] = retErrVar
		// Catch only makes sense when there is a place to put the
		// recovered value — i.e. formDefine or formAssign. In
		// formDiscard there is no LHS to rebind; rewrite as if it were
		// ErrF returning the second tuple element.
		return assembleCatchErrBlock(bindLine, recoveryLHS, errVar, retErrVar, fn, indent, zeros), false, errorsUsed, nil
	}

	return "", false, false, fmt.Errorf("renderTryE: unknown method %q", sub.Method)
}

// renderRecoverSteps emits one block per RecoverIs / RecoverAs step,
// to be inserted between the bind line and the terminal bubble check
// in renderTryE. Each block clears the captured err and rebinds the
// recovery target when the captured err matches the step's predicate.
//
// Returns the prelude string (with a leading "\n<indent>" per step
// so it concatenates cleanly onto the bind line) and any error from
// invalid step shape (e.g. RecoverAs called without a typed-nil
// arg). The caller is responsible for marking errorsUsed=true when
// any step is present, since the emitted blocks reference
// errors.Is / errors.As.
func renderRecoverSteps(fset *token.FileSet, src []byte, sub qSubCall, counter int, indent, errVar, recoveryLHS string, subs []qSubCall, subTexts []string) (string, error) {
	if len(sub.RecoverSteps) == 0 {
		return "", nil
	}
	var b bytes.Buffer
	for i, step := range sub.RecoverSteps {
		valueText := exprTextSubst(fset, src, step.ValueArg, subs, subTexts)
		b.WriteByte('\n')
		b.WriteString(indent)
		switch step.Kind {
		case recoverKindIs:
			matchText := exprTextSubst(fset, src, step.MatchArg, subs, subTexts)
			fmt.Fprintf(&b, "if %s != nil && errors.Is(%s, %s) {\n", errVar, errVar, matchText)
			fmt.Fprintf(&b, "%s\t%s, %s = %s, nil\n", indent, recoveryLHS, errVar, valueText)
			fmt.Fprintf(&b, "%s}", indent)
		case recoverKindAs:
			typeExpr, ok := extractTypeFromTypedNil(step.MatchArg)
			if !ok {
				return "", fmt.Errorf("q.TryE(...).RecoverAs (step %d): first argument must be a typed-nil literal like `(*MyErr)(nil)` so the rewriter can extract the target type at compile time", i+1)
			}
			typeText := exprText(fset, src, typeExpr)
			asVar := fmt.Sprintf("_qAs%d_%d", counter, i)
			fmt.Fprintf(&b, "if %s != nil {\n", errVar)
			fmt.Fprintf(&b, "%s\tvar %s %s\n", indent, asVar, typeText)
			fmt.Fprintf(&b, "%s\tif errors.As(%s, &%s) {\n", indent, errVar, asVar)
			fmt.Fprintf(&b, "%s\t\t%s, %s = %s, nil\n", indent, recoveryLHS, errVar, valueText)
			fmt.Fprintf(&b, "%s\t}\n", indent)
			fmt.Fprintf(&b, "%s}", indent)
		}
	}
	return b.String(), nil
}

// extractTypeFromTypedNil parses a typed-nil literal expression like
// `(*MyErr)(nil)` or `MyErrType(nil)` and returns the underlying
// type expression (`*MyErr` / `MyErrType`). The expression must be
// a single-arg call whose only arg is the identifier `nil`. Parens
// wrapping the type are stripped.
func extractTypeFromTypedNil(arg ast.Expr) (ast.Expr, bool) {
	call, ok := arg.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil, false
	}
	id, ok := call.Args[0].(*ast.Ident)
	if !ok || id.Name != "nil" {
		return nil, false
	}
	typeExpr := call.Fun
	if paren, ok := typeExpr.(*ast.ParenExpr); ok {
		typeExpr = paren.X
	}
	return typeExpr, true
}

// renderNotNil produces the replacement for bare q.NotNil across all
// four forms. The bubbled error is q.ErrNil (spelled through the
// local alias).
func renderNotNil(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, alias string, subs []qSubCall, subTexts []string) (string, error) {
	zeros, indent, _, innerText, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", err
	}
	bindLine, checkVar := nilBindLineAndCheck(fset, src, sh, innerText, counter)
	zeros[len(zeros)-1] = alias + ".ErrNil"
	return assembleNilBlock(bindLine, checkVar, indent, zeros), nil
}

// renderNotNilE produces the replacement for q.NotNilE chains across
// all four forms.
func renderNotNilE(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string) (string, bool, bool, error) {
	zeros, indent, _, innerText, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", false, false, err
	}
	bindLine, checkVar := nilBindLineAndCheck(fset, src, sh, innerText, counter)

	switch sub.Method {
	case "Err":
		if len(sub.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("q.NotNilE(...).Err requires exactly one argument (the replacement error); got %d", len(sub.MethodArgs))
		}
		zeros[len(zeros)-1] = exprText(fset, src, sub.MethodArgs[0])
		return assembleNilBlock(bindLine, checkVar, indent, zeros), false, false, nil

	case "ErrF":
		if len(sub.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("q.NotNilE(...).ErrF requires exactly one argument (a func() error thunk); got %d", len(sub.MethodArgs))
		}
		fn := exprText(fset, src, sub.MethodArgs[0])
		zeros[len(zeros)-1] = fmt.Sprintf("(%s)()", fn)
		return assembleNilBlock(bindLine, checkVar, indent, zeros), false, false, nil

	case "Wrap":
		if len(sub.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("q.NotNilE(...).Wrap requires exactly one argument (the message string); got %d", len(sub.MethodArgs))
		}
		msg := exprText(fset, src, sub.MethodArgs[0])
		zeros[len(zeros)-1] = fmt.Sprintf("errors.New(%s)", msg)
		return assembleNilBlock(bindLine, checkVar, indent, zeros), false, true, nil

	case "Wrapf":
		if len(sub.MethodArgs) < 1 {
			return "", false, false, fmt.Errorf("q.NotNilE(...).Wrapf requires at least one argument (the format string); got %d", len(sub.MethodArgs))
		}
		var argParts []string
		for _, a := range sub.MethodArgs {
			argParts = append(argParts, exprText(fset, src, a))
		}
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s)", joinWith(argParts, ", "))
		return assembleNilBlock(bindLine, checkVar, indent, zeros), true, false, nil

	case "Catch":
		if len(sub.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("q.NotNilE(...).Catch requires exactly one argument (a func() (*T, error)); got %d", len(sub.MethodArgs))
		}
		fn := exprText(fset, src, sub.MethodArgs[0])
		retErrVar := fmt.Sprintf("_qRet%d", counter)
		zeros[len(zeros)-1] = retErrVar
		recoveryLHS := lhsTextOrUnderscore(fset, src, sh, counter)
		return assembleNilCatchBlock(bindLine, checkVar, recoveryLHS, retErrVar, fn, indent, zeros), false, false, nil
	}

	return "", false, false, fmt.Errorf("renderNotNilE: unknown method %q", sub.Method)
}

// renderLock produces the replacement for q.Lock. Statement-only:
// evaluates the locker expression once into a local (so expressions
// with side effects like `rwm.RLocker()` don't double-fire), Locks
// it, then defers Unlock in the enclosing function.
func renderLock(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string) (string, error) {
	if sh.Form != formDiscard {
		return "", fmt.Errorf("q.Lock must be an expression statement (no LHS, no return position); the call returns no value")
	}
	indent := indentOf(src, fset.Position(sh.Stmt.Pos()).Offset)
	lockerText := exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	tmp := fmt.Sprintf("_qLock%d", counter)
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s := %s\n", tmp, lockerText)
	fmt.Fprintf(&b, "%s%s.Lock()\n", indent, tmp)
	fmt.Fprintf(&b, "%sdefer %s.Unlock()", indent, tmp)
	return b.String(), nil
}

// renderPanicMarker produces the replacement for q.TODO / q.Unreachable.
// Both are statement-only panics with a file:line-prefixed message
// built from a compile-time literal + optional user-supplied
// message expression. name is "q.TODO" or "q.Unreachable".
func renderPanicMarker(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, name string, subs []qSubCall, subTexts []string) (string, error) {
	if sh.Form != formDiscard {
		return "", fmt.Errorf("%s must be an expression statement (no LHS, no return position); the call returns no value", name)
	}
	prefix := tracePrefix(fset, sub.OuterCall.Pos())
	base := name + " " + prefix
	var msgExpr string
	if len(sub.MethodArgs) == 1 {
		userMsg := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		msgExpr = fmt.Sprintf("%s + (%s)", strconv.Quote(base+": "), userMsg)
	} else {
		msgExpr = strconv.Quote(base)
	}
	return fmt.Sprintf("panic(%s)", msgExpr), nil
}

// renderRequire produces the replacement for q.Require. Bubbles an
// error to the enclosing function's error return when cond is false;
// the bubble is `fmt.Errorf("<file:line>[: <msg>]: %w", q.ErrRequireFailed)`
// so callers can `errors.Is(err, q.ErrRequireFailed)` to detect a
// require failure. The user-supplied message (if any) is %s-spliced
// between the location prefix and the wrapped sentinel.
// Statement-only.
func renderRequire(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, alias string, subs []qSubCall, subTexts []string) (string, error) {
	if sh.Form != formDiscard {
		return "", fmt.Errorf("q.Require must be an expression statement (no LHS, no return position); the call returns no value")
	}
	zeros, indent, _, _, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", err
	}
	condText := exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	prefix := tracePrefix(fset, sub.OuterCall.Pos())
	sentinel := alias + ".ErrRequireFailed"
	var bubbleExpr string
	if len(sub.MethodArgs) == 1 {
		userMsg := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		bubbleExpr = fmt.Sprintf("fmt.Errorf(%s, %s, %s)", strconv.Quote(prefix+": %s: %w"), userMsg, sentinel)
	} else {
		bubbleExpr = fmt.Sprintf("fmt.Errorf(%s, %s)", strconv.Quote(prefix+": %w"), sentinel)
	}
	zeros[len(zeros)-1] = bubbleExpr
	var b bytes.Buffer
	fmt.Fprintf(&b, "if !(%s) {\n", condText)
	fmt.Fprintf(&b, "%s\treturn %s\n", indent, joinWith(zeros, ", "))
	fmt.Fprintf(&b, "%s}", indent)
	return b.String(), nil
}

// tracePrefix returns a "<basename>:<line>" string for the supplied
// source position. Used by renderTrace / renderTraceE to inject a
// call-site location into the bubbled error at compile time.
// Basename is preferred over absolute path because the prefix ends
// up in runtime error messages where brevity wins over path
// provenance.
func tracePrefix(fset *token.FileSet, pos token.Pos) string {
	p := fset.Position(pos)
	return fmt.Sprintf("%s:%d", filepath.Base(p.Filename), p.Line)
}

// renderTrace produces the replacement for bare q.Trace. Like
// renderTry but the bubbled error is wrapped with a file:line
// prefix captured at compile time.
func renderTrace(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string) (string, error) {
	zeros, indent, errVar, innerText, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", err
	}
	_ = innerText
	bindLine := tryBindLine(fset, src, sh, errVar, innerText, indent, counter)
	prefix := tracePrefix(fset, sub.OuterCall.Pos())
	formatLit := strconv.Quote(prefix + ": %w")
	zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s, %s)", formatLit, errVar)
	return assembleErrBlock(bindLine, errVar, indent, zeros), nil
}

// renderTraceE produces the replacement for q.TraceE chains. Each
// chain method composes *over* the trace prefix: `Wrap("ctx")` at
// file.go:42 becomes `fmt.Errorf("file.go:42: ctx: %w", err)`,
// `Err(replacement)` becomes `fmt.Errorf("file.go:42: %w", replacement)`,
// etc. No method can opt out of the prefix — that's the whole point
// of Trace.
func renderTraceE(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string) (string, error) {
	zeros, indent, errVar, innerText, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", err
	}
	_ = innerText
	bindLine := tryBindLine(fset, src, sh, errVar, innerText, indent, counter)
	prefix := tracePrefix(fset, sub.OuterCall.Pos())

	switch sub.Method {
	case "Err":
		if len(sub.MethodArgs) != 1 {
			return "", fmt.Errorf("q.TraceE(...).Err requires exactly one argument (the replacement error); got %d", len(sub.MethodArgs))
		}
		rep := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		formatLit := strconv.Quote(prefix + ": %w")
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s, %s)", formatLit, rep)
		return assembleErrBlock(bindLine, errVar, indent, zeros), nil

	case "ErrF":
		if len(sub.MethodArgs) != 1 {
			return "", fmt.Errorf("q.TraceE(...).ErrF requires exactly one argument (an error-transform fn); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		formatLit := strconv.Quote(prefix + ": %w")
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s, (%s)(%s))", formatLit, fn, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), nil

	case "Wrap":
		if len(sub.MethodArgs) != 1 {
			return "", fmt.Errorf("q.TraceE(...).Wrap requires exactly one argument (the message string); got %d", len(sub.MethodArgs))
		}
		msg := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		formatLit := strconv.Quote(prefix + ": %s: %w")
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s, %s, %s)", formatLit, msg, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), nil

	case "Wrapf":
		if len(sub.MethodArgs) < 1 {
			return "", fmt.Errorf("q.TraceE(...).Wrapf requires at least one argument (the format string); got %d", len(sub.MethodArgs))
		}
		formatExpr, ok := sub.MethodArgs[0].(*ast.BasicLit)
		if !ok || formatExpr.Kind != token.STRING {
			return "", fmt.Errorf("q.TraceE(...).Wrapf's first argument must be a string literal so the rewriter can splice in the trace prefix and `: %%w`")
		}
		userFormat, err := strconv.Unquote(formatExpr.Value)
		if err != nil {
			return "", fmt.Errorf("q.TraceE(...).Wrapf: cannot unquote format literal: %w", err)
		}
		formatLit := strconv.Quote(prefix + ": " + userFormat + ": %w")
		argParts := []string{formatLit}
		for _, a := range sub.MethodArgs[1:] {
			argParts = append(argParts, exprTextSubst(fset, src, a, subs, subTexts))
		}
		argParts = append(argParts, errVar)
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s)", joinWith(argParts, ", "))
		return assembleErrBlock(bindLine, errVar, indent, zeros), nil

	case "Catch":
		if len(sub.MethodArgs) != 1 {
			return "", fmt.Errorf("q.TraceE(...).Catch requires exactly one argument (a func(error) (T, error)); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		retErrVar := fmt.Sprintf("_qRet%d", counter)
		formatLit := strconv.Quote(prefix + ": %w")
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s, %s)", formatLit, retErrVar)
		recoveryLHS := lhsTextOrUnderscore(fset, src, sh, counter)
		return assembleCatchErrBlock(bindLine, recoveryLHS, errVar, retErrVar, fn, indent, zeros), nil
	}

	return "", fmt.Errorf("renderTraceE: unknown method %q", sub.Method)
}

// renderOk produces the replacement for bare q.Ok across all forms.
// Bind line binds both the value and the ok flag to a local tuple,
// the bubble check is `if !<okVar>`, and the bubbled error is the
// q.ErrNotOk sentinel.
func renderOk(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, alias string, subs []qSubCall, subTexts []string) (string, error) {
	zeros, indent, _, _, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", err
	}
	bindLine, okVar := okBindLineAndCheck(fset, src, sh, sub, counter, subs, subTexts)
	zeros[len(zeros)-1] = alias + ".ErrNotOk"
	return assembleOkBlock(bindLine, okVar, indent, zeros), nil
}

// renderOkE produces the replacement for q.OkE chains across all
// forms. Same bubble-shape vocabulary as NotNilE (no captured source
// error, so Wrap → errors.New and Wrapf → fmt.Errorf); the only
// differences from the NotNil family are the bind line (binds a
// tuple) and the bubble check (`!<okVar>`).
func renderOkE(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string) (string, bool, bool, error) {
	zeros, indent, _, _, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", false, false, err
	}
	bindLine, okVar := okBindLineAndCheck(fset, src, sh, sub, counter, subs, subTexts)

	switch sub.Method {
	case "Err":
		if len(sub.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("q.OkE(...).Err requires exactly one argument (the replacement error); got %d", len(sub.MethodArgs))
		}
		zeros[len(zeros)-1] = exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		return assembleOkBlock(bindLine, okVar, indent, zeros), false, false, nil

	case "ErrF":
		if len(sub.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("q.OkE(...).ErrF requires exactly one argument (a func() error thunk); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf("(%s)()", fn)
		return assembleOkBlock(bindLine, okVar, indent, zeros), false, false, nil

	case "Wrap":
		if len(sub.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("q.OkE(...).Wrap requires exactly one argument (the message string); got %d", len(sub.MethodArgs))
		}
		msg := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf("errors.New(%s)", msg)
		return assembleOkBlock(bindLine, okVar, indent, zeros), false, true, nil

	case "Wrapf":
		if len(sub.MethodArgs) < 1 {
			return "", false, false, fmt.Errorf("q.OkE(...).Wrapf requires at least one argument (the format string); got %d", len(sub.MethodArgs))
		}
		var argParts []string
		for _, a := range sub.MethodArgs {
			argParts = append(argParts, exprTextSubst(fset, src, a, subs, subTexts))
		}
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s)", joinWith(argParts, ", "))
		return assembleOkBlock(bindLine, okVar, indent, zeros), true, false, nil

	case "Catch":
		if len(sub.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("q.OkE(...).Catch requires exactly one argument (a func() (T, error)); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		retErrVar := fmt.Sprintf("_qRet%d", counter)
		zeros[len(zeros)-1] = retErrVar
		recoveryLHS := lhsTextOrUnderscore(fset, src, sh, counter)
		return assembleOkCatchBlock(bindLine, okVar, recoveryLHS, retErrVar, fn, indent, zeros), false, false, nil
	}

	return "", false, false, fmt.Errorf("renderOkE: unknown method %q", sub.Method)
}

// okBindLineAndCheck builds the bind line for the Ok family and the
// variable name to test. Thin wrapper that pulls inner text from
// sub.OkArgs; Recv and As use okBindLineFromInner with their own
// synthetic inner text (`<-ch`, `x.(T)`).
func okBindLineAndCheck(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string) (bindLine, okVar string) {
	innerText := okInnerText(fset, src, sub, subs, subTexts)
	return okBindLineFromInner(fset, src, sh, counter, innerText)
}

// okBindLineFromInner is the common bind-line builder for every
// family that follows the comma-ok pattern. Callers compute their
// own inner text — the source span for Ok, `<-(ch)` for Recv,
// `(x).(T)` for As — then this helper drops it into the per-form
// tuple binding.
func okBindLineFromInner(fset *token.FileSet, src []byte, sh callShape, counter int, innerText string) (bindLine, okVar string) {
	okVar = fmt.Sprintf("_qOk%d", counter)
	indent := indentOf(src, fset.Position(sh.Stmt.Pos()).Offset)
	switch sh.Form {
	case formDefine:
		return fmt.Sprintf("%s, %s := %s", exprText(fset, src, sh.LHSExpr), okVar, innerText), okVar
	case formAssign:
		return fmt.Sprintf("var %s bool\n%s%s, %s = %s", okVar, indent, exprText(fset, src, sh.LHSExpr), okVar, innerText), okVar
	case formDiscard:
		return fmt.Sprintf("_, %s := %s", okVar, innerText), okVar
	case formReturn, formHoist:
		return fmt.Sprintf("_qTmp%d, %s := %s", counter, okVar, innerText), okVar
	}
	return "/* unsupported form */", okVar
}

// recvInnerText returns "<-(<chExpr>)" with nested q.* spans
// substituted. Always parenthesised so chExpr's internal operators
// bind correctly under the leading `<-`.
func recvInnerText(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string) string {
	ch := exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	return fmt.Sprintf("<-(%s)", ch)
}

// asInnerText returns "(<xExpr>).(<T>)" with nested q.* spans
// substituted in xExpr. The type argument is spliced verbatim from
// source — q.* shouldn't appear in a type expression, so no
// substitution is attempted.
func asInnerText(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string) string {
	x := exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	t := exprText(fset, src, sub.AsType)
	return fmt.Sprintf("(%s).(%s)", x, t)
}

// renderRecv produces the replacement for bare q.Recv. Mirrors
// renderOk's shape but with a synthetic inner text and the
// ErrChanClosed sentinel.
func renderRecv(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, alias string, subs []qSubCall, subTexts []string) (string, error) {
	zeros, indent, _, _, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", err
	}
	bindLine, okVar := okBindLineFromInner(fset, src, sh, counter, recvInnerText(fset, src, sub, subs, subTexts))
	zeros[len(zeros)-1] = alias + ".ErrChanClosed"
	return assembleOkBlock(bindLine, okVar, indent, zeros), nil
}

// renderRecvE produces the replacement for q.RecvE chains. Reuses
// renderOkE's method-dispatch structure with Recv's synthetic inner
// text.
func renderRecvE(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string) (string, bool, bool, error) {
	return renderOkLikeE(fset, src, sh, sub, counter, recvInnerText(fset, src, sub, subs, subTexts), "q.RecvE", subs, subTexts)
}

// renderAs produces the replacement for bare q.As. Mirrors
// renderRecv with `<x>.(<T>)` as the inner text.
func renderAs(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, alias string, subs []qSubCall, subTexts []string) (string, error) {
	zeros, indent, _, _, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", err
	}
	bindLine, okVar := okBindLineFromInner(fset, src, sh, counter, asInnerText(fset, src, sub, subs, subTexts))
	zeros[len(zeros)-1] = alias + ".ErrBadTypeAssert"
	return assembleOkBlock(bindLine, okVar, indent, zeros), nil
}

// renderAsE produces the replacement for q.AsE chains.
func renderAsE(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string) (string, bool, bool, error) {
	return renderOkLikeE(fset, src, sh, sub, counter, asInnerText(fset, src, sub, subs, subTexts), "q.AsE", subs, subTexts)
}

// renderOkLikeE is the shared chain-dispatcher for the Ok-like
// families (OkE, RecvE, AsE). Every family shares identical chain
// method semantics; only the bind's inner text differs. name is
// used in error messages ("q.OkE(...).Err", etc.) so diagnostics
// still point at the right family.
func renderOkLikeE(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, innerText, name string, subs []qSubCall, subTexts []string) (string, bool, bool, error) {
	zeros, indent, _, _, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", false, false, err
	}
	bindLine, okVar := okBindLineFromInner(fset, src, sh, counter, innerText)

	switch sub.Method {
	case "Err":
		if len(sub.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("%s(...).Err requires exactly one argument (the replacement error); got %d", name, len(sub.MethodArgs))
		}
		zeros[len(zeros)-1] = exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		return assembleOkBlock(bindLine, okVar, indent, zeros), false, false, nil
	case "ErrF":
		if len(sub.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("%s(...).ErrF requires exactly one argument (a func() error thunk); got %d", name, len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf("(%s)()", fn)
		return assembleOkBlock(bindLine, okVar, indent, zeros), false, false, nil
	case "Wrap":
		if len(sub.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("%s(...).Wrap requires exactly one argument (the message string); got %d", name, len(sub.MethodArgs))
		}
		msg := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf("errors.New(%s)", msg)
		return assembleOkBlock(bindLine, okVar, indent, zeros), false, true, nil
	case "Wrapf":
		if len(sub.MethodArgs) < 1 {
			return "", false, false, fmt.Errorf("%s(...).Wrapf requires at least one argument (the format string); got %d", name, len(sub.MethodArgs))
		}
		var argParts []string
		for _, a := range sub.MethodArgs {
			argParts = append(argParts, exprTextSubst(fset, src, a, subs, subTexts))
		}
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s)", joinWith(argParts, ", "))
		return assembleOkBlock(bindLine, okVar, indent, zeros), true, false, nil
	case "Catch":
		if len(sub.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("%s(...).Catch requires exactly one argument (a func() (T, error)); got %d", name, len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		retErrVar := fmt.Sprintf("_qRet%d", counter)
		zeros[len(zeros)-1] = retErrVar
		recoveryLHS := lhsTextOrUnderscore(fset, src, sh, counter)
		return assembleOkCatchBlock(bindLine, okVar, recoveryLHS, retErrVar, fn, indent, zeros), false, false, nil
	}
	return "", false, false, fmt.Errorf("renderOkLikeE: unknown method %q", sub.Method)
}

// okInnerText returns the source text for the Ok / OkE entry's
// argument span, with nested q.* spans substituted by their temps.
// Works for both the single-CallExpr and two-arg shapes by slicing
// from the first arg's Pos to the last arg's End.
func okInnerText(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string) string {
	if len(sub.OkArgs) == 0 {
		return ""
	}
	start := fset.Position(sub.OkArgs[0].Pos()).Offset
	end := fset.Position(sub.OkArgs[len(sub.OkArgs)-1].End()).Offset
	return substituteSpans(fset, src, start, end, subs, subTexts)
}

// assembleOkBlock formats the Ok-family replacement skeleton. Like
// the NotNil version but the check is `!<okVar>` (bool) rather than
// `<checkVar> == nil` (pointer).
//
//	<bindLine>
//	if !<okVar> {
//	    return <zeros>
//	}
func assembleOkBlock(bindLine, okVar, indent string, zeros []string) string {
	var b bytes.Buffer
	b.WriteString(bindLine)
	b.WriteByte('\n')
	fmt.Fprintf(&b, "%sif !%s {\n", indent, okVar)
	fmt.Fprintf(&b, "%s\treturn %s\n", indent, joinWith(zeros, ", "))
	fmt.Fprintf(&b, "%s}", indent)
	return b.String()
}

// assembleOkCatchBlock is the Ok-family Catch counterpart. Mirrors
// the NotNil version but with the `!<okVar>` check.
//
//	<bindLine>
//	if !<okVar> {
//	    var <retErrVar> error
//	    <recoveryLHS>, <retErrVar> = (<fn>)()
//	    if <retErrVar> != nil {
//	        return <zeros>
//	    }
//	}
func assembleOkCatchBlock(bindLine, okVar, recoveryLHS, retErrVar, fn, indent string, zeros []string) string {
	var b bytes.Buffer
	b.WriteString(bindLine)
	b.WriteByte('\n')
	fmt.Fprintf(&b, "%sif !%s {\n", indent, okVar)
	fmt.Fprintf(&b, "%s\tvar %s error\n", indent, retErrVar)
	fmt.Fprintf(&b, "%s\t%s, %s = (%s)()\n", indent, recoveryLHS, retErrVar, fn)
	fmt.Fprintf(&b, "%s\tif %s != nil {\n", indent, retErrVar)
	fmt.Fprintf(&b, "%s\t\treturn %s\n", indent, joinWith(zeros, ", "))
	fmt.Fprintf(&b, "%s\t}\n", indent)
	fmt.Fprintf(&b, "%s}", indent)
	return b.String()
}

// commonRenderInputs assembles the pieces every renderer needs: the
// per-result zero-value expressions, the original statement's indent,
// the local err-variable name, and the source text of the inner
// expression.
func commonRenderInputs(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string) (zeros []string, indent, errVar, innerText string, err error) {
	results := sh.EnclosingFuncType.Results
	if results == nil || results.NumFields() == 0 {
		return nil, "", "", "", fmt.Errorf("q.* used in a function with no return values; the bubble has nowhere to go")
	}
	zeros, err = zeroExprs(fset, src, results)
	if err != nil {
		return nil, "", "", "", err
	}
	innerText = exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	errVar = fmt.Sprintf("_qErr%d", counter)
	indent = indentOf(src, fset.Position(sh.Stmt.Pos()).Offset)
	return zeros, indent, errVar, innerText, nil
}

// tryBindLine builds the bind line for the Try family across all
// forms:
//
//	formDefine:           v, _qErrN := <inner>
//	formAssign:           var _qErrN error
//	                      v, _qErrN = <inner>
//	formDiscard:          _, _qErrN := <inner>
//	formReturn/formHoist: _qTmpN, _qErrN := <inner>
func tryBindLine(fset *token.FileSet, src []byte, sh callShape, errVar, innerText, indent string, counter int) string {
	switch sh.Form {
	case formDefine:
		return fmt.Sprintf("%s, %s := %s", exprText(fset, src, sh.LHSExpr), errVar, innerText)
	case formAssign:
		return fmt.Sprintf("var %s error\n%s%s, %s = %s", errVar, indent, exprText(fset, src, sh.LHSExpr), errVar, innerText)
	case formDiscard:
		return fmt.Sprintf("_, %s := %s", errVar, innerText)
	case formReturn, formHoist:
		return fmt.Sprintf("_qTmp%d, %s := %s", counter, errVar, innerText)
	}
	return fmt.Sprintf("/* unsupported form %v */", sh.Form)
}

// nilBindLineAndCheck builds the bind line for the NotNil family and
// the variable name to test for nil. For define and assign forms the
// LHS itself is the check variable; for discard, a fresh _qVal<N>
// temporary holds the value being tested; for return, `_qTmp<N>`
// doubles as the check var and the value spliced into the final
// return.
func nilBindLineAndCheck(fset *token.FileSet, src []byte, sh callShape, innerText string, counter int) (bindLine, checkVar string) {
	switch sh.Form {
	case formDefine:
		lhs := exprText(fset, src, sh.LHSExpr)
		return fmt.Sprintf("%s := %s", lhs, innerText), lhs
	case formAssign:
		lhs := exprText(fset, src, sh.LHSExpr)
		return fmt.Sprintf("%s = %s", lhs, innerText), lhs
	case formDiscard:
		tmp := fmt.Sprintf("_qVal%d", counter)
		return fmt.Sprintf("%s := %s", tmp, innerText), tmp
	case formReturn, formHoist:
		tmp := fmt.Sprintf("_qTmp%d", counter)
		return fmt.Sprintf("%s := %s", tmp, innerText), tmp
	}
	return "/* unsupported form */", "_"
}

// lhsTextOrUnderscore returns the recovery target for Catch across
// every form: the source LHS for define/assign, `_` for discard,
// and the per-stmt `_qTmp<counter>` for return/hoist (where the
// rewriter synthesized a temp to carry the value into the final
// reconstructed statement). Called by every family's Catch
// assembler; without the return/hoist branch a `q.OkE(...).Catch`
// in return position crashes on nil LHSExpr.
func lhsTextOrUnderscore(fset *token.FileSet, src []byte, sh callShape, counter int) string {
	switch sh.Form {
	case formDiscard:
		return "_"
	case formReturn, formHoist:
		return fmt.Sprintf("_qTmp%d", counter)
	}
	return exprText(fset, src, sh.LHSExpr)
}

// assembleErrBlock formats the universal Try-family replacement
// skeleton. bindLine carries the form-specific bind statements (one
// or two lines, no trailing newline).
//
//	<bindLine>
//	if <errVar> != nil {
//	    return <zeros>
//	}
func assembleErrBlock(bindLine, errVar, indent string, zeros []string) string {
	var b bytes.Buffer
	b.WriteString(bindLine)
	b.WriteByte('\n')
	fmt.Fprintf(&b, "%sif %s != nil {\n", indent, errVar)
	fmt.Fprintf(&b, "%s\treturn %s\n", indent, joinWith(zeros, ", "))
	fmt.Fprintf(&b, "%s}", indent)
	return b.String()
}

// assembleNilBlock formats the universal NotNil-family replacement
// skeleton.
//
//	<bindLine>
//	if <checkVar> == nil {
//	    return <zeros>
//	}
func assembleNilBlock(bindLine, checkVar, indent string, zeros []string) string {
	var b bytes.Buffer
	b.WriteString(bindLine)
	b.WriteByte('\n')
	fmt.Fprintf(&b, "%sif %s == nil {\n", indent, checkVar)
	fmt.Fprintf(&b, "%s\treturn %s\n", indent, joinWith(zeros, ", "))
	fmt.Fprintf(&b, "%s}", indent)
	return b.String()
}

// assembleCatchErrBlock formats the Catch replacement for the Try
// family. The err branch reassigns the LHS via fn(err); on (recovered,
// nil) execution falls through, on (zero, newErr) newErr bubbles.
//
//	<bindLine>
//	if <errVar> != nil {
//	    var <retErrVar> error
//	    <recoveryLHS>, <retErrVar> = (<fn>)(<errVar>)
//	    if <retErrVar> != nil {
//	        return <zeros>
//	    }
//	}
func assembleCatchErrBlock(bindLine, recoveryLHS, errVar, retErrVar, fn, indent string, zeros []string) string {
	var b bytes.Buffer
	b.WriteString(bindLine)
	b.WriteByte('\n')
	fmt.Fprintf(&b, "%sif %s != nil {\n", indent, errVar)
	fmt.Fprintf(&b, "%s\tvar %s error\n", indent, retErrVar)
	fmt.Fprintf(&b, "%s\t%s, %s = (%s)(%s)\n", indent, recoveryLHS, retErrVar, fn, errVar)
	fmt.Fprintf(&b, "%s\tif %s != nil {\n", indent, retErrVar)
	fmt.Fprintf(&b, "%s\t\treturn %s\n", indent, joinWith(zeros, ", "))
	fmt.Fprintf(&b, "%s\t}\n", indent)
	fmt.Fprintf(&b, "%s}", indent)
	return b.String()
}

// assembleNilCatchBlock is the NotNil-family Catch counterpart.
//
//	<bindLine>
//	if <checkVar> == nil {
//	    var <retErrVar> error
//	    <recoveryLHS>, <retErrVar> = (<fn>)()
//	    if <retErrVar> != nil {
//	        return <zeros>
//	    }
//	}
func assembleNilCatchBlock(bindLine, checkVar, recoveryLHS, retErrVar, fn, indent string, zeros []string) string {
	var b bytes.Buffer
	b.WriteString(bindLine)
	b.WriteByte('\n')
	fmt.Fprintf(&b, "%sif %s == nil {\n", indent, checkVar)
	fmt.Fprintf(&b, "%s\tvar %s error\n", indent, retErrVar)
	fmt.Fprintf(&b, "%s\t%s, %s = (%s)()\n", indent, recoveryLHS, retErrVar, fn)
	fmt.Fprintf(&b, "%s\tif %s != nil {\n", indent, retErrVar)
	fmt.Fprintf(&b, "%s\t\treturn %s\n", indent, joinWith(zeros, ", "))
	fmt.Fprintf(&b, "%s\t}\n", indent)
	fmt.Fprintf(&b, "%s}", indent)
	return b.String()
}

// zeroExprs builds the per-result-position zero-value expressions for
// the enclosing function's result list. One expression per *result
// value*, expanding multi-name fields like `(a, b int)` into two
// entries.
func zeroExprs(fset *token.FileSet, src []byte, results *ast.FieldList) ([]string, error) {
	var out []string
	for _, f := range results.List {
		typeStart := fset.Position(f.Type.Pos()).Offset
		typeEnd := fset.Position(f.Type.End()).Offset
		typeText := string(src[typeStart:typeEnd])
		zero := "*new(" + typeText + ")"
		n := len(f.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			out = append(out, zero)
		}
	}
	return out, nil
}

// renderCheckCtx produces the replacement for bare q.CheckCtx. Always
// statement-only: binds `_qErrN := (ctx).Err()` and bubbles when
// non-nil. The bubbled error is ctx.Err() itself (already carries
// context.Canceled / context.DeadlineExceeded identity).
func renderCheckCtx(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string) (string, error) {
	if sh.Form != formDiscard {
		return "", fmt.Errorf("q.CheckCtx must be an expression statement (no LHS, no return position); the call returns no value")
	}
	zeros, indent, errVar, _, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", err
	}
	ctxText := exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	bindLine := fmt.Sprintf("%s := (%s).Err()", errVar, ctxText)
	zeros[len(zeros)-1] = errVar
	return assembleErrBlock(bindLine, errVar, indent, zeros), nil
}

// renderCheckCtxE produces the replacement for q.CheckCtxE chains.
// Mirrors renderCheckE's chain dispatch but with `(ctx).Err()` as
// the bind-line source. The captured err (ctx.Err()) is available
// as `_qErrN` in the bubble expression for each method.
func renderCheckCtxE(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string) (string, bool, error) {
	if sh.Form != formDiscard {
		return "", false, fmt.Errorf("q.CheckCtxE must be an expression statement (no LHS, no return position); the chain returns no value")
	}
	zeros, indent, errVar, _, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", false, err
	}
	ctxText := exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	bindLine := fmt.Sprintf("%s := (%s).Err()", errVar, ctxText)

	switch sub.Method {
	case "Err":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.CheckCtxE(...).Err requires exactly one argument (the replacement error); got %d", len(sub.MethodArgs))
		}
		zeros[len(zeros)-1] = exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		return assembleErrBlock(bindLine, errVar, indent, zeros), false, nil
	case "ErrF":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.CheckCtxE(...).ErrF requires exactly one argument (an error-transform fn); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf("(%s)(%s)", fn, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), false, nil
	case "Wrap":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.CheckCtxE(...).Wrap requires exactly one argument (the message string); got %d", len(sub.MethodArgs))
		}
		msg := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf(`fmt.Errorf("%%s: %%w", %s, %s)`, msg, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), true, nil
	case "Wrapf":
		if len(sub.MethodArgs) < 1 {
			return "", false, fmt.Errorf("q.CheckCtxE(...).Wrapf requires at least one argument (the format string); got %d", len(sub.MethodArgs))
		}
		formatExpr, ok := sub.MethodArgs[0].(*ast.BasicLit)
		if !ok || formatExpr.Kind != token.STRING {
			return "", false, fmt.Errorf("q.CheckCtxE(...).Wrapf's first argument must be a string literal so the rewriter can splice in `: %%w`")
		}
		raw := formatExpr.Value
		formatWithW := raw[:len(raw)-1] + `: %w` + `"`
		argParts := []string{formatWithW}
		for _, a := range sub.MethodArgs[1:] {
			argParts = append(argParts, exprTextSubst(fset, src, a, subs, subTexts))
		}
		argParts = append(argParts, errVar)
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s)", joinWith(argParts, ", "))
		return assembleErrBlock(bindLine, errVar, indent, zeros), true, nil
	case "Catch":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.CheckCtxE(...).Catch requires exactly one argument (a func(error) error); got %d", len(sub.MethodArgs))
		}
		// Shape matches CheckE.Catch: fn returns error alone.
		// nil = suppress (fall through), non-nil = bubble.
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		retErrVar := fmt.Sprintf("_qRet%d", counter)
		zeros[len(zeros)-1] = retErrVar
		var b bytes.Buffer
		b.WriteString(bindLine)
		b.WriteByte('\n')
		fmt.Fprintf(&b, "%sif %s != nil {\n", indent, errVar)
		fmt.Fprintf(&b, "%s\t%s := (%s)(%s)\n", indent, retErrVar, fn, errVar)
		fmt.Fprintf(&b, "%s\tif %s != nil {\n", indent, retErrVar)
		fmt.Fprintf(&b, "%s\t\treturn %s\n", indent, joinWith(zeros, ", "))
		fmt.Fprintf(&b, "%s\t}\n", indent)
		fmt.Fprintf(&b, "%s}", indent)
		return b.String(), false, nil
	}
	return "", false, fmt.Errorf("renderCheckCtxE: unknown method %q", sub.Method)
}

// ctxHelperInnerText returns `<alias>.<helper>(<spread of OkArgs>)` with
// nested q.* spans substituted. Used by RecvCtx / AwaitCtx / AwaitAll*
// families to build the runtime helper call that the Try-family bind
// line wraps. Supports:
//
//   - Empty OkArgs (produces `<alias>.<helper>()`) — for variadic
//     helpers like AwaitAll() / AwaitAny() that accept zero futures.
//   - Variadic spread (`q.AwaitAll(fs...)`) — EntryEllipsis.IsValid()
//     triggers a trailing `...` on the last arg.
func ctxHelperInnerText(fset *token.FileSet, src []byte, alias, helper string, sub qSubCall, subs []qSubCall, subTexts []string) string {
	if len(sub.OkArgs) == 0 {
		return fmt.Sprintf("%s.%s()", alias, helper)
	}
	argStart := fset.Position(sub.OkArgs[0].Pos()).Offset
	argEnd := fset.Position(sub.OkArgs[len(sub.OkArgs)-1].End()).Offset
	argText := substituteSpans(fset, src, argStart, argEnd, subs, subTexts)
	if sub.EntryEllipsis.IsValid() {
		argText += "..."
	}
	return fmt.Sprintf("%s.%s(%s)", alias, helper, argText)
}

// renderRecvCtx / renderAwaitCtx are the bare forms: Try-shape bubble
// over the runtime helper's (T, error) tuple.
func renderRecvCtx(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, alias string, subs []qSubCall, subTexts []string) (string, error) {
	return renderTryLikeWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "RecvRawCtx", sub, subs, subTexts))
}

func renderAwaitCtx(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, alias string, subs []qSubCall, subTexts []string) (string, error) {
	return renderTryLikeWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "AwaitRawCtx", sub, subs, subTexts))
}

// renderTryLikeWithInner is the shared bare-Try rendering path given
// an explicit inner text (the RHS of the bind line). Used by the
// Ctx-aware families whose inner text comes from a runtime helper
// call, not the user's q.*-wrapped inner call directly.
func renderTryLikeWithInner(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, innerText string) (string, error) {
	zeros, indent, errVar, _, err := commonRenderInputs(fset, src, sh, sub, counter, nil, nil)
	if err != nil {
		return "", err
	}
	bindLine := tryBindLine(fset, src, sh, errVar, innerText, indent, counter)
	zeros[len(zeros)-1] = errVar
	return assembleErrBlock(bindLine, errVar, indent, zeros), nil
}

// renderRecvCtxE / renderAwaitCtxE are the chain variants — dispatch
// on sub.Method mirroring renderTryE/renderAwaitE.
func renderRecvCtxE(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, alias string, subs []qSubCall, subTexts []string) (string, bool, error) {
	return renderTryLikeEWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "RecvRawCtx", sub, subs, subTexts), "q.RecvCtxE", subs, subTexts)
}

func renderAwaitCtxE(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, alias string, subs []qSubCall, subTexts []string) (string, bool, error) {
	return renderTryLikeEWithInner(fset, src, sh, sub, counter, ctxHelperInnerText(fset, src, alias, "AwaitRawCtx", sub, subs, subTexts), "q.AwaitCtxE", subs, subTexts)
}

// renderTryLikeEWithInner is the shared chain-dispatcher for the
// Try-shaped chain families that use a custom inner text (RecvCtxE,
// AwaitCtxE). Mirrors renderTryE's per-method vocabulary exactly.
// name is the family label for diagnostics.
func renderTryLikeEWithInner(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, innerText, name string, subs []qSubCall, subTexts []string) (string, bool, error) {
	zeros, indent, errVar, _, err := commonRenderInputs(fset, src, sh, sub, counter, subs, subTexts)
	if err != nil {
		return "", false, err
	}
	bindLine := tryBindLine(fset, src, sh, errVar, innerText, indent, counter)

	switch sub.Method {
	case "Err":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("%s(...).Err requires exactly one argument (the replacement error); got %d", name, len(sub.MethodArgs))
		}
		zeros[len(zeros)-1] = exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		return assembleErrBlock(bindLine, errVar, indent, zeros), false, nil
	case "ErrF":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("%s(...).ErrF requires exactly one argument (an error-transform fn); got %d", name, len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf("(%s)(%s)", fn, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), false, nil
	case "Wrap":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("%s(...).Wrap requires exactly one argument (the message string); got %d", name, len(sub.MethodArgs))
		}
		msg := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf(`fmt.Errorf("%%s: %%w", %s, %s)`, msg, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), true, nil
	case "Wrapf":
		if len(sub.MethodArgs) < 1 {
			return "", false, fmt.Errorf("%s(...).Wrapf requires at least one argument (the format string); got %d", name, len(sub.MethodArgs))
		}
		formatExpr, ok := sub.MethodArgs[0].(*ast.BasicLit)
		if !ok || formatExpr.Kind != token.STRING {
			return "", false, fmt.Errorf("%s(...).Wrapf's first argument must be a string literal so the rewriter can splice in `: %%w`", name)
		}
		raw := formatExpr.Value
		formatWithW := raw[:len(raw)-1] + `: %w` + `"`
		argParts := []string{formatWithW}
		for _, a := range sub.MethodArgs[1:] {
			argParts = append(argParts, exprTextSubst(fset, src, a, subs, subTexts))
		}
		argParts = append(argParts, errVar)
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s)", joinWith(argParts, ", "))
		return assembleErrBlock(bindLine, errVar, indent, zeros), true, nil
	case "Catch":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("%s(...).Catch requires exactly one argument (a (T, error)-returning fn); got %d", name, len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		retErrVar := fmt.Sprintf("_qRet%d", counter)
		zeros[len(zeros)-1] = retErrVar
		recoveryLHS := lhsTextOrUnderscore(fset, src, sh, counter)
		return assembleCatchErrBlock(bindLine, recoveryLHS, errVar, retErrVar, fn, indent, zeros), false, nil
	}
	return "", false, fmt.Errorf("renderTryLikeEWithInner: unknown method %q", sub.Method)
}

// renderTimeoutDeadline produces the replacement for q.Timeout /
// q.Deadline: a (ctx, cancel) tuple bind to context.With{Timeout,Deadline}
// plus a `defer cancel()`. helper is "WithTimeout" or "WithDeadline".
// Only formDefine / formAssign are supported (single LHS holding the
// new ctx); every other form is a user error.
func renderTimeoutDeadline(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string, helper string) (string, error) {
	family := "q.Timeout"
	if helper == "WithDeadline" {
		family = "q.Deadline"
	}
	if sh.Form != formDefine && sh.Form != formAssign {
		return "", fmt.Errorf("%s must be used in a `newCtx := %s(...)` or `ctx = %s(...)` statement; the call returns a context.Context", family, family, family)
	}
	if len(sub.OkArgs) != 2 {
		return "", fmt.Errorf("%s expects two arguments (ctx, dur|t); got %d", family, len(sub.OkArgs))
	}
	indent := indentOf(src, fset.Position(sh.Stmt.Pos()).Offset)
	ctxText := exprTextSubst(fset, src, sub.OkArgs[0], subs, subTexts)
	argText := exprTextSubst(fset, src, sub.OkArgs[1], subs, subTexts)
	cancelVar := fmt.Sprintf("_qCancel%d", counter)
	lhsText := exprText(fset, src, sh.LHSExpr)

	var b bytes.Buffer
	switch sh.Form {
	case formDefine:
		fmt.Fprintf(&b, "%s, %s := context.%s(%s, %s)", lhsText, cancelVar, helper, ctxText, argText)
	case formAssign:
		fmt.Fprintf(&b, "var %s context.CancelFunc\n", cancelVar)
		fmt.Fprintf(&b, "%s%s, %s = context.%s(%s, %s)", indent, lhsText, cancelVar, helper, ctxText, argText)
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "%sdefer %s()", indent, cancelVar)
	return b.String(), nil
}

// exprText returns the source-text representation of an arbitrary
// expression by its source-byte span. Used to splice user-supplied
// arguments verbatim into the rewritten output, preserving the user's
// literal spelling (string concatenations, function values, etc.).
func exprText(fset *token.FileSet, src []byte, e ast.Expr) string {
	start := fset.Position(e.Pos()).Offset
	end := fset.Position(e.End()).Offset
	return string(src[start:end])
}

// hasImport reports whether file already imports the given package
// path under any name (including "_" / "."). Used to decide whether
// the rewriter needs to inject a fresh import spec.
func hasImport(file *ast.File, path string) bool {
	for _, imp := range file.Imports {
		got, err := unquote(imp.Path.Value)
		if err == nil && got == path {
			return true
		}
	}
	return false
}

// ensureImport returns out with the named package added to the file's
// import block. Three cases:
//
//  1. File has a parenthesised import block: insert `\n\t"<path>"`
//     just before the closing `)`.
//  2. File has a single-line `import "..."` form: replace the import
//     declaration entirely with a parenthesised block containing the
//     original import plus `"<path>"`.
//  3. File has no imports at all: insert `import "<path>"` after the
//     `package <name>` line.
//
// Detection works on the AST (parenthesised vs single) and the splice
// is purely textual. Caller is responsible for first checking
// hasImport so we never emit a duplicate.
func ensureImport(file *ast.File, fset *token.FileSet, out []byte, path string) []byte {
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}
		if gd.Lparen.IsValid() {
			rparenOff := fset.Position(gd.Rparen).Offset
			insertion := []byte(fmt.Sprintf("\t%q\n", path))
			return append(out[:rparenOff], append(insertion, out[rparenOff:]...)...)
		}
		start := fset.Position(gd.Pos()).Offset
		end := fset.Position(gd.End()).Offset
		spec := gd.Specs[0].(*ast.ImportSpec)
		original := spec.Path.Value
		var alias string
		if spec.Name != nil {
			alias = spec.Name.Name + " "
		}
		replacement := fmt.Sprintf("import (\n\t%s%s\n\t%q\n)", alias, original, path)
		return append(out[:start], append([]byte(replacement), out[end:]...)...)
	}
	pkgEnd := fset.Position(file.Name.End()).Offset
	insertion := []byte(fmt.Sprintf("\n\nimport %q", path))
	return append(out[:pkgEnd], append(insertion, out[pkgEnd:]...)...)
}

// indentOf returns the run of leading whitespace on the source line
// containing the given byte offset. Used to repeat the original
// statement's indentation across the multi-line replacement.
func indentOf(src []byte, off int) string {
	i := off
	for i > 0 && src[i-1] != '\n' {
		i--
	}
	end := i
	for end < len(src) && (src[end] == ' ' || src[end] == '\t') {
		end++
	}
	if end > off {
		end = off
	}
	return string(src[i:end])
}

// joinWith concatenates parts with sep. Local helper to keep the
// rewriter free of strings imports — keeps the file's surface small.
func joinWith(parts []string, sep string) string {
	var b bytes.Buffer
	for i, p := range parts {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(p)
	}
	return b.String()
}
