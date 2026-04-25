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
	"sort"
	"strings"
)

// checkErrorSlots type-checks the package and validates that every
// recognised q.* call site has the built-in `error` interface at
// its error slot. The same type-check pass also infers cleanups for
// any q.Open(...).Release() calls the user wrote with no args
// (AutoRelease=true): each is mutated in place with an
// AutoCleanup kind, or surfaces a diagnostic if T's shape is
// unrecognised.
//
// Returns a list of diagnostics (one per offending call). A nil or
// empty result means every slot checks out and every AutoRelease
// has a resolved cleanup — the caller proceeds to the rewrite
// pass. A non-empty result aborts the build via planUserPackage's
// diag path.
//
// If the type check itself cannot run (no importcfg, importer
// construction fails, pkgPath empty), returns nil — we are
// strictly a lint; the real compile will fail on anything q's
// own rewrite pass can't handle. AutoRelease calls in this case
// reach the rewriter with cleanupUnknown and produce an explicit
// runtime panic via panicUnrewritten on the q.Open stub if the
// rewriter can't emit a defer.
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
	for i := range shapes {
		for j := range shapes[i].Calls {
			sc := &shapes[i].Calls[j]
			if d, ok := validateSlot(fset, *sc, info, errType); ok {
				diags = append(diags, d)
			}
			if sc.AutoRelease {
				if d, ok := inferAutoCleanup(fset, sc, info, errType); ok {
					diags = append(diags, d)
				}
			}
			if isEnumFamily(sc.Family) {
				if d, ok := resolveEnum(fset, sc, info, pkgPath); ok {
					diags = append(diags, d)
				}
			}
		}
	}
	return diags
}

// isEnumFamily reports whether sc's family is one of the q.Enum*
// helpers whose rewriter output depends on the constant set of T.
func isEnumFamily(f family) bool {
	switch f {
	case familyEnumValues, familyEnumNames, familyEnumName,
		familyEnumParse, familyEnumValid, familyEnumOrdinal:
		return true
	}
	return false
}

// resolveEnum populates sc.EnumConsts and sc.EnumTypeText by
// looking up the type-arg T (carried in sc.AsType) in the type
// info, then walking T's declaring package for *types.Const objects
// whose type is identical to T. Names are returned in source
// declaration order. The type text is the printed form of sc.AsType.
//
// Restrictions / diagnostic cases:
//
//   - T must resolve to a *types.Named whose declaring package is the
//     same as the user's compilation unit. Cross-package T (e.g.
//     `q.EnumName[other.Color](v)`) is rejected with a diagnostic
//     because the rewriter writes unqualified constant names.
//   - At least one *types.Const of T must exist in the package.
//     Otherwise the helper has nothing to rewrite to (and the user
//     almost certainly has a bug — calling EnumValues[T] without
//     declaring any constants of T).
//
// On either failure resolveEnum returns a diagnostic; the build is
// aborted by the caller.
func resolveEnum(fset *token.FileSet, sc *qSubCall, info *types.Info, pkgPath string) (Diagnostic, bool) {
	if sc.AsType == nil {
		return Diagnostic{}, false
	}
	tv, ok := info.Types[sc.AsType]
	if !ok || tv.Type == nil {
		// Type info missing — skip. The rewriter will reach this
		// site with EnumConsts empty and surface a clear error
		// from the panicUnrewritten body.
		return Diagnostic{}, false
	}
	named, ok := tv.Type.(*types.Named)
	if !ok {
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q: %s requires a named type as the type parameter (e.g. `q.EnumName[Color](v)` where Color is a defined type); got %s", enumFamilyLabel(sc.Family), tv.Type.String()),
		}, true
	}
	declPkg := named.Obj().Pkg()
	if declPkg == nil {
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q: %s on built-in type %s is not supported; declare a defined type instead", enumFamilyLabel(sc.Family), named.String()),
		}, true
	}
	if declPkg.Path() != pkgPath {
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q: %s on cross-package type %s is not supported (the rewriter currently writes unqualified constant names). Declare a thin wrapper in this package, e.g. `func MyName(v %s) string { return q.EnumName[%s](v) }`", enumFamilyLabel(sc.Family), named.String(), named.Obj().Name(), named.Obj().Name()),
		}, true
	}

	scope := declPkg.Scope()
	type entry struct {
		name string
		pos  token.Position
	}
	var entries []entry
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		c, ok := obj.(*types.Const)
		if !ok {
			continue
		}
		if !types.Identical(c.Type(), named) {
			continue
		}
		entries = append(entries, entry{name: c.Name(), pos: fset.Position(c.Pos())})
	}
	if len(entries) == 0 {
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q: %s found no constants of type %s in package %s. Declare at least one `const ... %s = ...` first", enumFamilyLabel(sc.Family), named.Obj().Name(), declPkg.Name(), named.Obj().Name()),
		}, true
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].pos.Filename != entries[j].pos.Filename {
			return entries[i].pos.Filename < entries[j].pos.Filename
		}
		return entries[i].pos.Offset < entries[j].pos.Offset
	})

	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.name
	}
	sc.EnumConsts = names
	sc.EnumTypeText = named.Obj().Name()
	return Diagnostic{}, false
}

