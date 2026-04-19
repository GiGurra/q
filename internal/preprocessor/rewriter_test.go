package preprocessor

// Unit tests for the rewriter. Black-box-style: feed a small source
// string in, run scan + rewrite, assert on the rewritten output. These
// are the fastest signal when iterating on rewriter behavior — the e2e
// fixtures cost ~half a second each because they invoke `go build`.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewriteTryAssign_BasicShape(t *testing.T) {
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func parse(s string) (int, error) {
	n := q.Try(atoi(s))
	return n * 2, nil
}

func atoi(s string) (int, error) { return 0, nil }
`
	got := mustRewrite(t, src)

	wants := []string{
		"n, _qErr1 := atoi(s)",
		"if _qErr1 != nil {",
		"return *new(int), _qErr1",
		"var _ = q.ErrNil",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("rewritten output missing %q.\n--- output:\n%s", w, got)
		}
	}
	if strings.Contains(got, "q.Try(") {
		t.Errorf("rewritten output still contains a q.Try call.\n--- output:\n%s", got)
	}
}

func TestRewriteTryAssign_MultipleResults(t *testing.T) {
	// Function with three results — two non-error positions, the
	// rewriter should emit a zero for each non-error position and
	// thread the captured err into the last slot.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func three(s string) (int, string, error) {
	n := q.Try(atoi(s))
	return n, "x", nil
}

func atoi(s string) (int, error) { return 0, nil }
`
	got := mustRewrite(t, src)
	want := "return *new(int), *new(string), _qErr1"
	if !strings.Contains(got, want) {
		t.Errorf("rewritten output missing %q.\n--- output:\n%s", want, got)
	}
}

func TestRewriteTryAssign_AliasedImport(t *testing.T) {
	// Aliased import: q is renamed to qq locally. The scanner must
	// follow the alias and the sentinel must reference it.
	src := `package p

import qq "github.com/GiGurra/q/pkg/q"

func parse(s string) (int, error) {
	n := qq.Try(atoi(s))
	return n, nil
}

func atoi(s string) (int, error) { return 0, nil }
`
	got := mustRewrite(t, src)
	if !strings.Contains(got, "var _ = qq.ErrNil") {
		t.Errorf("alias-aware sentinel missing.\n--- output:\n%s", got)
	}
	if strings.Contains(got, "qq.Try(") {
		t.Errorf("aliased q.Try call should have been rewritten.\n--- output:\n%s", got)
	}
}

func TestRewriteTryEWrapf_InjectsFmtImport(t *testing.T) {
	// Source has no `fmt` import; the Wrapf rewrite needs fmt.Errorf,
	// so the rewriter must inject the import.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func parse(s string) (int, error) {
	n := q.TryE(atoi(s)).Wrapf("parsing %q", s)
	return n, nil
}

func atoi(s string) (int, error) { return 0, nil }
`
	got := mustRewrite(t, src)
	wants := []string{
		`fmt.Errorf("parsing %q: %w", s, _qErr1)`,
		`"fmt"`, // injected import
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q.\n--- output:\n%s", w, got)
		}
	}
}

func TestRewriteTryECatch_RecoveryShape(t *testing.T) {
	// Catch's replacement is structurally distinct: the err branch
	// reassigns LHS via fn and inspects the second return.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func parse(s string) (int, error) {
	n := q.TryE(atoi(s)).Catch(myFn)
	return n, nil
}

func atoi(s string) (int, error)             { return 0, nil }
func myFn(e error) (int, error)              { return 0, nil }
`
	got := mustRewrite(t, src)
	wants := []string{
		"n, _qErr1 := atoi(s)",
		"var _qRet1 error",
		"n, _qRet1 = (myFn)(_qErr1)",
		"if _qRet1 != nil {",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q.\n--- output:\n%s", w, got)
		}
	}
}

