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
func rewriteFile(fset *token.FileSet, file *ast.File, src []byte, shapes []callShape, alias string) ([]byte, []string, error) {
	type edit struct {
		start, end int
		text       string
	}

	edits := make([]edit, 0, len(shapes))
	counter := 0
	needsFmt, needsErrors := false, false
	for _, sh := range shapes {
		counter++
		text, fmtUsed, errorsUsed, err := renderShape(fset, src, sh, counter, alias)
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
	return out, addedImports, nil
}

// renderShape dispatches one matched call site to the right renderer.
// Returns the replacement text, flags indicating whether fmt / errors
// are used by the replacement (the caller injects imports if so), and
// any error.
//
// For formReturn shapes, the renderer builds the usual bind + bubble
// block; this wrapper appends the reconstructed final return using
// `_qTmp<N>` in place of the q.* sub-expression.
func renderShape(fset *token.FileSet, src []byte, sh callShape, counter int, alias string) (string, bool, bool, error) {
	var (
		text                   string
		fmtUsed, errorsUsed    bool
		err                    error
	)
	switch sh.Family {
	case familyTry:
		text, err = renderTry(fset, src, sh, counter)
	case familyTryE:
		text, fmtUsed, err = renderTryE(fset, src, sh, counter)
	case familyNotNil:
		text, err = renderNotNil(fset, src, sh, counter, alias)
	case familyNotNilE:
		text, fmtUsed, errorsUsed, err = renderNotNilE(fset, src, sh, counter)
	default:
		return "", false, false, fmt.Errorf("renderShape: unknown family %v", sh.Family)
	}
	if err != nil {
		return "", false, false, err
	}
	if sh.Form == formReturn {
		text += finalReturnSuffix(fset, src, sh, counter)
	}
	return text, fmtUsed, errorsUsed, nil
}

// finalReturnSuffix builds the `\n<indent>return <reconstructed>`
// tail for a formReturn shape. The reconstructed return is the
// original return statement's source text with the q.* outer call
// span replaced by `_qTmp<N>`. This preserves the spelling of every
// other result expression (user comments, literal formatting,
// implicit conversions) verbatim.
func finalReturnSuffix(fset *token.FileSet, src []byte, sh callShape, counter int) string {
	indent := indentOf(src, fset.Position(sh.Stmt.Pos()).Offset)
	retStart := fset.Position(sh.Stmt.Pos()).Offset
	retEnd := fset.Position(sh.Stmt.End()).Offset
	retText := string(src[retStart:retEnd])
	callStartRel := fset.Position(sh.OuterCall.Pos()).Offset - retStart
	callEndRel := fset.Position(sh.OuterCall.End()).Offset - retStart
	tmp := fmt.Sprintf("_qTmp%d", counter)
	reconstructed := retText[:callStartRel] + tmp + retText[callEndRel:]
	return "\n" + indent + reconstructed
}

// renderTry produces the replacement for bare q.Try across all three
// forms. The returned text always ends with `if <errVar> != nil { return … }`.
// The bubbled error is the captured err itself (no wrapping for bare).
func renderTry(fset *token.FileSet, src []byte, sh callShape, counter int) (string, error) {
	zeros, indent, errVar, innerText, err := commonRenderInputs(fset, src, sh, counter)
	if err != nil {
		return "", err
	}
	bindLine := tryBindLine(fset, src, sh, errVar, innerText, indent, counter)
	zeros[len(zeros)-1] = errVar
	return assembleErrBlock(bindLine, errVar, indent, zeros), nil
}

// renderTryE produces the replacement for q.TryE chains across all
// three forms. The chain method picks how the bubbled error is
// shaped; the form picks the bind line.
func renderTryE(fset *token.FileSet, src []byte, sh callShape, counter int) (string, bool, error) {
	zeros, indent, errVar, innerText, err := commonRenderInputs(fset, src, sh, counter)
	if err != nil {
		return "", false, err
	}
	bindLine := tryBindLine(fset, src, sh, errVar, innerText, indent, counter)

	switch sh.Method {
	case "Err":
		if len(sh.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.TryE(...).Err requires exactly one argument (the replacement error); got %d", len(sh.MethodArgs))
		}
		zeros[len(zeros)-1] = exprText(fset, src, sh.MethodArgs[0])
		return assembleErrBlock(bindLine, errVar, indent, zeros), false, nil

	case "ErrF":
		if len(sh.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.TryE(...).ErrF requires exactly one argument (an error-transform fn); got %d", len(sh.MethodArgs))
		}
		fn := exprText(fset, src, sh.MethodArgs[0])
		zeros[len(zeros)-1] = fmt.Sprintf("(%s)(%s)", fn, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), false, nil

	case "Wrap":
		if len(sh.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.TryE(...).Wrap requires exactly one argument (the message string); got %d", len(sh.MethodArgs))
		}
		msg := exprText(fset, src, sh.MethodArgs[0])
		zeros[len(zeros)-1] = fmt.Sprintf(`fmt.Errorf("%%s: %%w", %s, %s)`, msg, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), true, nil

	case "Wrapf":
		if len(sh.MethodArgs) < 1 {
			return "", false, fmt.Errorf("q.TryE(...).Wrapf requires at least one argument (the format string); got %d", len(sh.MethodArgs))
		}
		formatExpr, ok := sh.MethodArgs[0].(*ast.BasicLit)
		if !ok || formatExpr.Kind != token.STRING {
			return "", false, fmt.Errorf("q.TryE(...).Wrapf's first argument must be a string literal so the rewriter can splice in `: %%w`")
		}
		raw := formatExpr.Value
		formatWithW := raw[:len(raw)-1] + `: %w` + `"`
		argParts := []string{formatWithW}
		for _, a := range sh.MethodArgs[1:] {
			argParts = append(argParts, exprText(fset, src, a))
		}
		argParts = append(argParts, errVar)
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s)", joinWith(argParts, ", "))
		return assembleErrBlock(bindLine, errVar, indent, zeros), true, nil

	case "Catch":
		if len(sh.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.TryE(...).Catch requires exactly one argument (a (T, error)-returning fn); got %d", len(sh.MethodArgs))
		}
		fn := exprText(fset, src, sh.MethodArgs[0])
		retErrVar := fmt.Sprintf("_qRet%d", counter)
		zeros[len(zeros)-1] = retErrVar
		// Catch only makes sense when there is a place to put the
		// recovered value — i.e. formDefine or formAssign. In
		// formDiscard there is no LHS to rebind; rewrite as if it were
		// ErrF returning the second tuple element.
		recoveryLHS := lhsTextOrUnderscore(fset, src, sh)
		return assembleCatchErrBlock(bindLine, recoveryLHS, errVar, retErrVar, fn, indent, zeros), false, nil
	}

	return "", false, fmt.Errorf("renderTryE: unknown method %q", sh.Method)
}

