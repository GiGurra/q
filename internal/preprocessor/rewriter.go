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
	needsFmt, needsErrors := false, false
	for _, sh := range shapes {
		text, fmtUsed, errorsUsed, err := renderShape(fset, src, sh, &counter, alias)
		if err != nil {
			return nil, nil, err
		}
		if fmtUsed {
			needsFmt = true
		}
		if errorsUsed {
			needsErrors = true
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

	// Apply statement-span edits bottom-up so earlier offsets do not
	// shift while later ones rewrite the file.
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })

	out := append([]byte(nil), src...)
	for _, e := range edits {
		out = append(out[:e.start], append([]byte(e.text), out[e.end:]...)...)
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
// substituted by its `_qTmp<N>`.
//
// *counter is the running per-file counter source. renderShape
// allocates one increment per sub-call so every temp name
// (`_qErrN`, `_qTmpN`, `_qValN`, `_qRetN`) stays globally unique.
// Returns flags indicating whether fmt / errors are used by the
// replacement (the caller injects imports if so).
func renderShape(fset *token.FileSet, src []byte, sh callShape, counter *int, alias string) (string, bool, bool, error) {
	if len(sh.Calls) == 0 {
		return "", false, false, fmt.Errorf("renderShape: shape has no sub-calls")
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

	var (
		blocks             []string
		fmtUsed, errorsUsed bool
	)
	for _, idx := range order {
		block, fu, eu, err := renderSubCall(fset, src, sh, idx, sh.Calls, counters, alias)
		if err != nil {
			return "", false, false, err
		}
		if fu {
			fmtUsed = true
		}
		if eu {
			errorsUsed = true
		}
		blocks = append(blocks, block)
	}
	text := joinWith(blocks, "\n"+indent)
	if sh.Form == formReturn || sh.Form == formHoist {
		text += finalStmtSuffix(fset, src, sh, counters)
	}
	return text, fmtUsed, errorsUsed, nil
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
// by its `_qTmp<counter>`. "Immediate child" means: contained by
// [start, end] and not contained by any other sub also contained by
// [start, end]. An exact match ([start, end] == OuterCall span) is
// not a child — otherwise rendering a sub's own InnerExpr text
// would substitute the parent sub into its own bind line. Children
// are applied bottom-up in offset-descending order so earlier
// offsets stay valid.
func substituteSpans(fset *token.FileSet, src []byte, start, end int, subs []qSubCall, counters []int) string {
	var contained []int
	for i, sub := range subs {
		cs := fset.Position(sub.OuterCall.Pos()).Offset
		ce := fset.Position(sub.OuterCall.End()).Offset
		if cs < start || ce > end {
			continue
		}
		if cs == start && ce == end {
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
		tmp := fmt.Sprintf("_qTmp%d", counters[i])
		text = append(text[:cs], append([]byte(tmp), text[ce:]...)...)
	}
	return string(text)
}

// exprTextSubst is exprText with nested q.* substitutions applied —
// used wherever a user-supplied expression might contain q.* calls
// that have been hoisted into their own binds. For locations known
// to be q.*-free (e.g. LHS on the direct-bind path), exprText
// suffices.
func exprTextSubst(fset *token.FileSet, src []byte, e ast.Expr, subs []qSubCall, counters []int) string {
	start := fset.Position(e.Pos()).Offset
	end := fset.Position(e.End()).Offset
	return substituteSpans(fset, src, start, end, subs, counters)
}

// renderSubCall dispatches one sub-call to the family-specific
// renderer. The full subs/counters lists are threaded through so
// each renderer can substitute nested q.* spans inside its own
// InnerExpr / MethodArgs text.
func renderSubCall(fset *token.FileSet, src []byte, sh callShape, subIdx int, subs []qSubCall, counters []int, alias string) (string, bool, bool, error) {
	sub := subs[subIdx]
	counter := counters[subIdx]
	switch sub.Family {
	case familyTry:
		text, err := renderTry(fset, src, sh, sub, counter, subs, counters)
		return text, false, false, err
	case familyTryE:
		text, fmtUsed, err := renderTryE(fset, src, sh, sub, counter, subs, counters)
		return text, fmtUsed, false, err
	case familyNotNil:
		text, err := renderNotNil(fset, src, sh, sub, counter, alias, subs, counters)
		return text, false, false, err
	case familyNotNilE:
		return renderNotNilE(fset, src, sh, sub, counter, subs, counters)
	case familyCheck:
		text, err := renderCheck(fset, src, sh, sub, counter, subs, counters)
		return text, false, false, err
	case familyCheckE:
		text, fmtUsed, err := renderCheckE(fset, src, sh, sub, counter, subs, counters)
		return text, fmtUsed, false, err
	case familyOpen, familyOpenE:
		text, fmtUsed, err := renderOpen(fset, src, sh, sub, counter, subs, counters)
		return text, fmtUsed, false, err
	}
	return "", false, false, fmt.Errorf("renderSubCall: unknown family %v", sub.Family)
}

// renderCheck produces the replacement for bare q.Check. Bind line
// is `_qErrN := <inner>` (no T to bind) and the bubble is the
// captured err itself.
func renderCheck(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, counters []int) (string, error) {
	zeros, indent, errVar, innerText, err := commonRenderInputs(fset, src, sh, sub, counter, subs, counters)
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
func renderCheckE(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, counters []int) (string, bool, error) {
	zeros, indent, errVar, innerText, err := commonRenderInputs(fset, src, sh, sub, counter, subs, counters)
	if err != nil {
		return "", false, err
	}
	bindLine := fmt.Sprintf("%s := %s", errVar, innerText)

	switch sub.Method {
	case "Err":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.CheckE(...).Err requires exactly one argument (the replacement error); got %d", len(sub.MethodArgs))
		}
		zeros[len(zeros)-1] = exprTextSubst(fset, src, sub.MethodArgs[0], subs, counters)
		return assembleErrBlock(bindLine, errVar, indent, zeros), false, nil
	case "ErrF":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.CheckE(...).ErrF requires exactly one argument (an error-transform fn); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, counters)
		zeros[len(zeros)-1] = fmt.Sprintf("(%s)(%s)", fn, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), false, nil
	case "Wrap":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.CheckE(...).Wrap requires exactly one argument (the message string); got %d", len(sub.MethodArgs))
		}
		msg := exprTextSubst(fset, src, sub.MethodArgs[0], subs, counters)
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
			argParts = append(argParts, exprTextSubst(fset, src, a, subs, counters))
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
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, counters)
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
func renderOpen(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, counters []int) (string, bool, error) {
	zeros, indent, errVar, innerText, err := commonRenderInputs(fset, src, sh, sub, counter, subs, counters)
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
		zeros[len(zeros)-1] = exprTextSubst(fset, src, sub.MethodArgs[0], subs, counters)
		block = assembleErrBlock(bindLine, errVar, indent, zeros)
	case "ErrF":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.OpenE(...).ErrF requires exactly one argument (an error-transform fn); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, counters)
		zeros[len(zeros)-1] = fmt.Sprintf("(%s)(%s)", fn, errVar)
		block = assembleErrBlock(bindLine, errVar, indent, zeros)
	case "Wrap":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.OpenE(...).Wrap requires exactly one argument (the message string); got %d", len(sub.MethodArgs))
		}
		msg := exprTextSubst(fset, src, sub.MethodArgs[0], subs, counters)
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
			argParts = append(argParts, exprTextSubst(fset, src, a, subs, counters))
		}
		argParts = append(argParts, errVar)
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s)", joinWith(argParts, ", "))
		fmtUsed = true
		block = assembleErrBlock(bindLine, errVar, indent, zeros)
	case "Catch":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.OpenE(...).Catch requires exactly one argument (a func(error) (T, error)); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, counters)
		retErrVar := fmt.Sprintf("_qRet%d", counter)
		zeros[len(zeros)-1] = retErrVar
		// Recovery rebinds valueVar so the deferred cleanup
		// later fires on the recovered value.
		block = assembleCatchErrBlock(bindLine, valueVar, errVar, retErrVar, fn, indent, zeros)
	default:
		return "", false, fmt.Errorf("renderOpen: unknown method %q", sub.Method)
	}

	cleanupText := exprTextSubst(fset, src, sub.ReleaseArg, subs, counters)
	deferLine := fmt.Sprintf("defer (%s)(%s)", cleanupText, valueVar)
	return block + "\n" + indent + deferLine, fmtUsed, nil
}

