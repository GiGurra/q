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
	"strconv"
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
		Uses:  map[*ast.Ident]types.Object{},
		Defs:  map[*ast.Ident]types.Object{},
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
			if sc.Family == familyExhaustive {
				if d, ok := validateExhaustive(fset, &shapes[i], sc, info, pkgPath); ok {
					diags = append(diags, d)
				}
			}
			if isReflectionFamily(sc.Family) {
				if d, ok := resolveReflection(fset, sc, info); ok {
					diags = append(diags, d)
				}
			}
			if sc.Family == familyMatch {
				if d, ok := resolveMatch(fset, sc, info, pkgPath); ok {
					diags = append(diags, d)
				}
			}
		}
	}
	return diags
}

// validateExhaustive enforces that every const of T (the inferred
// type of the expression wrapped by q.Exhaustive) appears in at
// least one case clause of the enclosing switch statement. A
// `default:` clause opts out — when present, the catch-all covers
// any missing constants.
//
// Returns a diagnostic when the type can't resolve to a defined
// named type with constants, or when one or more constants are not
// referenced by any case clause.
func validateExhaustive(fset *token.FileSet, sh *callShape, sc *qSubCall, info *types.Info, pkgPath string) (Diagnostic, bool) {
	if sc.InnerExpr == nil {
		return Diagnostic{}, false
	}
	tv, ok := info.Types[sc.InnerExpr]
	if !ok || tv.Type == nil {
		return Diagnostic{}, false
	}
	named, ok := tv.Type.(*types.Named)
	if !ok {
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q: q.Exhaustive requires a named type (e.g. `type Color int` with `const Red Color = …`); got %s", tv.Type.String()),
		}, true
	}
	declPkg := named.Obj().Pkg()
	if declPkg == nil {
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q: q.Exhaustive on built-in type %s is not supported; use a defined type", named.String()),
		}, true
	}
	if declPkg.Path() != pkgPath {
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q: q.Exhaustive on cross-package type %s is not supported (the rewriter currently checks against the case clauses' source-text, which doesn't qualify foreign constants). Wrap the switch in a thin function declared in %s.", named.String(), declPkg.Name()),
		}, true
	}

	// Collect all declared constants of the type.
	scope := declPkg.Scope()
	declared := map[string]bool{}
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		c, ok := obj.(*types.Const)
		if !ok {
			continue
		}
		if !types.Identical(c.Type(), named) {
			continue
		}
		declared[c.Name()] = true
	}
	if len(declared) == 0 {
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q: q.Exhaustive: type %s has no constants declared in package %s", named.Obj().Name(), declPkg.Name()),
		}, true
	}

	// Walk the SwitchStmt's body for case clauses. Every declared
	// constant must appear in some case — `default:` catches values
	// outside the declared set (forward-compat / Lax-opted types /
	// runtime drift) but does NOT replace coverage of the declared
	// constants. This rule keeps q.Exhaustive's promise honest for
	// Lax-JSON types: known values still must each have a dedicated
	// case, default catches the genuinely-unknown.
	swStmt, ok := sh.Stmt.(*ast.SwitchStmt)
	if !ok || swStmt.Body == nil {
		return Diagnostic{}, false
	}
	covered := map[string]bool{}
	for _, body := range swStmt.Body.List {
		cc, ok := body.(*ast.CaseClause)
		if !ok {
			continue
		}
		if cc.List == nil {
			// `default:` clause — catches unknown values, not a
			// substitute for declared-constant coverage.
			continue
		}
		for _, expr := range cc.List {
			caseTV, ok := info.Types[expr]
			if !ok {
				continue
			}
			// Match a named constant by its declaring object — far
			// more robust than source-text identity (handles
			// alias-imports, qualified names, parenthesised
			// expressions, etc.).
			if ident, ok := exprAsConstObjName(expr, info); ok && declared[ident] {
				covered[ident] = true
				continue
			}
			// Fallback: const value identity. If the case expr
			// types as a constant of the same type, find a
			// matching declared const by value.
			if caseTV.Value != nil {
				for name := range declared {
					if obj := scope.Lookup(name); obj != nil {
						if c, isConst := obj.(*types.Const); isConst {
							if types.Identical(c.Type(), caseTV.Type) &&
								c.Val().String() == caseTV.Value.String() {
								covered[name] = true
							}
						}
					}
				}
			}
		}
	}

	var missing []string
	for name := range declared {
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return Diagnostic{}, false
	}
	sort.Strings(missing)
	pos := fset.Position(sc.OuterCall.Pos())
	return Diagnostic{
		File: pos.Filename,
		Line: pos.Line,
		Col:  pos.Column,
		Msg:  fmt.Sprintf("q: q.Exhaustive switch on %s is missing case(s) for: %s. Add the missing case(s), or use `default:` to opt out.", named.Obj().Name(), strings.Join(missing, ", ")),
	}, true
}