// renderNotNil produces the replacement for bare q.NotNil across all
// three forms. The bubbled error is q.ErrNil (spelled through the
// local alias).
func renderNotNil(fset *token.FileSet, src []byte, sh callShape, counter int, alias string) (string, error) {
	zeros, indent, _, innerText, err := commonRenderInputs(fset, src, sh, counter)
	if err != nil {
		return "", err
	}
	bindLine, checkVar := nilBindLineAndCheck(fset, src, sh, innerText, counter)
	zeros[len(zeros)-1] = alias + ".ErrNil"
	return assembleNilBlock(bindLine, checkVar, indent, zeros), nil
}

// renderNotNilE produces the replacement for q.NotNilE chains across
// all three forms.
func renderNotNilE(fset *token.FileSet, src []byte, sh callShape, counter int) (string, bool, bool, error) {
	zeros, indent, _, innerText, err := commonRenderInputs(fset, src, sh, counter)
	if err != nil {
		return "", false, false, err
	}
	bindLine, checkVar := nilBindLineAndCheck(fset, src, sh, innerText, counter)

	switch sh.Method {
	case "Err":
		if len(sh.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("q.NotNilE(...).Err requires exactly one argument (the replacement error); got %d", len(sh.MethodArgs))
		}
		zeros[len(zeros)-1] = exprText(fset, src, sh.MethodArgs[0])
		return assembleNilBlock(bindLine, checkVar, indent, zeros), false, false, nil

	case "ErrF":
		if len(sh.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("q.NotNilE(...).ErrF requires exactly one argument (a func() error thunk); got %d", len(sh.MethodArgs))
		}
		fn := exprText(fset, src, sh.MethodArgs[0])
		zeros[len(zeros)-1] = fmt.Sprintf("(%s)()", fn)
		return assembleNilBlock(bindLine, checkVar, indent, zeros), false, false, nil

	case "Wrap":
		if len(sh.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("q.NotNilE(...).Wrap requires exactly one argument (the message string); got %d", len(sh.MethodArgs))
		}
		msg := exprText(fset, src, sh.MethodArgs[0])
		zeros[len(zeros)-1] = fmt.Sprintf("errors.New(%s)", msg)
		return assembleNilBlock(bindLine, checkVar, indent, zeros), false, true, nil

	case "Wrapf":
		if len(sh.MethodArgs) < 1 {
			return "", false, false, fmt.Errorf("q.NotNilE(...).Wrapf requires at least one argument (the format string); got %d", len(sh.MethodArgs))
		}
		var argParts []string
		for _, a := range sh.MethodArgs {
			argParts = append(argParts, exprText(fset, src, a))
		}
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s)", joinWith(argParts, ", "))
		return assembleNilBlock(bindLine, checkVar, indent, zeros), true, false, nil

	case "Catch":
		if len(sh.MethodArgs) != 1 {
			return "", false, false, fmt.Errorf("q.NotNilE(...).Catch requires exactly one argument (a func() (*T, error)); got %d", len(sh.MethodArgs))
		}
		fn := exprText(fset, src, sh.MethodArgs[0])
		retErrVar := fmt.Sprintf("_qRet%d", counter)
		zeros[len(zeros)-1] = retErrVar
		recoveryLHS := lhsTextOrUnderscore(fset, src, sh)
		return assembleNilCatchBlock(bindLine, checkVar, recoveryLHS, retErrVar, fn, indent, zeros), false, false, nil
	}

	return "", false, false, fmt.Errorf("renderNotNilE: unknown method %q", sh.Method)
}

