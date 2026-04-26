package preprocessor

// assemble_unit_test.go — fast in-process tests for the q.Assemble
// resolver. These run in milliseconds vs the e2e fixtures' ~0.5s,
// letting us iterate on diagnostic wording and edge cases without
// the full toolexec build cycle.
//
// The test harness type-checks pkg/q from source on first use and
// installs a custom importer that returns the resulting types.Package
// when test sources reference "github.com/GiGurra/q/pkg/q". Stdlib
// imports flow through importer.Default(). This avoids the link-gate
// problem with blank-importing pkg/q into the test binary (the
// _q_atCompileTime relocation only resolves under -toolexec=q).

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// unitRepoRoot returns the absolute path to the repository root,
// computed from this test file's location. Mirrors repoRoot() in
// e2e_test.go (which lives in the _test package and isn't reachable
// from internal-package tests).
func unitRepoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// qPkgCache memoises the type-checked pkg/q package so the source
// parse + check (~few ms) only happens once per `go test` run.
var qPkgCache struct {
	once sync.Once
	pkg  *types.Package
	err  error
}

// qPackagePath is the import path test sources use to reference q.
// Kept as a constant so changing the module path only touches one
// spot. Mirrors qPkgImportPath in scanner.go.
const qPackagePath = "github.com/GiGurra/q/pkg/q"

// loadQPackage parses every *.go in pkg/q under repoRoot() and runs
// go/types over them, returning the resulting *types.Package. Used
// by the custom importer below so test sources can `import
// "github.com/GiGurra/q/pkg/q"` without dragging the real binary's
// link gate into the test process.
func loadQPackage() (*types.Package, error) {
	qPkgCache.once.Do(func() {
		dir := filepath.Join(unitRepoRoot(), "pkg", "q")
		entries, err := os.ReadDir(dir)
		if err != nil {
			qPkgCache.err = fmt.Errorf("read pkg/q dir: %w", err)
			return
		}
		fset := token.NewFileSet()
		var files []*ast.File
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
			if err != nil {
				qPkgCache.err = fmt.Errorf("parse %s: %w", e.Name(), err)
				return
			}
			files = append(files, f)
		}
		cfg := &types.Config{
			Importer: importer.Default(), // pkg/q's own deps are stdlib
			Error:    func(error) {},     // swallow noise from typecheck
		}
		pkg, err := cfg.Check("q", fset, files, nil)
		if err != nil {
			// Don't fail — partial Pkg may still be usable for the
			// symbols our tests need (Assemble, ErrNil, etc.). Real
			// fatal failures surface as missing-symbol errors at the
			// per-test importer level.
			_ = err
		}
		qPkgCache.pkg = pkg
	})
	return qPkgCache.pkg, qPkgCache.err
}

// qShimImporter returns the cached pkg/q types.Package for the q
// import path; falls through to importer.Default() for everything
// else (stdlib).
type qShimImporter struct {
	fallback types.Importer
}

func (i *qShimImporter) Import(path string) (*types.Package, error) {
	if path == qPackagePath {
		return loadQPackage()
	}
	return i.fallback.Import(path)
}

