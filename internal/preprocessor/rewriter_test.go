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
		// Skip negative fixtures — they exist precisely to produce
		// scan-time diagnostics; including them in this "should
		// scan cleanly" check would always fail.
		if strings.HasSuffix(e.Name(), "_rejected") {
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
				out, _, err := rewriteFile(fset, file, data, shapes, alias, "", nil)
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
					case "Try", "TryE", "NotNil", "NotNilE",
						"Check", "CheckE", "Open", "OpenE":
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

func TestRewriteMultipleQInReturn(t *testing.T) {
	// `return q.Try(a()) * q.Try(b()) / q.Try(c()), nil` — three q.*
	// calls in one return expression. Each binds to its own temp,
	// each has its own bubble check, and the final return substitutes
	// all three spans.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func compute(s string) (int, error) {
	return q.Try(atoi(s)) * q.Try(atoi(s)) / q.Try(atoi(s)), nil
}

func atoi(s string) (int, error) { return 0, nil }
`
	got := mustRewrite(t, src)
	wants := []string{
		"_qTmp1, _qErr1 := atoi(s)",
		"if _qErr1 != nil {",
		"_qTmp2, _qErr2 := atoi(s)",
		"if _qErr2 != nil {",
		"_qTmp3, _qErr3 := atoi(s)",
		"if _qErr3 != nil {",
		"return _qTmp1 * _qTmp2 / _qTmp3, nil",
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

func TestRewriteMixedFamiliesInReturn(t *testing.T) {
	// Mix Try and NotNil in a single return to confirm per-sub-call
	// family dispatch still works when rendered together.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func mix(s string, p *int) (int, error) {
	return q.Try(atoi(s)) + *q.NotNil(p), nil
}

func atoi(s string) (int, error) { return 0, nil }
`
	got := mustRewrite(t, src)
	wants := []string{
		"_qTmp1, _qErr1 := atoi(s)",
		"if _qErr1 != nil {",
		"_qTmp2 := p",
		"if _qTmp2 == nil {",
		"return *new(int), q.ErrNil",
		"return _qTmp1 + *_qTmp2, nil",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q.\n--- output:\n%s", w, got)
		}
	}
}

func TestRewriteHoist_NestedInCallRHS(t *testing.T) {
	// `v := f(q.Try(call()))` — q.Try is nested inside the RHS call,
	// not the direct RHS. Rewriter must hoist it to a preceding bind
	// + check, then rebuild the AssignStmt with the q.* span replaced.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func compute(s string) (int, error) {
	v := double(q.Try(atoi(s)))
	return v, nil
}

func atoi(s string) (int, error) { return 0, nil }
func double(n int) int           { return n * 2 }
`
	got := mustRewrite(t, src)
	wants := []string{
		"_qTmp1, _qErr1 := atoi(s)",
		"if _qErr1 != nil {",
		"return *new(int), _qErr1",
		"v := double(_qTmp1)",
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

func TestRewriteHoist_MultiLHS(t *testing.T) {
	// `a, b := fn(q.Try(call()))` — multi-LHS where the RHS call
	// returns (T1, T2) and itself takes a q.*. The direct-bind path
	// rejects multi-LHS; hoist handles it.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func pick(s string) (int, string, error) {
	a, b := split(q.Try(atoi(s)))
	return a, b, nil
}

func atoi(s string) (int, error)    { return 0, nil }
func split(n int) (int, string)     { return n, "x" }
`
	got := mustRewrite(t, src)
	wants := []string{
		"_qTmp1, _qErr1 := atoi(s)",
		"a, b := split(_qTmp1)",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q.\n--- output:\n%s", w, got)
		}
	}
}

func TestRewriteHoist_ExprStmtNested(t *testing.T) {
	// `f(q.Try(call()))` as an expression statement. No LHS; after
	// hoisting the q.*, the reconstructed stmt just calls f(_qTmp1)
	// for its side effect.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func run(s string) error {
	sink(q.Try(atoi(s)))
	return nil
}

func atoi(s string) (int, error) { return 0, nil }
func sink(n int)                 {}
`
	got := mustRewrite(t, src)
	wants := []string{
		"_qTmp1, _qErr1 := atoi(s)",
		"sink(_qTmp1)",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q.\n--- output:\n%s", w, got)
		}
	}
}

func TestRewriteHoist_QInWrapArg(t *testing.T) {
	// q.* passed as the argument to a chain method (Wrap takes a
	// string, so Wrap(q.Try(...)) doesn't type-check — but ErrF
	// takes a func(error) error value, so
	// q.TryE(f()).ErrF(mkFn(q.Try(g()))) is legitimate where mkFn
	// builds the error-transform fn from a T.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func run(s string) (int, error) {
	v := q.TryE(f()).ErrF(mkFn(q.Try(g(s))))
	return v, nil
}

func f() (int, error)         { return 0, nil }
func g(s string) (int, error) { return 0, nil }
func mkFn(n int) func(error) error { return func(e error) error { return e } }
`
	got := mustRewrite(t, src)
	wants := []string{
		"_qTmp1, _qErr1 := g(s)",
		"_qTmp2, _qErr2 := f()",
		"(mkFn(_qTmp1))(_qErr2)",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q.\n--- output:\n%s", w, got)
		}
	}
}