// exprAsConstObjName returns the *types.Const's name if expr resolves
// to one (handles bare identifiers and selector expressions like
// `pkg.Name`). Falls back to "", false for anything else.
func exprAsConstObjName(expr ast.Expr, info *types.Info) (string, bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		if obj := info.Uses[e]; obj != nil {
			if _, ok := obj.(*types.Const); ok {
				return obj.Name(), true
			}
		}
		if obj := info.Defs[e]; obj != nil {
			if _, ok := obj.(*types.Const); ok {
				return obj.Name(), true
			}
		}
	case *ast.SelectorExpr:
		if obj := info.Uses[e.Sel]; obj != nil {
			if _, ok := obj.(*types.Const); ok {
				return obj.Name(), true
			}
		}
	case *ast.ParenExpr:
		return exprAsConstObjName(e.X, info)
	}
	return "", false
}

// isEnumFamily reports whether sc's family is one of the q.Enum*
// helpers whose rewriter output depends on the constant set of T.
// Also covers the Gen* directives, which use the same constant
// resolution to drive the companion-file synthesis.
func isEnumFamily(f family) bool {
	switch f {
	case familyEnumValues, familyEnumNames, familyEnumName,
		familyEnumParse, familyEnumValid, familyEnumOrdinal,
		familyGenStringer, familyGenEnumJSONStrict, familyGenEnumJSONLax:
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
		name  string
		value string
		pos   token.Position
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
		entries = append(entries, entry{
			name:  c.Name(),
			value: c.Val().ExactString(),
			pos:   fset.Position(c.Pos()),
		})
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
	values := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.name
		values[i] = e.value
	}
	sc.EnumConsts = names
	sc.EnumConstValues = values
	sc.EnumTypeText = named.Obj().Name()
	// Capture the underlying basic-type kind for the Gen* directives
	// that need it to choose the right marshaller shape. Empty when
	// the underlying isn't a basic type (struct, interface, etc.) —
	// the Gen synthesis rejects those.
	if basic, ok := named.Underlying().(*types.Basic); ok {
		sc.EnumUnderlyingKind = basic.Name()
	}
	return Diagnostic{}, false
}

