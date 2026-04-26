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
// any q.Open(...).DeferCleanup() calls the user wrote with no args
// (InferCleanup=true): each is mutated in place with an
// AutoCleanup kind, or surfaces a diagnostic if T's shape is
// unrecognised.
//
// Returns a list of diagnostics (one per offending call). A nil or
// empty result means every slot checks out and every InferCleanup
// has a resolved cleanup — the caller proceeds to the rewrite
// pass. A non-empty result aborts the build via planUserPackage's
// diag path.
//
// If the type check itself cannot run (no importcfg, importer
// construction fails, pkgPath empty), returns nil — we are
// strictly a lint; the real compile will fail on anything q's
// own rewrite pass can't handle. InferCleanup calls in this case
// reach the rewriter with cleanupUnknown and produce an explicit
// runtime panic via panicUnrewritten on the q.Open stub if the
// rewriter can't emit a defer.
func checkErrorSlotsWithInfo(fset *token.FileSet, pkgPath, importcfgPath string, files []*ast.File, shapes []callShape) (*types.Info, []Diagnostic) {
	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Uses:  map[*ast.Ident]types.Object{},
		Defs:  map[*ast.Ident]types.Object{},
	}
	// Skip the typecheck pass entirely when the package has no
	// preprocessor-relevant code: no q.* shapes AND no file imports
	// pkg/q (which is what q.FnParams literal validation hangs off).
	if pkgPath == "" || importcfgPath == "" {
		return info, nil
	}
	if len(shapes) == 0 && !packageImportsQ(files) {
		return info, nil
	}

	imp, err := newImportcfgImporter(importcfgPath)
	if err != nil {
		return info, nil
	}

	cfg := &types.Config{
		Importer: imp,
		// Swallow type errors — the real compile reports those.
		Error: func(error) {},
	}
	// Best-effort: ignore the returned error. Info.Types is
	// populated for expressions that did type-check, which is all
	// we need.
	_, _ = cfg.Check(pkgPath, fset, files, info)

	errType := types.Universe.Lookup("error").Type()

	var diags []Diagnostic
	// Pass 1 — per-call resolution and guards that don't depend on
	// cross-call state. resolveEnum populates sc.EnumTypeText for the
	// q.Enum* and q.Gen* families; pass 2 reads that to enforce the
	// Lax-default rule.
	for i := range shapes {
		for j := range shapes[i].Calls {
			sc := &shapes[i].Calls[j]
			if d, ok := validateSlot(fset, *sc, info, errType); ok {
				diags = append(diags, d)
			}
			if sc.InferCleanup {
				if d, ok := inferDeferCleanup(fset, sc, info, errType); ok {
					diags = append(diags, d)
				}
			}
			if isEnumFamily(sc.Family) {
				if d, ok := resolveEnum(fset, sc, info, pkgPath); ok {
					diags = append(diags, d)
				}
			}
			if isReflectionFamily(sc.Family) {
				if d, ok := resolveReflection(fset, sc, info); ok {
					diags = append(diags, d)
				}
			}
			if sc.Family == familyAtCompileTime || sc.Family == familyAtCompileTimeCode {
				if d, ok := validateAtCompileTime(fset, sc, info, pkgPath); ok {
					diags = append(diags, d)
				}
			}
			if isAssembleFamily(sc.Family) {
				if d, ok := resolveAssemble(fset, sc, info, pkgPath); ok {
					diags = append(diags, d)
				}
			}
			if sc.Family == familyTern {
				if d, ok := resolveTern(fset, sc, info, pkgPath); ok {
					diags = append(diags, d)
				}
			}
			if sc.Family == familyAt {
				if d, ok := resolveAt(fset, sc, info, pkgPath); ok {
					diags = append(diags, d)
				}
			}
			if sc.Family == familyLazy || sc.Family == familyLazyE {
				if d, ok := resolveLazy(fset, sc, info, pkgPath); ok {
					diags = append(diags, d)
				}
			}
			if sc.Family == familyAtom || sc.Family == familyAtomOf {
				if d, ok := resolveAtom(fset, sc, info); ok {
					diags = append(diags, d)
				}
			}
		}
	}

	// Cross-call state: types opted into q.GenEnumJSONLax. The wire
	// format admits unknown values, so q.Exhaustive / q.Match on these
	// types must include a `default:` arm to handle the openness.
	laxTypes := collectLaxTypes(shapes)

	// OneOfN-derived sum types declared in this package — used by
	// q.AsOneOf, q.Match-on-sum, and q.Exhaustive type-switch coverage.
	oneOfTypes := resolveOneOfTypes(files, info, pkgPath)

	// Pass 1.4 — q.Sealed directives register their interface +
	// variant set into the SAME map (oneOfTypes) so q.Match /
	// q.Exhaustive on a Sealed-marked interface look up arms via the
	// same machinery as OneOfN. Runs before the AsOneOf pass so any
	// Sealed-tagged type is visible.
	for i := range shapes {
		for j := range shapes[i].Calls {
			sc := &shapes[i].Calls[j]
			if sc.Family == familySealed {
				if d, ok := resolveSealedDirective(fset, sc, info, pkgPath, oneOfTypes); ok {
					diags = append(diags, d)
				}
			}
		}
	}

	// Pass 1.5 — q.AsOneOf depends on the OneOf type map.
	for i := range shapes {
		for j := range shapes[i].Calls {
			sc := &shapes[i].Calls[j]
			if sc.Family == familyAsOneOf {
				if d, ok := resolveAsOneOf(fset, sc, info, pkgPath, oneOfTypes); ok {
					diags = append(diags, d)
				}
			}
		}
	}

	// Pass 2 — guards that depend on cross-call state.
	for i := range shapes {
		for j := range shapes[i].Calls {
			sc := &shapes[i].Calls[j]
			if sc.Family == familyExhaustive {
				if d, ok := validateExhaustive(fset, &shapes[i], sc, info, pkgPath, laxTypes, oneOfTypes, files); ok {
					diags = append(diags, d)
				}
			}
			if sc.Family == familyMatch {
				if d, ok := resolveMatch(fset, sc, info, pkgPath, laxTypes, oneOfTypes); ok {
					diags = append(diags, d)
				}
			}
		}
	}

	// Resource-escape detection. Independent of the type-resolution
	// passes above — purely syntactic, but it consults the scanner's
	// classified shapes to recognise q.Open(...).DeferCleanup(...) bindings.
	diags = append(diags, checkResourceEscapes(fset, files, shapes)...)

	// q.FnParams validation — required-by-default param structs.
	// Walks every CompositeLit in the package and checks marked
	// types. Independent of the q.* call shapes; piggy-backs on the
	// same go/types info.
	diags = append(diags, validateFnParams(fset, files, info)...)

	return info, diags
}