// commonRenderInputs assembles the pieces every renderer needs: the
// per-result zero-value expressions, the original statement's indent,
// the local err-variable name, and the source text of the inner
// expression.
func commonRenderInputs(fset *token.FileSet, src []byte, sh callShape, counter int) (zeros []string, indent, errVar, innerText string, err error) {
	results := sh.EnclosingFuncType.Results
	if results == nil || results.NumFields() == 0 {
		return nil, "", "", "", fmt.Errorf("q.* used in a function with no return values; the bubble has nowhere to go")
	}
	zeros, err = zeroExprs(fset, src, results)
	if err != nil {
		return nil, "", "", "", err
	}
	innerStart := fset.Position(sh.InnerExpr.Pos()).Offset
	innerEnd := fset.Position(sh.InnerExpr.End()).Offset
	innerText = string(src[innerStart:innerEnd])
	errVar = fmt.Sprintf("_qErr%d", counter)
	indent = indentOf(src, fset.Position(sh.Stmt.Pos()).Offset)
	return zeros, indent, errVar, innerText, nil
}

// tryBindLine builds the bind line for the Try family across all
// four forms:
//
//	formDefine:  v, _qErrN := <inner>
//	formAssign:  var _qErrN error
//	             v, _qErrN = <inner>
//	formDiscard: _, _qErrN := <inner>
//	formReturn:  _qTmpN, _qErrN := <inner>
func tryBindLine(fset *token.FileSet, src []byte, sh callShape, errVar, innerText, indent string, counter int) string {
	switch sh.Form {
	case formDefine:
		return fmt.Sprintf("%s, %s := %s", exprText(fset, src, sh.LHSExpr), errVar, innerText)
	case formAssign:
		return fmt.Sprintf("var %s error\n%s%s, %s = %s", errVar, indent, exprText(fset, src, sh.LHSExpr), errVar, innerText)
	case formDiscard:
		return fmt.Sprintf("_, %s := %s", errVar, innerText)
	case formReturn:
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
	case formReturn:
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