// resolveMatch captures the value/result type texts (for the IIFE
// the rewriter emits) and, when the value type is an enum,
// validates that every constant has a case (mirroring
// validateExhaustive). Returns a diagnostic on missing-case
// violations; type-text resolution itself never produces a
// diagnostic — when info isn't enough to resolve, the rewriter
// falls back to `any`.
func resolveMatch(fset *token.FileSet, sc *qSubCall, info *types.Info, pkgPath string) (Diagnostic, bool) {
	if sc.InnerExpr == nil {
		return Diagnostic{}, false
	}
	qualifier := func(p *types.Package) string {
		if p == nil || p.Path() == pkgPath {
			// Same-package types: emit unqualified. The
			// rewriter's output sits in the same package as the
			// user code, so `Coords` resolves correctly without
			// the `main.` (or pkg.) prefix.
			return ""
		}
		return p.Name()
	}
	if tv, ok := info.Types[sc.InnerExpr]; ok && tv.Type != nil {
		sc.EnumTypeText = types.TypeString(tv.Type, qualifier)
	}
	// Result type from the first arm whose result expression
	// resolves cleanly. For q.Case / q.Default the result expr's
	// type IS R. For q.CaseFn / q.DefaultFn the result expr is a
	// `func() R` — unwrap the func's single return.
	var resultArm *matchCase
	for i := range sc.MatchCases {
		mc := &sc.MatchCases[i]
		if !mc.IsDefault {
			resultArm = mc
			break
		}
	}
	if resultArm == nil {
		for i := range sc.MatchCases {
			mc := &sc.MatchCases[i]
			if mc.IsDefault {
				resultArm = mc
				break
			}
		}
	}
	if resultArm != nil {
		if tv, ok := info.Types[resultArm.ResultExpr]; ok && tv.Type != nil {
			resultType := tv.Type
			if resultArm.IsLazy {
				if sig, isSig := tv.Type.(*types.Signature); isSig && sig.Results().Len() == 1 {
					resultType = sig.Results().At(0).Type()
				}
			}
			sc.ResolvedString = types.TypeString(resultType, qualifier)
		}
	}

	// Coverage check: only when the value type is an enum (defined
	// named type with constants) AND no q.Default arm is present.
	tv, ok := info.Types[sc.InnerExpr]
	if !ok || tv.Type == nil {
		return Diagnostic{}, false
	}
	named, ok := tv.Type.(*types.Named)
	if !ok {
		return Diagnostic{}, false
	}
	declPkg := named.Obj().Pkg()
	if declPkg == nil || declPkg.Path() != pkgPath {
		return Diagnostic{}, false
	}
	scope := declPkg.Scope()
	declared := map[string]bool{}
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		c, isConst := obj.(*types.Const)
		if !isConst {
			continue
		}
		if !types.Identical(c.Type(), named) {
			continue
		}
		declared[name] = true
	}
	if len(declared) == 0 {
		return Diagnostic{}, false
	}
	hasDefault := false
	covered := map[string]bool{}
	for _, mc := range sc.MatchCases {
		if mc.IsDefault {
			hasDefault = true
			continue
		}
		if name, ok := exprAsConstObjName(mc.ValueExpr, info); ok && declared[name] {
			covered[name] = true
		}
	}
	if hasDefault {
		return Diagnostic{}, false
	}
	var missing []string
	for name := range declared {
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return Diagnostic{}, false
	}
	sort.Strings(missing)
	pos := fset.Position(sc.OuterCall.Pos())
	return Diagnostic{
		File: pos.Filename,
		Line: pos.Line,
		Col:  pos.Column,
		Msg:  fmt.Sprintf("q: q.Match on %s is missing case(s) for: %s. Add the missing case(s), or add a q.Default(...) arm.", named.Obj().Name(), strings.Join(missing, ", ")),
	}, true
}

// isReflectionFamily reports whether sc's family is one of the
// q.Fields / q.AllFields / q.TypeName / q.Tag compile-time-reflection
// helpers.
func isReflectionFamily(f family) bool {
	switch f {
	case familyFields, familyAllFields, familyTypeName, familyTag:
		return true
	}
	return false
}

// resolveReflection populates sc.StructFields and/or sc.ResolvedString
// for the reflection family by inspecting T (sc.AsType) via the
// types.Info. Returns a diagnostic on error (T isn't a struct for
// the field-listing forms, the named field doesn't exist for q.Tag,
// etc.).
func resolveReflection(fset *token.FileSet, sc *qSubCall, info *types.Info) (Diagnostic, bool) {
	if sc.AsType == nil {
		return Diagnostic{}, false
	}
	tv, ok := info.Types[sc.AsType]
	if !ok || tv.Type == nil {
		// Type info missing — skip; the panicUnrewritten body
		// will surface a runtime error if reached.
		return Diagnostic{}, false
	}

	if sc.Family == familyTypeName {
		sc.ResolvedString = formatTypeName(tv.Type)
		return Diagnostic{}, false
	}

	// Fields / AllFields / Tag all need T's underlying struct.
	st := dereferenceToStruct(tv.Type)
	if st == nil {
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q: %s requires a struct type (or pointer to struct) as the type parameter; got %s", reflectionFamilyLabel(sc.Family), tv.Type.String()),
		}, true
	}

	switch sc.Family {
	case familyFields, familyAllFields:
		var names []string
		for i := 0; i < st.NumFields(); i++ {
			f := st.Field(i)
			if sc.Family == familyFields && !f.Exported() {
				continue
			}
			names = append(names, f.Name())
		}
		sc.StructFields = names

	case familyTag:
		// OkArgs[0] is the field name, OkArgs[1] is the tag key —
		// both validated as string literals at scan time.
		if len(sc.OkArgs) != 2 {
			return Diagnostic{}, false
		}
		fieldLit, ok1 := sc.OkArgs[0].(*ast.BasicLit)
		keyLit, ok2 := sc.OkArgs[1].(*ast.BasicLit)
		if !ok1 || !ok2 {
			return Diagnostic{}, false
		}
		fieldName, err1 := strconv.Unquote(fieldLit.Value)
		key, err2 := strconv.Unquote(keyLit.Value)
		if err1 != nil || err2 != nil {
			return Diagnostic{}, false
		}
		// Find the field on the struct.
		var tag string
		found := false
		for i := 0; i < st.NumFields(); i++ {
			f := st.Field(i)
			if f.Name() == fieldName {
				tag = st.Tag(i)
				found = true
				break
			}
		}
		if !found {
			pos := fset.Position(sc.OuterCall.Pos())
			return Diagnostic{
				File: pos.Filename,
				Line: pos.Line,
				Col:  pos.Column,
				Msg:  fmt.Sprintf("q: q.Tag[%s]: field %q not found on the struct", tv.Type.String(), fieldName),
			}, true
		}
		sc.ResolvedString = reflectStructTag(tag).Get(key)
	}
	return Diagnostic{}, false
}

