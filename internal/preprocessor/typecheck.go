package preprocessor

// typecheck.go — the error-slot type guard.
//
// Motivation. Go's `f(g())` forwarding rule plus implicit
// concrete-to-interface assignability means a callee declared as
//
//	func Foo() (T, *MyErr)
//
// can be passed directly to `q.Try(Foo())` — `*MyErr` is
// assignable to `error`, so the call type-checks. But when Foo
// returns `(v, nil)` with the nil being a typed `*MyErr(nil)`,
// the implicit conversion to `error` yields a *non-nil* interface
// value holding a nil concrete. The rewritten `if err != nil`
// fires and q bubbles a bogus "error" that wraps nothing.
//
// This is Go's classic typed-nil-interface pitfall, and q is
// uniquely positioned to catch it: the moment a user writes
// `q.Try(Foo())`, we can inspect Foo's signature and require the
// error slot to be the exact built-in `error` interface. Any
// concrete type — pointer, struct, basic — at that slot is a
// build error with a diagnostic explaining the pitfall and the
// two acceptable fixes.
//
// Scope:
//
//   - Try / TryE / Open / OpenE: the inner call's last return
//     value is the error slot.
//   - Check / CheckE: the single argument is the error slot.
//
// Implementation. The type check runs once per user-package
// compile. It uses go/types with an importer backed by the
// compile's -importcfg (every direct and transitive dependency
// the real compile will see). When the type check can't run —
// missing importcfg, importer failure, or type errors unrelated
// to our check — we silently skip; the real compile will surface
// those far more helpfully than we can.

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"strings"
)

// checkErrorSlots type-checks the package and validates that every
// recognised q.* call site has the built-in `error` interface at
// its error slot.
//
// Returns a list of diagnostics (one per offending call). A nil or
// empty result means every slot checks out — the caller proceeds
// to the rewrite pass. A non-empty result aborts the build via
// planUserPackage's diag path.
//
// If the type check itself cannot run (no importcfg, importer
// construction fails, pkgPath empty), returns nil — we are
// strictly a lint; the real compile will fail on anything q's
// own rewrite pass can't handle.
func checkErrorSlots(fset *token.FileSet, pkgPath, importcfgPath string, files []*ast.File, shapes []callShape) []Diagnostic {
	if len(shapes) == 0 || pkgPath == "" || importcfgPath == "" {
		return nil
	}

	imp, err := newImportcfgImporter(importcfgPath)
	if err != nil {
		return nil
	}

	cfg := &types.Config{
		Importer: imp,
		// Swallow type errors — the real compile reports those.
		Error: func(error) {},
	}
	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
	}
	// Best-effort: ignore the returned error. Info.Types is
	// populated for expressions that did type-check, which is all
	// we need.
	_, _ = cfg.Check(pkgPath, fset, files, info)

	errType := types.Universe.Lookup("error").Type()

	var diags []Diagnostic
	for _, sh := range shapes {
		for _, sc := range sh.Calls {
			if d, ok := validateSlot(fset, sc, info, errType); ok {
				diags = append(diags, d)
			}
		}
	}
	return diags
}

// validateSlot returns a diagnostic when sc's error slot type is
// anything other than the built-in `error` interface.
func validateSlot(fset *token.FileSet, sc qSubCall, info *types.Info, errType types.Type) (Diagnostic, bool) {
	switch sc.Family {
	case familyTry, familyTryE, familyOpen, familyOpenE:
		// InnerExpr is a (T, error)-returning CallExpr. Inspect
		// the tuple's last element.
		t := info.TypeOf(sc.InnerExpr)
		if t == nil {
			return Diagnostic{}, false
		}
		tup, ok := t.(*types.Tuple)
		if !ok || tup.Len() < 2 {
			return Diagnostic{}, false
		}
		slot := tup.At(tup.Len() - 1).Type()
		if types.Identical(slot, errType) {
			return Diagnostic{}, false
		}
		return buildSlotDiag(fset, sc, slot, familyLabel(sc.Family), "last return value of the wrapped call"), true

	case familyCheck, familyCheckE:
		t := info.TypeOf(sc.InnerExpr)
		if t == nil {
			return Diagnostic{}, false
		}
		if types.Identical(t, errType) {
			return Diagnostic{}, false
		}
		return buildSlotDiag(fset, sc, t, familyLabel(sc.Family), "argument to "+familyLabel(sc.Family)), true
	}
	return Diagnostic{}, false
}

// buildSlotDiag formats the typed-nil-guard diagnostic. The
// message names the offending type, explains the pitfall, and
// suggests the two acceptable fixes.
func buildSlotDiag(fset *token.FileSet, sc qSubCall, got types.Type, entryName, slotRole string) Diagnostic {
	pos := fset.Position(sc.OuterCall.Pos())
	msg := fmt.Sprintf(
		"%s requires the built-in `error` interface at the %s, but got %s. "+
			"Implicitly converting a concrete type to `error` triggers Go's typed-nil-interface pitfall: "+
			"a nil %s becomes a non-nil `error` value, so the bubble check inside %s would fire for a "+
			"notionally-nil error. Fix by changing the callee to return `error`, or by converting "+
			"explicitly at the call site (and accepting that a typed nil will appear non-nil).",
		entryName, slotRole, got.String(), got.String(), entryName,
	)
	return Diagnostic{
		File: pos.Filename,
		Line: pos.Line,
		Col:  pos.Column,
		Msg:  "q: " + msg,
	}
}

// familyLabel is the user-facing spelling for each q.* family in
// diagnostics. Uses the bare + chain name split so "q.Try" and
// "q.TryE" read naturally in the message.
func familyLabel(f family) string {
	switch f {
	case familyTry:
		return "q.Try"
	case familyTryE:
		return "q.TryE"
	case familyCheck:
		return "q.Check"
	case familyCheckE:
		return "q.CheckE"
	case familyOpen:
		return "q.Open"
	case familyOpenE:
		return "q.OpenE"
	}
	return "q.*"
}

// newImportcfgImporter builds a go/types Importer that resolves
// imports through the compile's -importcfg file. Each
// `packagefile <path>=<archive>` line becomes one entry in an
// in-memory map; importer.ForCompiler reads the archive with gc's
// export-data format.
func newImportcfgImporter(importcfgPath string) (types.Importer, error) {
	data, err := os.ReadFile(importcfgPath)
	if err != nil {
		return nil, fmt.Errorf("read importcfg %s: %w", importcfgPath, err)
	}
	paths := parseImportcfgEntries(data)
	fset := token.NewFileSet()
	lookup := func(pkgPath string) (io.ReadCloser, error) {
		p, ok := paths[pkgPath]
		if !ok {
			return nil, fmt.Errorf("package %q not in importcfg", pkgPath)
		}
		return os.Open(p)
	}
	return importer.ForCompiler(fset, "gc", lookup), nil
}

// parseImportcfgEntries reads an importcfg file and returns the
// map from import path to compiled-archive path. Lines other than
// `packagefile path=archive` (blank, comments, `importmap`) are
// ignored — types only needs packagefile entries for export-data
// loading.
func parseImportcfgEntries(data []byte) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		const prefix = "packagefile "
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimPrefix(line, prefix)
		eq := strings.Index(rest, "=")
		if eq < 0 {
			continue
		}
		m[rest[:eq]] = rest[eq+1:]
	}
	return m
}