// analyzeAssembleSrc runs the scanner + typecheck pipeline on a Go
// source string and returns:
//   - diags: every diagnostic the typecheck pass emitted (one per
//     problematic call site).
//   - shapes: scanner-classified call shapes, for tests that want to
//     inspect resolved AssembleSteps directly.
//   - parseErr: a fatal parse error from the test src itself. Tests
//     should fail on this — it means the test src is malformed.
//
// The src must declare its own package and import qPackagePath
// (under any alias) when it uses q.Assemble.
func analyzeAssembleSrc(t *testing.T, src string) (diags []Diagnostic, shapes []callShape, parseErr error) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "test.go", src, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("parse: %w", err)
	}
	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Uses:  map[*ast.Ident]types.Object{},
		Defs:  map[*ast.Ident]types.Object{},
	}
	cfg := &types.Config{
		Importer: &qShimImporter{fallback: importer.Default()},
		Error:    func(error) {}, // swallow type errors; we test resolver diags
	}
	_, _ = cfg.Check("main", fset, []*ast.File{file}, info)

	gotShapes, scanDiags, scanErr := scanFile(fset, "test.go", file)
	if scanErr != nil {
		return nil, nil, fmt.Errorf("scan: %w", scanErr)
	}
	diags = append(diags, scanDiags...)

	errType := types.Universe.Lookup("error").Type()
	for i := range gotShapes {
		for j := range gotShapes[i].Calls {
			sc := &gotShapes[i].Calls[j]
			if d, ok := validateSlot(fset, *sc, info, errType); ok {
				diags = append(diags, d)
			}
			if isAssembleFamily(sc.Family) {
				if d, ok := resolveAssemble(fset, sc, info, "main"); ok {
					diags = append(diags, d)
				}
			}
		}
	}
	return diags, gotShapes, nil
}

// joinDiagMsgs concatenates every diagnostic's Msg field into one
// string with newlines between, suitable for substring assertions.
func joinDiagMsgs(diags []Diagnostic) string {
	var b strings.Builder
	for _, d := range diags {
		b.WriteString(d.Msg)
		b.WriteByte('\n')
	}
	return b.String()
}

// requireDiagContains fails the test when joined diagnostic text is
// missing any of `wants`. Reports each missing substring separately
// so a single failed test surfaces every gap at once.
func requireDiagContains(t *testing.T, diags []Diagnostic, wants ...string) {
	t.Helper()
	got := joinDiagMsgs(diags)
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("diagnostic missing required substring %q\nfull text:\n%s", w, got)
		}
	}
}

// requireNoDiags fails the test when any diagnostic was produced.
func requireNoDiags(t *testing.T, diags []Diagnostic) {
	t.Helper()
	if len(diags) != 0 {
		t.Fatalf("expected zero diagnostics; got %d:\n%s", len(diags), joinDiagMsgs(diags))
	}
}

// TestUnitImporterWorks is a sanity check that the qShim importer can
// resolve pkg/q. If this fails, every other unit test would
// silently pass (no q.Assemble found → no diagnostics → assertions
// vacuously hold) so we surface the configuration problem here.
func TestUnitImporterWorks(t *testing.T) {
	src := `package main
import "github.com/GiGurra/q/pkg/q"
func main() {
	_ = q.ErrNil
}
`
	if _, _, err := analyzeAssembleSrc(t, src); err != nil {
		t.Fatalf("type-checking pkg/q failed; importer setup is broken: %v", err)
	}
}