func TestRewriteInsideNestedBlocks(t *testing.T) {
	// Regression: q.* inside if-bodies, for/range bodies, switch
	// cases, etc. must be scanned (not just top-level statements).
	// The shape mirrors the generics_run_ok fixture's firstNonNil.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func first[T any](items []*T) (T, error) {
	for _, item := range items {
		if item != nil {
			v := q.NotNil(item)
			return *v, nil
		}
	}
	return *new(T), nil
}
`
	got := mustRewrite(t, src)
	t.Logf("rewritten:\n%s", got)
	if !strings.Contains(got, "if v == nil {") {
		t.Errorf("expected the q.NotNil rewrite to land inside the nested if-body.\n--- output:\n%s", got)
	}
}

func TestRewriteFixtureSource_NoExceptions(t *testing.T) {
	// Run the rewriter against every checked-in fixture's *.go file
	// and assert it produces a syntactically valid Go file with no
	// surprises (no `c` variable artefacts, no leftover q.* calls,
	// no orphan _qErr/_qVal references). Cheaper than the full e2e
	// build for catching common output-shape regressions.
	cases, err := os.ReadDir("testdata/cases")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range cases {
		if !e.IsDir() {
			continue
		}
		caseDir := filepath.Join("testdata", "cases", e.Name())
		entries, err := os.ReadDir(caseDir)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range entries {
			if !strings.HasSuffix(f.Name(), ".go") {
				continue
			}
			path := filepath.Join(caseDir, f.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Run(e.Name()+"/"+f.Name(), func(t *testing.T) {
				fset := token.NewFileSet()
				file, err := parser.ParseFile(fset, path, data, parser.ParseComments)
				if err != nil {
					t.Fatalf("parse: %v", err)
				}
				if qImportAlias(file) == "" {
					t.Skip("no q import")
				}
				shapes, diags, err := scanFile(fset, path, file)
				if err != nil {
					t.Fatal(err)
				}
				if len(diags) > 0 {
					t.Fatalf("unexpected scan diagnostics: %v", diags)
				}
				if len(shapes) == 0 {
					t.Skip("no shapes to rewrite")
				}
				alias := qImportAlias(file)
				out, _, err := rewriteFile(fset, file, data, shapes, alias)
				if err != nil {
					t.Fatalf("rewrite: %v", err)
				}
				// Must still parse as valid Go.
				rewritten, err := parser.ParseFile(token.NewFileSet(), path, out, parser.ParseComments)
				if err != nil {
					t.Fatalf("rewritten output failed to parse: %v\n--- output:\n%s", err, out)
				}
				// AST walk: no q.<entry>(...) call expressions should
				// survive in the rewritten output. The appended sentinel
				// `var _ = <alias>.ErrNil` is an ident *value* reference,
				// not a call, so it's invisible to a CallExpr walk.
				ast.Inspect(rewritten, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					x, ok := sel.X.(*ast.Ident)
					if !ok {
						return true
					}
					if x.Name != alias {
						return true
					}
					// One of the four entry helpers surviving the
					// rewrite is a bug — chain calls (q.TryE(...).Method())
					// also count: their outer .Method() is a CallExpr
					// whose Fun is a SelectorExpr whose X is the
					// q.TryE(...) sub-call, which we'd catch on this
					// inner pass.
					switch sel.Sel.Name {
					case "Try", "TryE", "NotNil", "NotNilE":
						t.Errorf("rewriter left a q.%s call un-erased at %s",
							sel.Sel.Name, fset.Position(call.Pos()))
					}
					return true
				})
			})
		}
	}
}

func TestRewriteMethodOnGenericReceiver(t *testing.T) {
	// Regression: q.Try inside a method on a generic type. The
	// rewriter pulls the result type T from the enclosing FuncDecl;
	// methods on Box[T] must work the same as plain generic functions.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

type Box[T any] struct {
	v   T
	err error
}

func (b Box[T]) read() (T, error) { return b.v, b.err }

func (b Box[T]) Get() (T, error) {
	v := q.Try(b.read())
	return v, nil
}
`
	got := mustRewrite(t, src)
	t.Logf("rewritten:\n%s", got)
	if !strings.Contains(got, "v, _qErr1 := b.read()") {
		t.Errorf("Box[T].Get rewrite missing.\n--- output:\n%s", got)
	}
	if !strings.Contains(got, "*new(T)") {
		t.Errorf("expected *new(T) zero-value with the type-parameter name.\n--- output:\n%s", got)
	}
}

func TestRewriteTryEErr_ReplacementError(t *testing.T) {
	// Err passes a constant error that should appear as the bubbled
	// value in place of the captured err.
	src := `package p

import (
	"errors"
	"github.com/GiGurra/q/pkg/q"
)

var ErrCustom = errors.New("custom")

func parse(s string) (int, error) {
	n := q.TryE(atoi(s)).Err(ErrCustom)
	return n, nil
}

func atoi(s string) (int, error) { return 0, nil }
`
	got := mustRewrite(t, src)
	want := "return *new(int), ErrCustom"
	if !strings.Contains(got, want) {
		t.Errorf("missing %q.\n--- output:\n%s", want, got)
	}
}

