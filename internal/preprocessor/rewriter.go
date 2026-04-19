package preprocessor

// rewriter.go — emit replacement text for one recognised q.* call
// site, then splice all replacements into a copy of the source bytes
// to produce a rewritten file.
//
// V1 only handles the `v := q.Try(call())` shape recognised by
// scanner.go. The rewriter is purely textual: it parses with
// go/parser only to drive the scanner; the actual mutation is on the
// raw source bytes, so non-rewritten regions stay byte-identical and
// gofmt-style formatting around the rewrite is preserved.
//
// Zero values come from the universal `*new(T)` form: `new(T)` returns
// a *T regardless of T, and `*` dereferences to T's zero value. This
// avoids per-type knowledge of zero-value spellings (`0` for ints, `""`
// for strings, `nil` for pointers, etc.) and works for user-defined
// types, generic types, and interfaces without special cases. The Go
// compiler optimises `*new(T)` away to a constant zero — the generated
// machine code is identical to a hand-written zero literal.

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
// referenced it. Cheaper than tracking residual usage.
func rewriteFile(fset *token.FileSet, src []byte, shapes []callShape, alias string) ([]byte, error) {
	type edit struct {
		start, end int
		text       string
	}

	edits := make([]edit, 0, len(shapes))
	counter := 0
	for _, sh := range shapes {
		counter++
		text, err := renderTryAssign(fset, src, sh, counter)
		if err != nil {
			return nil, err
		}
		start := fset.Position(sh.Stmt.Pos()).Offset
		end := fset.Position(sh.Stmt.End()).Offset
		edits = append(edits, edit{start: start, end: end, text: text})
	}

	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })

	out := append([]byte(nil), src...)
	for _, e := range edits {
		out = append(out[:e.start], append([]byte(e.text), out[e.end:]...)...)
	}
	if alias != "" {
		sentinel := fmt.Sprintf("\n\nvar _ = %s.ErrNil\n", alias)
		out = append(out, []byte(sentinel)...)
	}
	return out, nil
}

// renderTryAssign produces the inlined replacement text for one
// recognised `v := q.Try(call())` site. Output shape:
//
//	v, _qErrN := <inner-call-source>
//	if _qErrN != nil {
//	    return *new(T1), *new(T2), …, _qErrN
//	}
//
// The leading line carries no indent (the splice site already starts
// at the original statement's column); subsequent lines repeat the
// detected indent so the rewritten body still parses as Go and reads
// reasonably in tempfile dumps.
func renderTryAssign(fset *token.FileSet, src []byte, sh callShape, counter int) (string, error) {
	results := sh.EnclosingFunc.Type.Results
	if results == nil || results.NumFields() == 0 {
		return "", fmt.Errorf("q.Try used in a function with no return values; the bubble has nowhere to go")
	}
	zeros, err := zeroExprs(fset, src, results)
	if err != nil {
		return "", err
	}

	innerStart := fset.Position(sh.InnerCall.Pos()).Offset
	innerEnd := fset.Position(sh.InnerCall.End()).Offset
	innerText := string(src[innerStart:innerEnd])

	errVar := fmt.Sprintf("_qErr%d", counter)
	indent := indentOf(src, fset.Position(sh.Stmt.Pos()).Offset)

	// The last result must be the error position; we hand the captured
	// errVar there. If the user's function does not declare its last
	// result as `error`, the resulting source will not compile — that
	// is correct: the rewriter does not change function signatures.
	zeros[len(zeros)-1] = errVar

	var b bytes.Buffer
	fmt.Fprintf(&b, "%s, %s := %s\n", sh.LHSName, errVar, innerText)
	fmt.Fprintf(&b, "%sif %s != nil {\n", indent, errVar)
	fmt.Fprintf(&b, "%s\treturn %s\n", indent, joinWith(zeros, ", "))
	fmt.Fprintf(&b, "%s}", indent)
	return b.String(), nil
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