func TestRewriteHoist_NestedQInsideQInner(t *testing.T) {
	// `x := q.Try(Foo(q.Try(Bar())))` — outer q.Try's InnerExpr
	// itself contains an inner q.Try. The rewriter must hoist the
	// innermost q.Try first so its result feeds the outer's bind
	// line as a temp.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func compute() (int, error) {
	x := q.Try(Foo(q.Try(Bar())))
	return x, nil
}

func Bar() (int, error)   { return 0, nil }
func Foo(n int) (int, error) { return n, nil }
`
	got := mustRewrite(t, src)
	wants := []string{
		"_qTmp1, _qErr1 := Bar()",
		"if _qErr1 != nil {",
		"_qTmp2, _qErr2 := Foo(_qTmp1)",
		"if _qErr2 != nil {",
		"x := _qTmp2",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q.\n--- output:\n%s", w, got)
		}
	}
	if strings.Contains(got, "q.Try(") {
		t.Errorf("q.Try call survived rewrite (nested q.* not rewritten).\n--- output:\n%s", got)
	}
}

func TestRewriteHoist_MultipleInOneStatement(t *testing.T) {
	// Two q.* calls nested in one RHS expression. Each binds
	// separately with its own counter.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func add(a, b string, p *int) (int, error) {
	v := sum(q.Try(atoi(a)), q.Try(atoi(b))) + *q.NotNil(p)
	return v, nil
}

func atoi(s string) (int, error) { return 0, nil }
func sum(x, y int) int           { return x + y }
`
	got := mustRewrite(t, src)
	wants := []string{
		"_qTmp1, _qErr1 := atoi(a)",
		"_qTmp2, _qErr2 := atoi(b)",
		"_qTmp3 := p",
		"if _qTmp3 == nil {",
		"v := sum(_qTmp1, _qTmp2) + *_qTmp3",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q.\n--- output:\n%s", w, got)
		}
	}
}

func TestRewriteCheck_Bare(t *testing.T) {
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func run() error {
	q.Check(closeIt())
	return nil
}

func closeIt() error { return nil }
`
	got := mustRewrite(t, src)
	wants := []string{
		"_qErr1 := closeIt()",
		"if _qErr1 != nil {",
		"return _qErr1",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q.\n--- output:\n%s", w, got)
		}
	}
	if strings.Contains(got, "q.Check(") {
		t.Errorf("q.Check survived rewrite.\n--- output:\n%s", got)
	}
}

func TestRewriteCheckE_WrapAndCatch(t *testing.T) {
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func doWrap() error {
	q.CheckE(closeIt()).Wrap("finalising")
	return nil
}

func doCatch() error {
	q.CheckE(closeIt()).Catch(swallowTimeouts)
	return nil
}

func closeIt() error                     { return nil }
func swallowTimeouts(err error) error    { return err }
`
	got := mustRewrite(t, src)
	wants := []string{
		`fmt.Errorf("%s: %w", "finalising", _qErr1)`,
		"_qRet2 := (swallowTimeouts)(_qErr2)",
		"if _qRet2 != nil {",
		"return _qRet2",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q.\n--- output:\n%s", w, got)
		}
	}
}

func TestRewriteOpen_BareDefine(t *testing.T) {
	src := `package p

import "github.com/GiGurra/q/pkg/q"

type Conn struct{}

func (*Conn) Close() {}

func acquire() (int, error) {
	conn := q.Open(dial()).Release((*Conn).Close)
	_ = conn
	return 0, nil
}

func dial() (*Conn, error) { return nil, nil }
`
	got := mustRewrite(t, src)
	wants := []string{
		"conn, _qErr1 := dial()",
		"if _qErr1 != nil {",
		"return *new(int), _qErr1",
		"defer ((*Conn).Close)(conn)",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q.\n--- output:\n%s", w, got)
		}
	}
	if strings.Contains(got, "q.Open(") {
		t.Errorf("q.Open survived rewrite.\n--- output:\n%s", got)
	}
}

func TestRewriteOpenE_WrapChain(t *testing.T) {
	src := `package p

import "github.com/GiGurra/q/pkg/q"

type Conn struct{}

func (*Conn) Close() {}

func acquire() (int, error) {
	conn := q.OpenE(dial()).Wrap("dialing").Release((*Conn).Close)
	_ = conn
	return 0, nil
}

func dial() (*Conn, error) { return nil, nil }
`
	got := mustRewrite(t, src)
	wants := []string{
		"conn, _qErr1 := dial()",
		`fmt.Errorf("%s: %w", "dialing", _qErr1)`,
		"defer ((*Conn).Close)(conn)",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q.\n--- output:\n%s", w, got)
		}
	}
}