// openBindLine mirrors tryBindLine but always binds to a named
// variable for formDiscard — Open needs a target to pass to the
// deferred cleanup, so `_, _qErrN := …` (which Try uses) won't do.
func openBindLine(fset *token.FileSet, src []byte, sh callShape, errVar, innerText, indent string, counter int) string {
	switch sh.Form {
	case formDefine:
		return fmt.Sprintf("%s, %s := %s", exprText(fset, src, sh.LHSExpr), errVar, innerText)
	case formAssign:
		return fmt.Sprintf("var %s error\n%s%s, %s = %s", errVar, indent, exprText(fset, src, sh.LHSExpr), errVar, innerText)
	case formDiscard, formReturn, formHoist:
		return fmt.Sprintf("_qTmp%d, %s := %s", counter, errVar, innerText)
	}
	return fmt.Sprintf("/* unsupported form %v */", sh.Form)
}

// openValueVar returns the name of the bound resource variable for
// this Open sub-call. Used to spell the deferred cleanup arg and
// (for Catch) the recovery LHS.
func openValueVar(fset *token.FileSet, src []byte, sh callShape, counter int) string {
	switch sh.Form {
	case formDefine, formAssign:
		return exprText(fset, src, sh.LHSExpr)
	default:
		return fmt.Sprintf("_qTmp%d", counter)
	}
}