// enumFamilyLabel is the user-facing name for an enum family in
// diagnostic messages.
func enumFamilyLabel(f family) string {
	switch f {
	case familyEnumValues:
		return "q.EnumValues"
	case familyEnumNames:
		return "q.EnumNames"
	case familyEnumName:
		return "q.EnumName"
	case familyEnumParse:
		return "q.EnumParse"
	case familyEnumValid:
		return "q.EnumValid"
	case familyEnumOrdinal:
		return "q.EnumOrdinal"
	}
	return "q.Enum*"
}

// inferAutoCleanup populates sc.AutoCleanup with the cleanup form
// inferred from sc's resource type (the first return of InnerExpr).
// Returns a diagnostic when the type doesn't expose a recognised
// cleanup shape (channel / Close()/error / Close()).
//
// Recognised shapes (in order):
//
//   - channel type T → cleanupChanClose, rewrites to `defer close(v)`.
//   - T (or *T) has `Close() error` → cleanupCloseErr, rewrites to
//     `defer func() { _ = v.Close() }()` (silently discards the
//     close-time error; pass an explicit cleanup if you need to
//     handle it).
//   - T (or *T) has `Close()` (no return) → cleanupCloseVoid,
//     rewrites to `defer v.Close()`.
//
// Anything else is rejected with a diagnostic naming T and
// suggesting the two ways to fix the build (explicit cleanup or
// .NoRelease()).
func inferAutoCleanup(fset *token.FileSet, sc *qSubCall, info *types.Info, errType types.Type) (Diagnostic, bool) {
	t := info.TypeOf(sc.InnerExpr)
	if t == nil {
		// types pass didn't resolve — leave AutoCleanup zero. The
		// rewriter will see cleanupUnknown and emit a placeholder
		// (or surface its own error). Don't block the build here.
		return Diagnostic{}, false
	}
	tup, ok := t.(*types.Tuple)
	if !ok || tup.Len() < 2 {
		return Diagnostic{}, false
	}
	resourceType := tup.At(0).Type()

	// 1) Channel type: T = chan U / chan<- U / <-chan U.
	if _, isChan := resourceType.Underlying().(*types.Chan); isChan {
		sc.AutoCleanup = cleanupChanClose
		return Diagnostic{}, false
	}

	// 2) Method-set lookup. The method set of *T includes methods
	//    declared on either T or *T (per Go spec). When T is itself a
	//    pointer type (`*Foo`), *T is `**Foo` whose method set is
	//    empty — use T directly in that case so Close() declared on
	//    *Foo is reachable.
	lookupType := resourceType
	if _, isPtr := resourceType.(*types.Pointer); !isPtr {
		lookupType = types.NewPointer(resourceType)
	}
	mset := types.NewMethodSet(lookupType)
	for i := 0; i < mset.Len(); i++ {
		sel := mset.At(i)
		if sel.Obj().Name() != "Close" {
			continue
		}
		fn, ok := sel.Obj().(*types.Func)
		if !ok {
			continue
		}
		sig, ok := fn.Type().(*types.Signature)
		if !ok {
			continue
		}
		if sig.Params().Len() != 0 {
			continue
		}
		switch sig.Results().Len() {
		case 0:
			sc.AutoCleanup = cleanupCloseVoid
			return Diagnostic{}, false
		case 1:
			if types.Identical(sig.Results().At(0).Type(), errType) {
				sc.AutoCleanup = cleanupCloseErr
				return Diagnostic{}, false
			}
		}
	}

	// 3) No match — diagnostic.
	pos := fset.Position(sc.OuterCall.Pos())
	msg := fmt.Sprintf(
		"q.Open/OpenE(...).Release() (auto) cannot infer a cleanup for type %s. "+
			"Auto-Release supports channel types (rewrites to `close(v)`), and types with a "+
			"`Close() error` or `Close()` method. Either pass an explicit cleanup function "+
			"(`Release(myCleanup)`), or opt out with `.NoRelease()` if no cleanup is wanted.",
		resourceType.String(),
	)
	return Diagnostic{
		File: pos.Filename,
		Line: pos.Line,
		Col:  pos.Column,
		Msg:  "q: " + msg,
	}, true
}

// validateSlot returns a diagnostic when sc's error slot type is
// anything other than the built-in `error` interface.
func validateSlot(fset *token.FileSet, sc qSubCall, info *types.Info, errType types.Type) (Diagnostic, bool) {
	switch sc.Family {
	case familyTry, familyTryE, familyOpen, familyOpenE, familyTrace, familyTraceE:
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
	case familyTrace:
		return "q.Trace"
	case familyTraceE:
		return "q.TraceE"
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