// TestUnitAssembleDiagnostics drives the resolver through the full
// diagnostic matrix in a single fast (~ms) table. Each case is a
// minimal Go src that exercises one (or several combined) failure
// modes; assertions are substring matches against the joined
// diagnostic text. New diagnostic shapes should be added here first
// — iteration is dramatically faster than the e2e fixture round
// trip — and then mirrored as an e2e fixture for the integration
// guarantee.
func TestUnitAssembleDiagnostics(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantSubs []string // every substring must appear in joined diags
	}{
		{
			name: "missing recipe",
			src: `package main
import "github.com/GiGurra/q/pkg/q"
type Cfg struct{}
type DB struct{}
type Server struct{}
func newCfg() *Cfg                         { return nil }
func newServer(d *DB, c *Cfg) *Server      { return nil }
func main() { _, _ = q.Assemble[*Server](newCfg, newServer).Release() }
`,
			wantSubs: []string{
				"missing recipe for *DB",
				"needed by #2 (newServer)",
				"What the resolver sees:",
				"input *DB ?? (no recipe provides this)",
			},
		},
		{
			name: "duplicate provider",
			src: `package main
import "github.com/GiGurra/q/pkg/q"
type Cfg struct{}
func newA() *Cfg { return nil }
func newB() *Cfg { return nil }
func main() { _, _ = q.Assemble[*Cfg](newA, newB).Release() }
`,
			wantSubs: []string{
				"duplicate provider for *Cfg",
				"#1 (newA)",
				"#2 (newB)",
				"pick one or define distinct named types",
			},
		},
		{
			name: "dependency cycle",
			src: `package main
import "github.com/GiGurra/q/pkg/q"
type A struct{}
type B struct{}
type Root struct{}
func newA(b *B) *A     { return nil }
func newB(a *A) *B     { return nil }
func newRoot(a *A) *Root { return nil }
func main() { _, _ = q.Assemble[*Root](newA, newB, newRoot).Release() }
`,
			wantSubs: []string{
				"dependency cycle:",
				"*A (#1 (newA))",
				"*B (#2 (newB))",
			},
		},
		{
			name: "unsatisfiable target",
			src: `package main
import "github.com/GiGurra/q/pkg/q"
type Cfg struct{}
type Server struct{}
func newCfg() *Cfg { return nil }
func main() { _, _ = q.Assemble[*Server](newCfg).Release() }
`,
			wantSubs: []string{
				"target type *Server is not produced by any recipe",
				"Providers supplied: #1→*Cfg",
			},
		},
		{
			name: "unused recipe with dep tree",
			src: `package main
import "github.com/GiGurra/q/pkg/q"
type Cfg struct{}
type DB struct{}
func newCfg() *Cfg            { return nil }
func newDB(c *Cfg) *DB        { return nil }
func unrelated() string       { return "" }
func main() { _, _ = q.Assemble[*DB](newCfg, newDB, unrelated).Release() }
`,
			wantSubs: []string{
				"unused recipe(s):",
				"#3 (unrelated) — provides string",
				"The target type *DB requires:",
				"*DB <- #2 (newDB) [fn]",
				"*Cfg <- #1 (newCfg) [fn]",
			},
		},
		{
			name: "interface ambiguity",
			src: `package main
import "github.com/GiGurra/q/pkg/q"
type Greeter interface{ Greet() string }
type EN struct{}
type ES struct{}
func (EN) Greet() string  { return "" }
func (ES) Greet() string  { return "" }
type App struct{}
func newEN() *EN          { return nil }
func newES() *ES          { return nil }
func newApp(g Greeter) *App { return nil }
func main() { _, _ = q.Assemble[*App](newEN, newES, newApp).Release() }
`,
			wantSubs: []string{
				"interface input Greeter",
				"is satisfied by multiple providers",
				"#1 (newEN) → *EN",
				"#2 (newES) → *ES",
				"narrow the recipe set or define distinct named types",
			},
		},
		{
			name: "recipe with no return",
			src: `package main
import "github.com/GiGurra/q/pkg/q"
type DB struct{}
func sideEffect()    {}
func newDB() *DB     { return nil }
func main() { _, _ = q.Assemble[*DB](sideEffect, newDB).Release() }
`,
			wantSubs: []string{
				"recipe #1 (sideEffect) returns no values",
				"recipes must return T, (T, error), or (T, func(), error)",
			},
		},
		{
			name: "recipe 3 returns wrong second-type",
			src: `package main
import "github.com/GiGurra/q/pkg/q"
type DB struct{}
func newDB() (*DB, string, error) { return nil, "", nil }
func main() { _, _ = q.Assemble[*DB](newDB).Release() }
`,
			wantSubs: []string{
				"second return is string",
				"for resource recipes the second return must be `func()`",
			},
		},
		{
			name: "recipe with 4 returns",
			src: `package main
import "github.com/GiGurra/q/pkg/q"
type DB struct{}
func newDB() (*DB, string, int, error) { return nil, "", 0, nil }
func main() { _, _ = q.Assemble[*DB](newDB).Release() }
`,
			wantSubs: []string{
				"returns 4 values",
			},
		},
		{
			name: "recipe with non-error second return",
			src: `package main
import "github.com/GiGurra/q/pkg/q"
type DB struct{}
type MyErr struct{}
func (e *MyErr) Error() string { return "" }
func newDB() (*DB, *MyErr) { return nil, nil }
func main() { _, _ = q.Assemble[*DB](newDB).Release() }
`,
			wantSubs: []string{
				"second return is *MyErr",
				"recipes must return T, (T, error), or (T, func(), error)",
			},
		},
		{
			name: "variadic recipe",
			src: `package main
import "github.com/GiGurra/q/pkg/q"
type DB struct{}
func newDB(extras ...string) *DB { return nil }
func main() { _, _ = q.Assemble[*DB](newDB).Release() }
`,
			wantSubs: []string{
				"is variadic",
				"wrap it in a fixed-arity adapter",
			},
		},
		{
			name: "combined errors — duplicate + missing",
			src: `package main
import "github.com/GiGurra/q/pkg/q"
type Cfg struct{}
type Cache struct{}
type Server struct{}
func newCfg()                          *Cfg    { return nil }
func newOther()                        *Cfg    { return nil }
func newServer(c *Cfg, ch *Cache)      *Server { return nil }
func main() { _, _ = q.Assemble[*Server](newCfg, newOther, newServer).Release() }
`,
			wantSubs: []string{
				"duplicate provider for *Cfg",
				"missing recipe for *Cache",
				"What the resolver sees:",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			diags, _, err := analyzeAssembleSrc(t, tc.src)
			if err != nil {
				t.Fatalf("analyze: %v", err)
			}
			if len(diags) == 0 {
				t.Fatalf("expected at least one diagnostic; got none. wanted substrings: %v", tc.wantSubs)
			}
			requireDiagContains(t, diags, tc.wantSubs...)
		})
	}
}