// finalStmtSuffix builds the `\n<indent><reconstructed-stmt>` tail
// for a formReturn or formHoist shape. The reconstructed statement
// is the original statement's source text with every outermost q.*
// span replaced by its corresponding `_qTmp<N>`. Nested q.* spans
// are already covered by their enclosing outermost span, so we
// only substitute immediate children of the statement — which is
// exactly what substituteSpans does.
func finalStmtSuffix(fset *token.FileSet, src []byte, sh callShape, counters []int) string {
	indent := indentOf(src, fset.Position(sh.Stmt.Pos()).Offset)
	start := fset.Position(sh.Stmt.Pos()).Offset
	end := fset.Position(sh.Stmt.End()).Offset
	return "\n" + indent + substituteSpans(fset, src, start, end, sh.Calls, counters)
}

// renderTry produces the replacement for bare q.Try across all
// forms. The returned text always ends with `if <errVar> != nil { return … }`.
// The bubbled error is the captured err itself (no wrapping for bare).
func renderTry(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, counters []int) (string, error) {
	zeros, indent, errVar, innerText, err := commonRenderInputs(fset, src, sh, sub, counter, subs, counters)
	if err != nil {
		return "", err
	}
	bindLine := tryBindLine(fset, src, sh, errVar, innerText, indent, counter)
	zeros[len(zeros)-1] = errVar
	return assembleErrBlock(bindLine, errVar, indent, zeros), nil
}