// collectLaxTypes returns the set of (unqualified, same-package) type
// names that have been opted into q.GenEnumJSONLax. Read by
// validateExhaustive / resolveMatch to enforce the
// "Lax-opted types require default:" rule. Names match the
// EnumTypeText form populated by resolveEnum (named.Obj().Name()).
func collectLaxTypes(shapes []callShape) map[string]bool {
	out := map[string]bool{}
	for _, sh := range shapes {
		for _, sc := range sh.Calls {
			if sc.Family == familyGenEnumJSONLax && sc.EnumTypeText != "" {
				out[sc.EnumTypeText] = true
			}
		}
	}
	return out
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
func validateExhaustive(fset *token.FileSet, sh *callShape, sc *qSubCall, info *types.Info, pkgPath string, laxTypes map[string]bool, oneOfTypes map[*types.TypeName]oneOfArms, files []*ast.File) (Diagnostic, bool) {
	if sc.InnerExpr == nil {
		return Diagnostic{}, false
	}
	// q.OneOfN-derived value flowing through a type switch:
	//
	//	switch v := q.Exhaustive(<x>.Value).(type) { case T1: …; case T2: … }
	//
	// Detect by walking the AST for a TypeSwitchStmt whose tag's
	// X is sc.OuterCall (the q.Exhaustive call). When found, the
	// OneOf-specific validator handles coverage and exits.
	if sw := findExhaustiveTypeSwitchParent(files, sc); sw != nil {
		if d, ok := validateExhaustiveOneOf(fset, sw, sc, info, pkgPath, oneOfTypes); ok {
			return d, true
		}
		// Even if validateExhaustiveOneOf returned no diagnostic, when
		// the inner expression IS a OneOfN's .Value access OR is itself
		// a Sealed-marker interface, we are done — the regular const-
		// coverage path below would be misleading.
		if sel, ok := sc.InnerExpr.(*ast.SelectorExpr); ok && sel.Sel != nil && sel.Sel.Name == "Value" {
			if xtv, ok := info.Types[sel.X]; ok && xtv.Type != nil {
				if _, isOne := armsForType(xtv.Type, oneOfTypes, pkgPath); isOne {
					return Diagnostic{}, false
				}
			}
		}
		if itv, ok := info.Types[sc.InnerExpr]; ok && itv.Type != nil {
			if _, isSealed := armsForType(itv.Type, oneOfTypes, pkgPath); isSealed {
				return Diagnostic{}, false
			}
		}
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
	hasDefault := false
	covered := map[string]bool{}
	for _, body := range swStmt.Body.List {
		cc, ok := body.(*ast.CaseClause)
		if !ok {
			continue
		}
		if cc.List == nil {
			// `default:` clause — catches unknown values, not a
			// substitute for declared-constant coverage.
			hasDefault = true
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
	if len(missing) > 0 {
		sort.Strings(missing)
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q: q.Exhaustive switch on %s is missing case(s) for: %s. Add the missing case(s), or use `default:` to opt out.", named.Obj().Name(), strings.Join(missing, ", ")),
		}, true
	}

	// Lax-opted types must have an explicit default: arm. The wire
	// format admits unknown values, so the switch needs to handle
	// runtime drift / forward-compat values — even when every
	// currently-declared constant is covered.
	if !hasDefault && laxTypes[named.Obj().Name()] {
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q: q.Exhaustive switch on %s requires a `default:` arm because the type is opted into q.GenEnumJSONLax (the wire format admits unknown values, so runtime drift / forward-compat values must be handled explicitly).", named.Obj().Name()),
		}, true
	}
	return Diagnostic{}, false
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
func resolveMatch(fset *token.FileSet, sc *qSubCall, info *types.Info, pkgPath string, laxTypes map[string]bool, oneOfTypes map[*types.TypeName]oneOfArms) (Diagnostic, bool) {
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
	// Resolve the matched value's type V (used to classify each cond).
	var matchedType types.Type
	if tv, ok := info.Types[sc.InnerExpr]; ok && tv.Type != nil {
		matchedType = tv.Type
		sc.EnumTypeText = types.TypeString(matchedType, qualifier)
	}

	// q.OneOfN-derived sum: dispatch via tag, not value-equality. The
	// dedicated path validates each q.Case arm's cond TYPE against the
	// variant list (the cond value itself is dropped) and each q.OnType
	// arm's handler param TYPE the same way; coverage is enforced
	// across the variant list.
	if matchedType != nil {
		if arms, ok := armsForType(matchedType, oneOfTypes, pkgPath); ok {
			return resolveMatchOneOf(fset, sc, info, pkgPath, arms, oneOfTypes)
		}
	}

	// q.OnType outside a OneOfN match makes no sense — surface it.
	for i := range sc.MatchCases {
		mc := &sc.MatchCases[i]
		if mc.IsOnType {
			pos := fset.Position(mc.HandlerExpr.Pos())
			return Diagnostic{
				File: pos.Filename,
				Line: pos.Line,
				Col:  pos.Column,
				Msg:  "q.OnType: only valid in a q.Match whose value is a q.OneOfN-derived sum type",
			}, true
		}
	}

	// Classify every q.Case arm's cond by its resolved type:
	//   matched-value-typed → value match
	//   bool                → predicate
	//   func() V            → lazy value match
	//   func() bool         → lazy predicate
	// Diagnostic for any other shape — the user wrote something
	// nonsensical for q.Case's first arg.
	for i := range sc.MatchCases {
		mc := &sc.MatchCases[i]
		if mc.IsDefault {
			continue
		}
		tv, ok := info.Types[mc.CondExpr]
		if !ok || tv.Type == nil || matchedType == nil {
			continue
		}
		if d, ok := classifyMatchCond(fset, sc, mc, tv.Type, matchedType, qualifier); ok {
			return d, true
		}
	}

	// Result type — q.Case / q.Default's result is R-typed; the first
	// non-default arm's result Type IS R. Used by the rewriter to
	// spell the IIFE's return type.
	for i := range sc.MatchCases {
		mc := &sc.MatchCases[i]
		if mc.IsDefault {
			continue
		}
		if tv, ok := info.Types[mc.ResultExpr]; ok && tv.Type != nil {
			sc.ResolvedString = types.TypeString(tv.Type, qualifier)
			break
		}
	}
	if sc.ResolvedString == "" {
		// All non-default arms unresolved — try the default arm.
		for i := range sc.MatchCases {
			mc := &sc.MatchCases[i]
			if !mc.IsDefault {
				continue
			}
			if tv, ok := info.Types[mc.ResultExpr]; ok && tv.Type != nil {
				sc.ResolvedString = types.TypeString(tv.Type, qualifier)
				break
			}
		}
	}

	// Predicate-arm rule: when any arm is a predicate (bool / func()
	// bool cond) the if-chain shape can't statically cover the value
	// space, so a q.Default arm is required.
	hasPredicate := false
	hasDefaultArm := false
	for _, mc := range sc.MatchCases {
		if mc.IsPredicate {
			hasPredicate = true
		}
		if mc.IsDefault {
			hasDefaultArm = true
		}
	}
	if hasPredicate && !hasDefaultArm {
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  "q: q.Match with a predicate q.Case (bool or func() bool cond) requires a q.Default(...) arm — predicate matches can't be statically covered for exhaustiveness.",
		}, true
	}

	// Coverage check: only when the value type is an enum (defined
	// named type with constants) AND no q.Default arm is present AND
	// no predicate arms are involved (predicates can't be statically
	// counted as covering specific constants).
	if hasPredicate {
		return Diagnostic{}, false
	}
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
		if name, ok := exprAsConstObjName(mc.CondExpr, info); ok && declared[name] {
			covered[name] = true
		}
	}
	if hasDefault {
		// Default present — covers any unknown values, no missing-case
		// diagnostic. Lax-default rule is also satisfied.
		return Diagnostic{}, false
	}
	var missing []string
	for name := range declared {
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q: q.Match on %s is missing case(s) for: %s. Add the missing case(s), or add a q.Default(...) arm.", named.Obj().Name(), strings.Join(missing, ", ")),
		}, true
	}

	// Lax-opted types must include a q.Default arm even when every
	// declared constant is covered. Mirrors the rule in
	// validateExhaustive — the wire format admits unknown values, so
	// the match needs an explicit catch-all for runtime drift /
	// forward-compat values.
	if laxTypes[named.Obj().Name()] {
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q: q.Match on %s requires a q.Default(...) arm because the type is opted into q.GenEnumJSONLax (the wire format admits unknown values, so runtime drift / forward-compat values must be handled explicitly).", named.Obj().Name()),
		}, true
	}
	return Diagnostic{}, false
}