func TestRewriteTryReturn_BasicShape(t *testing.T) {
	// `return q.Try(call()), nil` — q.* sits as one top-level result in
	// a return statement. The rewriter binds to `_qTmp1`, emits the
	// bubble block, and reconstructs the return with `_qTmp1` spliced
	// in place of the q.Try(...) sub-expression.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func parse(s string) (int, error) {
	return q.Try(atoi(s)), nil
}

func atoi(s string) (int, error) { return 0, nil }
`
	got := mustRewrite(t, src)
	wants := []string{
		"_qTmp1, _qErr1 := atoi(s)",
		"if _qErr1 != nil {",
		"return *new(int), _qErr1",
		"return _qTmp1, nil",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q.\n--- output:\n%s", w, got)
		}
	}
	if strings.Contains(got, "q.Try(") {
		t.Errorf("q.Try call survived rewrite.\n--- output:\n%s", got)
	}
}

func TestRewriteTryReturn_NestedInExpression(t *testing.T) {
	// `return q.Try(call()) * 2, nil` — q.* is nested inside an
	// arithmetic expression in the first return result. The rewriter
	// must still recognise it, bind the q.Try call to `_qTmp1`, and
	// rebuild the return with just that sub-expression substituted.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func parseDouble(s string) (int, error) {
	return q.Try(atoi(s)) * 2, nil
}

func atoi(s string) (int, error) { return 0, nil }
`
	got := mustRewrite(t, src)
	wants := []string{
		"_qTmp1, _qErr1 := atoi(s)",
		"return _qTmp1 * 2, nil",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q.\n--- output:\n%s", w, got)
		}
	}
	if strings.Contains(got, "q.Try(") {
		t.Errorf("q.Try call survived rewrite.\n--- output:\n%s", got)
	}
}

func TestRewriteTryEReturn_WrapChain(t *testing.T) {
	// Chain method on a return-position q.TryE: error-branch wraps via
	// fmt.Errorf, success-branch substitutes the temp into the rebuilt
	// return.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func parse(s string) (int, error) {
	return q.TryE(atoi(s)).Wrap("parsing"), nil
}

func atoi(s string) (int, error) { return 0, nil }
`
	got := mustRewrite(t, src)
	wants := []string{
		"_qTmp1, _qErr1 := atoi(s)",
		`fmt.Errorf("%s: %w", "parsing", _qErr1)`,
		"return _qTmp1, nil",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q.\n--- output:\n%s", w, got)
		}
	}
}

func TestRewriteNotNilReturn_MidPosition(t *testing.T) {
	// q.NotNil as the middle of three return results. The final return
	// should preserve the surrounding expressions verbatim and only
	// substitute the q.NotNil(p) span with _qTmp1.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func pick(p *int) (string, *int, error) {
	return "tag", q.NotNil(p), nil
}
`
	got := mustRewrite(t, src)
	wants := []string{
		"_qTmp1 := p",
		"if _qTmp1 == nil {",
		"return *new(string), *new(*int), q.ErrNil",
		`return "tag", _qTmp1, nil`,
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q.\n--- output:\n%s", w, got)
		}
	}
}

func TestRewriteTryAssign_NoQImport_NoChange(t *testing.T) {
	src := `package p

func plain(s string) (int, error) {
	return 0, nil
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	shapes, diags, err := scanFile(fset, "p.go", file)
	if err != nil {
		t.Fatal(err)
	}
	if len(shapes) != 0 || len(diags) != 0 {
		t.Errorf("file without q import should yield no shapes and no diags, got %d shapes, %d diags", len(shapes), len(diags))
	}
}

// mustRewrite runs the full scan + rewrite pipeline on src and
// returns the rewritten bytes as a string. Fatal on any error.
func mustRewrite(t *testing.T, src string) string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	shapes, diags, err := scanFile(fset, "p.go", file)
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if len(shapes) == 0 {
		t.Fatal("scanner returned no shapes")
	}
	alias := qImportAlias(file)
	out, _, err := rewriteFile(fset, file, []byte(src), shapes, alias)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