func TestRewriteOpen_ReturnPosition(t *testing.T) {
	src := `package p

import "github.com/GiGurra/q/pkg/q"

type Conn struct{}

func (*Conn) Close() {}

func get() (*Conn, error) {
	return q.Open(dial()).Release((*Conn).Close), nil
}

func dial() (*Conn, error) { return nil, nil }
`
	got := mustRewrite(t, src)
	wants := []string{
		"_qTmp1, _qErr1 := dial()",
		"if _qErr1 != nil {",
		"defer ((*Conn).Close)(_qTmp1)",
		"return _qTmp1, nil",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q.\n--- output:\n%s", w, got)
		}
	}
}

func TestRewriteEmitsLineDirectives(t *testing.T) {
	// When origPath is supplied, the output must start with a
	// //line directive pointing at that path, and each rewrite must
	// be followed by a //line directive resetting to the line after
	// the original statement. Together these make DWARF point at
	// the user's file (so IDE breakpoints match) instead of the
	// preprocessor's tempdir path.
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func run(s string) (int, error) {
	n := q.Try(atoi(s))
	return n, nil
}

func atoi(s string) (int, error) { return 0, nil }
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
	if len(diags) > 0 {
		t.Fatalf("unexpected scan diagnostics: %v", diags)
	}
	alias := qImportAlias(file)

	out, _, err := rewriteFile(fset, file, []byte(src), shapes, alias, "/home/user/proj/p.go", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	// The rewritten file must start with a file-level //line
	// directive pointing at the supplied path.
	if !strings.HasPrefix(got, "//line /home/user/proj/p.go:1\n") {
		t.Errorf("missing file-level //line prefix; got: %q", firstLine(got))
	}

	// The q.Try rewrite was on source line 6 (1-indexed); its
	// `return n, nil` follow-up is on line 7. After the rewrite
	// expansion, we expect a //line directive resetting to line 7.
	if !strings.Contains(got, "//line /home/user/proj/p.go:7") {
		t.Errorf("missing per-edit //line reset after the q.Try rewrite.\n--- output:\n%s", got)
	}

	// The rewritten output must still parse as valid Go.
	if _, err := parser.ParseFile(token.NewFileSet(), "p.go", out, parser.ParseComments); err != nil {
		t.Errorf("rewritten output failed to parse: %v\n--- output:\n%s", err, got)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func TestRewriteLineDirectiveSurvivesTrailingComment(t *testing.T) {
	// Regression: when the rewritten statement has a trailing `//
	// note` on the same source line, the per-edit //line directive
	// must end with a newline so the user's comment doesn't land on
	// the same physical line as the directive. Otherwise Go parses
	// `//line /path:N // note` as one directive and rejects
	// "invalid line number: N // note".
	src := `package p

import "github.com/GiGurra/q/pkg/q"

func f() error {
	q.Check(func() error { return nil }()) // trailing comment stays valid
	return nil
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
	if len(diags) > 0 {
		t.Fatalf("unexpected scan diagnostics: %v", diags)
	}
	alias := qImportAlias(file)

	out, _, err := rewriteFile(fset, file, []byte(src), shapes, alias, "/abs/path/p.go", nil)
	if err != nil {
		t.Fatal(err)
	}

	// The //line directive and the user's trailing comment must
	// occupy separate lines.
	lines := strings.Split(string(out), "\n")
	for i, line := range lines {
		if strings.Contains(line, "//line /abs/path/p.go:") && strings.Contains(line, "trailing comment") {
			t.Errorf("line directive and user trailing comment collide on line %d: %q", i+1, line)
		}
	}

	// And the output must parse cleanly.
	if _, err := parser.ParseFile(token.NewFileSet(), "p.go", out, parser.ParseComments); err != nil {
		t.Errorf("rewritten output failed to parse: %v\n--- output:\n%s", err, out)
	}
}

func TestRewriteScan_ValueRefToQErrNilIsNotFlagged(t *testing.T) {
	// Regression: findQReference used to flag *any* selector rooted at
	// the q alias, including plain value references like
	// `errors.Is(err, q.ErrNil)`. That is a legitimate exported
	// sentinel — no call to rewrite. The scanner should only flag
	// q.* call expressions.
	src := `package p

import (
	"errors"

	"github.com/GiGurra/q/pkg/q"
)

func check(p *int) error {
	_, err := lookup(p)
	if errors.Is(err, q.ErrNil) {
		return errors.New("missed")
	}
	return nil
}

func lookup(p *int) (int, error) {
	v := q.NotNil(p)
	return *v, nil
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
	if len(diags) > 0 {
		t.Errorf("unexpected diagnostics for q.ErrNil value reference: %v", diags)
	}
	// One shape: q.NotNil(p) in the second func.
	if len(shapes) != 1 {
		t.Errorf("expected exactly one recognised shape; got %d", len(shapes))
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
	out, _, err := rewriteFile(fset, file, []byte(src), shapes, alias, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
