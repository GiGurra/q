package preprocessor

// Unit tests for the rewriter. Black-box-style: feed a small source
// string in, run scan + rewrite, assert on the rewritten output. These
// are the fastest signal when iterating on rewriter behavior — the e2e
// fixtures cost ~half a second each because they invoke `go build`.

import (
	"go/parser"
	"go/token"
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
	out, err := rewriteFile(fset, []byte(src), shapes, alias)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