// renderTryE produces the replacement for q.TryE chains across all
// four forms. The chain method picks how the bubbled error is
// shaped; the form picks the bind line.
func renderTryE(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, counters []int) (string, bool, error) {
	zeros, indent, errVar, innerText, err := commonRenderInputs(fset, src, sh, sub, counter, subs, counters)
	if err != nil {
		return "", false, err
	}
	bindLine := tryBindLine(fset, src, sh, errVar, innerText, indent, counter)

	switch sub.Method {
	case "Err":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.TryE(...).Err requires exactly one argument (the replacement error); got %d", len(sub.MethodArgs))
		}
		zeros[len(zeros)-1] = exprTextSubst(fset, src, sub.MethodArgs[0], subs, counters)
		return assembleErrBlock(bindLine, errVar, indent, zeros), false, nil

	case "ErrF":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.TryE(...).ErrF requires exactly one argument (an error-transform fn); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, counters)
		zeros[len(zeros)-1] = fmt.Sprintf("(%s)(%s)", fn, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), false, nil

	case "Wrap":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.TryE(...).Wrap requires exactly one argument (the message string); got %d", len(sub.MethodArgs))
		}
		msg := exprTextSubst(fset, src, sub.MethodArgs[0], subs, counters)
		zeros[len(zeros)-1] = fmt.Sprintf(`fmt.Errorf("%%s: %%w", %s, %s)`, msg, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), true, nil

	case "Wrapf":
		if len(sub.MethodArgs) < 1 {
			return "", false, fmt.Errorf("q.TryE(...).Wrapf requires at least one argument (the format string); got %d", len(sub.MethodArgs))
		}
		formatExpr, ok := sub.MethodArgs[0].(*ast.BasicLit)
		if !ok || formatExpr.Kind != token.STRING {
			return "", false, fmt.Errorf("q.TryE(...).Wrapf's first argument must be a string literal so the rewriter can splice in `: %%w`")
		}
		raw := formatExpr.Value
		formatWithW := raw[:len(raw)-1] + `: %w` + `"`
		argParts := []string{formatWithW}
		for _, a := range sub.MethodArgs[1:] {
			argParts = append(argParts, exprTextSubst(fset, src, a, subs, counters))
		}
		argParts = append(argParts, errVar)
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s)", joinWith(argParts, ", "))
		return assembleErrBlock(bindLine, errVar, indent, zeros), true, nil

	case "Catch":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.TryE(...).Catch requires exactly one argument (a (T, error)-returning fn); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, counters)
		retErrVar := fmt.Sprintf("_qRet%d", counter)
		zeros[len(zeros)-1] = retErrVar
		// Catch only makes sense when there is a place to put the
		// recovered value — i.e. formDefine or formAssign. In
		// formDiscard there is no LHS to rebind; rewrite as if it were
		// ErrF returning the second tuple element.
		recoveryLHS := lhsTextOrUnderscore(fset, src, sh)
		return assembleCatchErrBlock(bindLine, recoveryLHS, errVar, retErrVar, fn, indent, zeros), false, nil
	}

	return "", false, fmt.Errorf("renderTryE: unknown method %q", sub.Method)
}

// renderNotNil produces the replacement for bare q.NotNil across all
// four forms. The bubbled error is q.ErrNil (spelled through the
// local alias).
func renderNotNil(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, alias string, subs []qSubCall, counters []int) (string, error) {
	zeros, indent, _, innerText, err := commonRenderInputs(fset, src, sh, sub, counter, subs, counters)
	if err != nil {
		return "", err
	}
	bindLine, checkVar := nilBindLineAndCheck(fset, src, sh, innerText, counter)
	zeros[len(zeros)-1] = alias + ".ErrNil"
	return assembleNilBlock(bindLine, checkVar, indent, zeros), nil
}

// renderNotNilE produces the replacement for q.NotNilE chains across
// all four forms.
func renderNotNilE(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, counters []int) (string, bool, bool, error) {
	zeros, indent, _, innerText, err := commonRenderInputs(fset, src, sh, sub, counter, subs, counters)
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
		recoveryLHS := lhsTextOrUnderscore(fset, src, sh)
		return assembleNilCatchBlock(bindLine, checkVar, recoveryLHS, retErrVar, fn, indent, zeros), false, false, nil
	}

	return "", false, false, fmt.Errorf("renderNotNilE: unknown method %q", sub.Method)
}

// commonRenderInputs assembles the pieces every renderer needs: the
// per-result zero-value expressions, the original statement's indent,
// the local err-variable name, and the source text of the inner
// expression.
func commonRenderInputs(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, counters []int) (zeros []string, indent, errVar, innerText string, err error) {
	results := sh.EnclosingFuncType.Results
	if results == nil || results.NumFields() == 0 {
		return nil, "", "", "", fmt.Errorf("q.* used in a function with no return values; the bubble has nowhere to go")
	}
	zeros, err = zeroExprs(fset, src, results)
	if err != nil {
		return nil, "", "", "", err
	}
	innerText = exprTextSubst(fset, src, sub.InnerExpr, subs, counters)
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

// lhsTextOrUnderscore returns the LHS source text for define/assign
// forms, or "_" for discard. Used by Catch's recovery line where the
// rebinding has nowhere to go in the discard case.
func lhsTextOrUnderscore(fset *token.FileSet, src []byte, sh callShape) string {
	if sh.Form == formDiscard {
		return "_"
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