// classifyMatchCond inspects a q.Case arm's cond type and populates
// mc.IsPredicate / mc.CondLazy. Four shapes are accepted:
//
//	cond is matched-value-typed   → value match (no flags)
//	cond is bool                  → predicate (IsPredicate)
//	cond is func() V              → lazy value match (CondLazy)
//	cond is func() bool           → lazy predicate (IsPredicate + CondLazy)
//
// Anything else returns a diagnostic — the user wrote something that
// doesn't fit any sensible q.Case dispatch shape.
//
// Untyped-bool / untyped-int constants are normalised via the
// matched-value comparison: an untyped 0 in a q.Case(0, ...) where
// V is int is reported as type "int" (not "untyped int") via the
// info.Types lookup, which already does the conversion.
func classifyMatchCond(fset *token.FileSet, sc *qSubCall, mc *matchCase, condType, matchedType types.Type, qualifier types.Qualifier) (Diagnostic, bool) {
	boolType := types.Typ[types.Bool]

	// Direct shapes first.
	if types.AssignableTo(condType, matchedType) {
		// Plain value match — default flags.
		return Diagnostic{}, false
	}
	if types.Identical(condType, boolType) {
		mc.IsPredicate = true
		return Diagnostic{}, false
	}

	// Function shapes: func() V or func() bool.
	if sig, ok := condType.(*types.Signature); ok {
		if sig.Params().Len() == 0 && sig.Results().Len() == 1 {
			retType := sig.Results().At(0).Type()
			if types.AssignableTo(retType, matchedType) {
				mc.CondLazy = true
				return Diagnostic{}, false
			}
			if types.Identical(retType, boolType) {
				mc.CondLazy = true
				mc.IsPredicate = true
				return Diagnostic{}, false
			}
		}
	}

	pos := fset.Position(mc.CondExpr.Pos())
	return Diagnostic{
		File: pos.Filename,
		Line: pos.Line,
		Col:  pos.Column,
		Msg: fmt.Sprintf(
			"q: q.Case cond has type %s, which is not the matched value's type (%s), bool, func() %s, or func() bool",
			types.TypeString(condType, qualifier),
			types.TypeString(matchedType, qualifier),
			types.TypeString(matchedType, qualifier),
		),
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

// validateAtCompileTime walks the q.AtCompileTime closure body for
// captures that aren't allowed (anything outside the closure scope
// that isn't a package-level decl or another q.AtCompileTime LHS
// binding) and for q.* calls (rejected in this version — Phase 3
// will lift this). Also captures the result-type text from the type
// info so the synthesis pass has a concrete spelling for the
// `func() R { ... }()` invocation.
func validateAtCompileTime(fset *token.FileSet, sc *qSubCall, info *types.Info, pkgPath string) (Diagnostic, bool) {
	if sc.AtCTClosure == nil {
		return Diagnostic{}, false
	}
	// 1. Resolve R's type text via the type-arg AsType.
	if sc.AsType != nil {
		if tv, ok := info.Types[sc.AsType]; ok && tv.Type != nil {
			sc.AtCTResultText = types.TypeString(tv.Type, func(p *types.Package) string {
				if p == nil || p.Path() == pkgPath {
					return ""
				}
				return p.Name()
			})
		}
	}
	// Fallback: read R from the closure's result list.
	if sc.AtCTResultText == "" && sc.AtCTClosure.Type != nil &&
		sc.AtCTClosure.Type.Results != nil &&
		sc.AtCTClosure.Type.Results.NumFields() == 1 {
		f := sc.AtCTClosure.Type.Results.List[0]
		if tv, ok := info.Types[f.Type]; ok && tv.Type != nil {
			sc.AtCTResultText = types.TypeString(tv.Type, func(p *types.Package) string {
				if p == nil || p.Path() == pkgPath {
					return ""
				}
				return p.Name()
			})
		}
	}

	// Phase 4: q.* calls inside the closure body (including
	// q.AtCompileTime itself) are allowed. The synthesis pass
	// invokes the subprocess with -toolexec=<qBin> so nested q.*
	// calls get rewritten before the subprocess compiles. Recursive
	// q.AtCompileTime is processed by a recursive q invocation that
	// synthesizes its own .q-comptime-<hash>/ directory. Cycle
	// detection within a single package is handled by the synthesis
	// pass's topo-sort; cross-package recursion has no cycle (each
	// package compile is independent).
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

// inferDeferCleanup populates sc.AutoCleanup with the cleanup form
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
// .NoDeferCleanup()).
func inferDeferCleanup(fset *token.FileSet, sc *qSubCall, info *types.Info, errType types.Type) (Diagnostic, bool) {
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

	if kind := inferCleanupKind(resourceType, errType); kind != cleanupUnknown {
		sc.AutoCleanup = kind
		return Diagnostic{}, false
	}

	// 3) No match — diagnostic.
	pos := fset.Position(sc.OuterCall.Pos())
	msg := fmt.Sprintf(
		"q.Open/OpenE(...).DeferCleanup() (auto) cannot infer a cleanup for type %s. "+
			"Auto-DeferCleanup supports channel types (rewrites to `close(v)`), and types with a "+
			"`Close() error` or `Close()` method. Either pass an explicit cleanup function "+
			"(`Release(myCleanup)`), or opt out with `.NoDeferCleanup()` if no cleanup is wanted.",
		resourceType.String(),
	)
	return Diagnostic{
		File: pos.Filename,
		Line: pos.Line,
		Col:  pos.Column,
		Msg:  "q: " + msg,
	}, true
}

// inferCleanupKind reports the auto-cleanup form for type t, or
// cleanupUnknown when no shape matches:
//   - Bidirectional / send-only channels → cleanupChanClose (close(v)).
//     Receive-only channels (<-chan U) are NEVER auto-closed —
//     closing a channel is the sender's responsibility, and Go
//     itself forbids `close()` on a recv-only channel. A recipe
//     producing <-chan U is signalling that the channel is
//     consumed, not owned, by the assembly.
//   - T has Close()       → cleanupCloseVoid (v.Close()).
//   - T has Close() error → cleanupCloseErr  (_ = v.Close()).
//
// Reusable by both q.Open's auto-DeferCleanup and q.Assemble's auto-
// detect on resource recipes whose T carries a Close shape but
// whose recipe signature didn't declare an explicit cleanup.
func inferCleanupKind(t, errType types.Type) cleanupKind {
	if t == nil {
		return cleanupUnknown
	}
	if ch, isChan := t.Underlying().(*types.Chan); isChan {
		if ch.Dir() == types.RecvOnly {
			return cleanupUnknown
		}
		return cleanupChanClose
	}
	// Method-set lookup. The method set of *T includes methods on
	// either T or *T (per Go spec). When T is itself a pointer, *T
	// is **Foo with an empty method set — fall back to T directly.
	lookupType := t
	if _, isPtr := t.(*types.Pointer); !isPtr {
		lookupType = types.NewPointer(t)
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
			return cleanupCloseVoid
		case 1:
			if types.Identical(sig.Results().At(0).Type(), errType) {
				return cleanupCloseErr
			}
		}
	}
	return cleanupUnknown
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