// dereferenceToStruct unwraps a pointer to its element type and
// returns the underlying *types.Struct, or nil when T isn't a
// struct (or pointer to one).
func dereferenceToStruct(t types.Type) *types.Struct {
	if ptr, isPtr := t.(*types.Pointer); isPtr {
		t = ptr.Elem()
	}
	if named, isNamed := t.(*types.Named); isNamed {
		t = named.Underlying()
	}
	st, _ := t.(*types.Struct)
	return st
}

// formatTypeName returns the user-facing type-name for q.TypeName.
// For named types, just the unqualified identifier. For pointer
// types, the dereferenced name. For unnamed types (slice, map,
// chan, func, struct literal), `types.Type.String()` (using the
// short package qualifier).
func formatTypeName(t types.Type) string {
	if ptr, isPtr := t.(*types.Pointer); isPtr {
		return formatTypeName(ptr.Elem())
	}
	if named, isNamed := t.(*types.Named); isNamed {
		return named.Obj().Name()
	}
	return types.TypeString(t, func(p *types.Package) string {
		if p == nil {
			return ""
		}
		return p.Name()
	})
}

// reflectStructTag mirrors the standard library's
// `reflect.StructTag` parser without forcing the preprocessor to
// import `reflect` (which it would otherwise have no need for).
type reflectStructTag string

// Get returns the value associated with key in the tag string. If
// the key is absent, Get returns the empty string. Identical
// semantics to `reflect.StructTag.Get`, simplified for our use:
// we don't need the (value, ok) form.
func (tag reflectStructTag) Get(key string) string {
	for tag != "" {
		i := 0
		for i < len(tag) && tag[i] == ' ' {
			i++
		}
		tag = tag[i:]
		if tag == "" {
			break
		}
		i = 0
		for i < len(tag) && tag[i] > ' ' && tag[i] != ':' && tag[i] != '"' && tag[i] != 0x7f {
			i++
		}
		if i == 0 || i+1 >= len(tag) || tag[i] != ':' || tag[i+1] != '"' {
			break
		}
		name := string(tag[:i])
		tag = tag[i+1:]
		i = 1
		for i < len(tag) && tag[i] != '"' {
			if tag[i] == '\\' {
				i++
			}
			i++
		}
		if i >= len(tag) {
			break
		}
		qvalue := string(tag[:i+1])
		tag = tag[i+1:]
		if key == name {
			value, err := strconv.Unquote(qvalue)
			if err != nil {
				return ""
			}
			return value
		}
	}
	return ""
}

// reflectionFamilyLabel returns the user-facing helper name.
func reflectionFamilyLabel(f family) string {
	switch f {
	case familyFields:
		return "q.Fields"
	case familyAllFields:
		return "q.AllFields"
	case familyTypeName:
		return "q.TypeName"
	case familyTag:
		return "q.Tag"
	}
	return "q.<reflection>"
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
	case familyGenStringer:
		return "q.GenStringer"
	case familyGenEnumJSONStrict:
		return "q.GenEnumJSONStrict"
	case familyGenEnumJSONLax:
		return "q.GenEnumJSONLax"
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