// TestUnitAssembleCtxAsInlineValue exercises ctx-as-inline-value:
// a context.Context value passed as a recipe arg is matched to a
// recipe input via interface satisfaction. ctx is recipe #1 in the
// user's list with normal 1-based numbering (no special treatment).
func TestUnitAssembleCtxAsInlineValue(t *testing.T) {
	src := `package main

import (
	"context"

	"github.com/GiGurra/q/pkg/q"
)

type Cfg struct{}
type DB struct{ ctx context.Context; cfg *Cfg }

func newCfg() *Cfg                              { return &Cfg{} }
func newDB(ctx context.Context, c *Cfg) *DB    { return &DB{ctx: ctx, cfg: c} }

func main() {
	_, _ = q.Assemble[*DB](context.Background(), newCfg, newDB).Release()
}
`
	diags, shapes, err := analyzeAssembleSrc(t, src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	requireNoDiags(t, diags)
	steps := shapes[0].Calls[0].AssembleSteps
	if len(steps) != 3 {
		t.Fatalf("expected 3 topo-sorted steps; got %d", len(steps))
	}
	// ctx is recipe #1 (the first inline value); newCfg is #2; newDB
	// is #3. Topo: ctx and newCfg are leaves; newDB requires both.
	// AssembleCtxDepKey must be set so the rewriter knows to bind
	// _qDbg from the ctx provider for the optional debug-trace
	// prelude.
	if shapes[0].Calls[0].AssembleCtxDepKey == "" {
		t.Errorf("AssembleCtxDepKey should be set when a ctx provider exists")
	}
}

// TestUnitAssembleCtxOnlyForDebug verifies that supplying ctx purely
// for assembly-config — no recipe consumes it — does NOT trigger
// the unused-recipe diagnostic. context.Context is exempt because
// it's expected to ride into the assembly for debug / future hooks.
func TestUnitAssembleCtxOnlyForDebug(t *testing.T) {
	src := `package main

import (
	"context"

	"github.com/GiGurra/q/pkg/q"
)

type Cfg struct{}
type DB struct{ cfg *Cfg }

// No recipe takes context.Context — ctx is supplied purely for
// assembly-config (debug etc).
func newCfg() *Cfg     { return &Cfg{} }
func newDB(c *Cfg) *DB { return &DB{cfg: c} }

func main() {
	_, _ = q.Assemble[*DB](context.Background(), newCfg, newDB).Release()
}
`
	diags, shapes, err := analyzeAssembleSrc(t, src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	requireNoDiags(t, diags) // ctx is exempt from unused-recipe check
	if shapes[0].Calls[0].AssembleCtxDepKey == "" {
		t.Errorf("AssembleCtxDepKey should be set even when ctx has no consumer (debug still works)")
	}
}

// TestUnitAssembleAllHappyPath exercises q.AssembleAll[T] with three
// distinct concrete plugins implementing a common interface. The
// resolver must collect all three providers (rather than rejecting
// the multi-provider case as q.Assemble would) and the topo-sorted
// step list must include each provider exactly once.
func TestUnitAssembleAllHappyPath(t *testing.T) {
	src := `package main

import "github.com/GiGurra/q/pkg/q"

type Plugin interface{ Name() string }

type authP struct{}
type logP struct{}
type metricsP struct{}

func (authP) Name() string    { return "auth" }
func (logP) Name() string     { return "log" }
func (metricsP) Name() string { return "metrics" }

func newAuth()    Plugin { return authP{} }
func newLog()     Plugin { return logP{} }
func newMetrics() Plugin { return metricsP{} }

func main() {
	_, _ = q.AssembleAll[Plugin](newAuth, newLog, newMetrics).Release()
}
`
	diags, shapes, err := analyzeAssembleSrc(t, src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	requireNoDiags(t, diags)
	if len(shapes) != 1 || len(shapes[0].Calls) != 1 {
		t.Fatalf("expected one call shape; got %d shapes", len(shapes))
	}
	sc := shapes[0].Calls[0]
	if sc.Family != familyAssembleAll {
		t.Fatalf("expected familyAssembleAll; got %v", sc.Family)
	}
	if len(sc.AssembleAllProviderRidxs) != 3 {
		t.Fatalf("expected 3 provider ridxs; got %d", len(sc.AssembleAllProviderRidxs))
	}
	if len(sc.AssembleSteps) != 3 {
		t.Fatalf("expected 3 topo-sorted steps; got %d", len(sc.AssembleSteps))
	}
}

// TestUnitAssembleAllConcreteSameType — multiple recipes producing
// the SAME concrete target type is the natural multi-element case
// for AssembleAll: each recipe contributes one element to the
// resulting slice. (Contrast with q.Assemble[*Cfg] where this is
// rejected as an ambiguous duplicate-provider situation.)
func TestUnitAssembleAllConcreteSameType(t *testing.T) {
	src := `package main
import "github.com/GiGurra/q/pkg/q"
type Cfg struct{}
func newA() *Cfg { return nil }
func newB() *Cfg { return nil }
func main() { _, _ = q.AssembleAll[*Cfg](newA, newB).Release() }
`
	diags, shapes, err := analyzeAssembleSrc(t, src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	requireNoDiags(t, diags)
	sc := shapes[0].Calls[0]
	if len(sc.AssembleAllProviderRidxs) != 2 {
		t.Fatalf("expected 2 provider ridxs; got %d", len(sc.AssembleAllProviderRidxs))
	}
	if len(sc.AssembleSteps) != 2 {
		t.Fatalf("expected 2 topo-sorted steps; got %d", len(sc.AssembleSteps))
	}
}

// TestUnitAssembleAllNoProviders — calling AssembleAll[T] with no
// recipe whose output is assignable to T must error. (The would-be
// success path produces an empty []T which is almost certainly a
// mistake, so we surface it.)
func TestUnitAssembleAllNoProviders(t *testing.T) {
	src := `package main
import "github.com/GiGurra/q/pkg/q"
type Plugin interface{ Name() string }
type Cfg struct{}
func newCfg() *Cfg { return nil }
func main() { _, _ = q.AssembleAll[Plugin](newCfg).Release() }
`
	diags, _, err := analyzeAssembleSrc(t, src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	requireDiagContains(t, diags,
		"target type Plugin has no providers",
		"q.AssembleAll[T] needs at least one recipe whose output is assignable to T",
	)
}

// TestUnitAssembleAllWithDeps — AssembleAll[T] still pulls in
// transitive deps for each provider. Two providers, both depending
// on a shared *Cfg recipe; the topo sort must produce the *Cfg step
// exactly once and each provider step references it.
func TestUnitAssembleAllWithDeps(t *testing.T) {
	src := `package main

import "github.com/GiGurra/q/pkg/q"

type Plugin interface{ Name() string }
type Cfg struct{}

type authP struct{ cfg *Cfg }
type logP struct{ cfg *Cfg }

func (authP) Name() string { return "auth" }
func (logP) Name() string  { return "log" }

func newCfg() *Cfg                { return &Cfg{} }
func newAuth(c *Cfg) Plugin       { return authP{cfg: c} }
func newLog(c *Cfg) Plugin        { return logP{cfg: c} }

func main() {
	_, _ = q.AssembleAll[Plugin](newCfg, newAuth, newLog).Release()
}
`
	diags, shapes, err := analyzeAssembleSrc(t, src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	requireNoDiags(t, diags)
	sc := shapes[0].Calls[0]
	if len(sc.AssembleSteps) != 3 {
		t.Fatalf("expected 3 steps (cfg + 2 plugins); got %d", len(sc.AssembleSteps))
	}
	if sc.AssembleSteps[0].RecipeIdx != 0 {
		t.Errorf("expected newCfg first in topo order; got recipe idx %d", sc.AssembleSteps[0].RecipeIdx)
	}
}

// TestUnitAssembleStructHappyPath exercises q.AssembleStruct[T] —
// each App field gets a recipe whose output matches the field type;
// shared transitive deps (here *Config) build only once.
func TestUnitAssembleStructHappyPath(t *testing.T) {
	src := `package main

import "github.com/GiGurra/q/pkg/q"

type Config struct{}
type DB     struct{ cfg *Config }
type Server struct{ db *DB; cfg *Config }
type Worker struct{ db *DB }
type App struct {
	Server *Server
	Worker *Worker
}

func newConfig() *Config                 { return &Config{} }
func newDB(c *Config) *DB                { return &DB{cfg: c} }
func newServer(d *DB, c *Config) *Server { return &Server{db: d, cfg: c} }
func newWorker(d *DB) *Worker            { return &Worker{db: d} }

func main() {
	_, _ = q.AssembleStruct[App](newConfig, newDB, newServer, newWorker).Release()
}
`
	diags, shapes, err := analyzeAssembleSrc(t, src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	requireNoDiags(t, diags)
	sc := shapes[0].Calls[0]
	if sc.Family != familyAssembleStruct {
		t.Fatalf("expected familyAssembleStruct; got %v", sc.Family)
	}
	if len(sc.AssembleStructFieldNames) != 2 || len(sc.AssembleStructFieldKeys) != 2 {
		t.Fatalf("expected 2 fields; got %d names / %d keys",
			len(sc.AssembleStructFieldNames), len(sc.AssembleStructFieldKeys))
	}
	// 4 recipes → 4 topo steps (newConfig + newDB shared, plus 2 leaf recipes).
	if len(sc.AssembleSteps) != 4 {
		t.Fatalf("expected 4 topo-sorted steps; got %d", len(sc.AssembleSteps))
	}
}

// TestUnitAssembleStructMissingField — when no recipe provides a
// required field's type, the resolver must surface a per-field
// missing-provider diagnostic naming the field.
func TestUnitAssembleStructMissingField(t *testing.T) {
	src := `package main
import "github.com/GiGurra/q/pkg/q"
type Server struct{}
type Worker struct{}
type App struct {
	Server *Server
	Worker *Worker
}
func newServer() *Server { return nil }
func main() {
	_, _ = q.AssembleStruct[App](newServer).Release()
}
`
	diags, _, err := analyzeAssembleSrc(t, src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	requireDiagContains(t, diags,
		"field Worker of App",
		"has no provider",
	)
}

// TestUnitAssembleStructNonStructTarget — q.AssembleStruct[T] is
// rejected when T is not a struct. Suggests q.Assemble[T] instead.
func TestUnitAssembleStructNonStructTarget(t *testing.T) {
	src := `package main
import "github.com/GiGurra/q/pkg/q"
type Server struct{}
func newServer() *Server { return nil }
func main() {
	_, _ = q.AssembleStruct[*Server](newServer).Release()
}
`
	diags, _, err := analyzeAssembleSrc(t, src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	requireDiagContains(t, diags,
		"is not a struct",
		"q.AssembleStruct[T] requires T to be a struct type",
		"use q.Assemble[T]",
	)
}

// TestUnitAssembleStructEmptyTarget — q.AssembleStruct[struct{}] is
// rejected (would always return zero struct).
func TestUnitAssembleStructEmptyTarget(t *testing.T) {
	src := `package main
import "github.com/GiGurra/q/pkg/q"
type Empty struct{}
func main() {
	_, _ = q.AssembleStruct[Empty]("ignored").Release()
}
`
	diags, _, err := analyzeAssembleSrc(t, src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	requireDiagContains(t, diags, "has no fields")
}

// TestUnitAssembleStructInterfaceField — a field with an interface
// type is satisfied by a concrete provider via assignability.
func TestUnitAssembleStructInterfaceField(t *testing.T) {
	src := `package main

import "github.com/GiGurra/q/pkg/q"

type Greeter interface{ Greet() string }
type EN struct{}
func (EN) Greet() string { return "hi" }

type Cfg struct{}
type Bundle struct {
	G   Greeter
	Cfg *Cfg
}

func newCfg() *Cfg { return &Cfg{} }
func newEN() *EN   { return &EN{} }

func main() {
	_, _ = q.AssembleStruct[Bundle](newCfg, newEN).Release()
}
`
	diags, shapes, err := analyzeAssembleSrc(t, src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	requireNoDiags(t, diags)
	sc := shapes[0].Calls[0]
	if len(sc.AssembleStructFieldNames) != 2 {
		t.Fatalf("expected 2 fields; got %d", len(sc.AssembleStructFieldNames))
	}
}

// TestUnitAssembleHappyPath exercises the simplest valid call to make
// sure the unit harness reaches resolveAssemble without errors.
func TestUnitAssembleHappyPath(t *testing.T) {
	src := `package main

import "github.com/GiGurra/q/pkg/q"

type Cfg struct{ DB string }
type DB struct{ cfg *Cfg }

func newCfg() *Cfg     { return &Cfg{DB: "x"} }
func newDB(c *Cfg) *DB { return &DB{cfg: c} }

func main() {
	_, _ = q.Assemble[*DB](newCfg, newDB).Release()
}
`
	diags, shapes, err := analyzeAssembleSrc(t, src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	requireNoDiags(t, diags)
	if len(shapes) != 1 || len(shapes[0].Calls) != 1 {
		t.Fatalf("expected one call shape; got %d shapes", len(shapes))
	}
	steps := shapes[0].Calls[0].AssembleSteps
	if len(steps) != 2 {
		t.Fatalf("expected 2 topo-sorted steps; got %d", len(steps))
	}
	if steps[0].RecipeIdx != 0 || steps[1].RecipeIdx != 1 {
		t.Errorf("unexpected topo order: %+v", steps)
	}
}
