package preprocessor

// oneof.go — typecheck + rewrite for the q.OneOfN AND q.Sealed
// sum-type families. Both sums share dispatch machinery; they differ
// only in the runtime carrier:
//
//   - q.OneOfN: struct{Tag uint8; Value any}. Construction via
//     q.AsOneOf[T](v); dispatch via switch on .Tag.
//   - q.Sealed: marker interface with auto-synthesised marker
//     methods on each variant. Construction is direct (`Variant{...}`
//     already implements the marker); dispatch via Go type switch.
//
// Five call shapes are wired into the preprocessor by this file:
//
//   1. q.AsOneOf[T](v) — wrap v as a OneOfN-derived sum type T.
//      Validates T is OneOfN-derived (struct flavour), finds v's
//      type's position in T's arm list, emits T{Tag: <pos>, Value: v}.
//
//   2. q.Match(s, …) where s is a OneOfN-derived sum (struct or
//      Sealed interface). q.Case arms select by cond type; q.OnType
//      arms bind the unwrapped payload; q.Default catches the rest.
//      Rewriter emits an IIFE-wrapped switch — on .Tag for the struct
//      flavour, on .(type) for the Sealed flavour.
//
//   3. switch v := q.Exhaustive(s.Value).(type) { … } where s is a
//      q.OneOfN struct. Coverage check enforces every variant has a
//      case (or default: opt-out).
//
//   4. switch v := q.Exhaustive(m).(type) { … } where m is a Sealed
//      marker-interface value. Coverage check uses the registered
//      closed set.
//
//   5. var _ = q.Sealed[I](Variant{}, …) — package-level directive
//      that registers the closed set for I and triggers companion-
//      file synthesis of the per-variant marker methods.
//
// Discovery flow. resolveOneOfTypes walks TypeSpecs to find OneOfN-
// derived alias types. resolveSealedDirective walks q.Sealed
// directives to find Sealed-marker interfaces and their variants.
// Both populate the same `oneOfArms`-keyed map so armsForType serves
// both dispatch shapes uniformly.

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"
)

// oneOfArms is the cached variant list for a OneOfN-derived type.
// Both the *types.Type (for assignability comparisons) and the
// textual spelling (for splicing into the rewritten type assertion)
// are kept in declaration order, so index i corresponds to runtime
// Tag i+1.
type oneOfArms struct {
	Types []types.Type
	Texts []string
}

// resolveOneOfTypes walks the package's type declarations and builds
// a map of every defined named type whose underlying type derives
// from q.OneOfN. Bare q.OneOfN[…] uses (no alias) are not in this
// map; resolveAsOneOf / resolveMatch handle those at the call site
// via TypeArgs() directly.
func resolveOneOfTypes(files []*ast.File, info *types.Info, pkgPath string) map[*types.TypeName]oneOfArms {
	out := map[*types.TypeName]oneOfArms{}
	qualifier := func(p *types.Package) string {
		if p == nil || p.Path() == pkgPath {
			return ""
		}
		return p.Name()
	}
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				// Resolve the RHS expression's type — for `type Status q.OneOf2[A, B]`
				// this is a *types.Named whose origin is the generic OneOf2.
				tv, ok := info.Types[ts.Type]
				if !ok || tv.Type == nil {
					continue
				}
				rhsNamed, ok := tv.Type.(*types.Named)
				if !ok {
					continue
				}
				if !isOneOfGeneric(rhsNamed) {
					continue
				}
				args := rhsNamed.TypeArgs()
				if args == nil {
					continue
				}
				arms := oneOfArms{}
				for i := 0; i < args.Len(); i++ {
					t := args.At(i)
					arms.Types = append(arms.Types, t)
					arms.Texts = append(arms.Texts, types.TypeString(t, qualifier))
				}
				// The defined type for Status is in info.Defs at ts.Name.
				obj, _ := info.Defs[ts.Name].(*types.TypeName)
				if obj == nil {
					continue
				}
				out[obj] = arms
			}
		}
	}
	return out
}

// qEitherImportPath is the import path of the either subpackage,
// whose Either[L, R] type is structurally a 2-arm OneOf and reuses
// every typecheck / rewrite path the OneOfN family does.
const qEitherImportPath = qPkgImportPath + "/either"

// isOneOfGeneric reports whether n is an instantiation of one of the
// recognised sum-type generic types: q.OneOfN (in pkg/q) or
// either.Either (in pkg/q/either). Both produce a `Tag uint8 + Value
// any` runtime shape and share the same dispatch machinery.
func isOneOfGeneric(n *types.Named) bool {
	origin := n.Origin()
	if origin == nil {
		return false
	}
	obj := origin.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	switch obj.Pkg().Path() {
	case qPkgImportPath:
		return strings.HasPrefix(obj.Name(), "OneOf") && len(obj.Name()) > len("OneOf")
	case qEitherImportPath:
		return obj.Name() == "Either"
	}
	return false
}

// armsForType returns the variant list for a OneOfN-derived type t,
// or (nil, false) if t isn't OneOfN-derived. Handles three cases:
//
//   - `type Status q.OneOf2[…]` — defined named type with an OneOfN
//     RHS; recovered via the package's TypeSpec walk (onesByName).
//   - `type Result = either.Either[L, R]` — alias of an OneOfN
//     instantiation; unaliased to the named instantiation, then
//     served via TypeArgs().
//   - Bare `q.OneOf2[…]` / `either.Either[L, R]` with no alias —
//     served directly via TypeArgs().
func armsForType(t types.Type, onesByName map[*types.TypeName]oneOfArms, pkgPath string) (oneOfArms, bool) {
	t = types.Unalias(t)
	named, ok := t.(*types.Named)
	if !ok {
		return oneOfArms{}, false
	}
	if isOneOfGeneric(named) {
		args := named.TypeArgs()
		if args == nil {
			return oneOfArms{}, false
		}
		qualifier := func(p *types.Package) string {
			if p == nil || p.Path() == pkgPath {
				return ""
			}
			return p.Name()
		}
		arms := oneOfArms{}
		for i := 0; i < args.Len(); i++ {
			a := args.At(i)
			arms.Types = append(arms.Types, a)
			arms.Texts = append(arms.Texts, types.TypeString(a, qualifier))
		}
		return arms, true
	}
	if obj := named.Obj(); obj != nil {
		if arms, ok := onesByName[obj]; ok {
			return arms, true
		}
	}
	return oneOfArms{}, false
}

// resolveAsOneOf validates a q.AsOneOf[T](v) call site. T must be
// a OneOfN-derived type; v's type must be identical to one of T's
// arms. Populates sc.OneOfArmIdx (1-based) and sc.OneOfTypeText
// for the rewriter.
func resolveAsOneOf(fset *token.FileSet, sc *qSubCall, info *types.Info, pkgPath string, ones map[*types.TypeName]oneOfArms) (Diagnostic, bool) {
	if sc.AsType == nil || sc.InnerExpr == nil {
		return Diagnostic{}, false
	}
	tv, ok := info.Types[sc.AsType]
	if !ok || tv.Type == nil {
		return Diagnostic{}, false
	}
	pos := fset.Position(sc.OuterCall.Pos())
	// q.AsOneOf is the struct-flavour constructor — for Sealed-marker
	// interfaces, variants flow as themselves and the user just writes
	// `var m T = Variant{...}` directly. Reject early with a directed
	// diagnostic.
	if named, ok := types.Unalias(tv.Type).(*types.Named); ok {
		if _, isIface := named.Underlying().(*types.Interface); isIface {
			return Diagnostic{
				File: pos.Filename,
				Line: pos.Line,
				Col:  pos.Column,
				Msg:  fmt.Sprintf("q.AsOneOf[T]: T (%s) is a Sealed-marker interface — variants implement it via the synthesised marker, so just construct the variant value directly (e.g. `var m %s = Variant{...}`)", named.Obj().Name(), named.Obj().Name()),
			}, true
		}
	}
	arms, ok := armsForType(tv.Type, ones, pkgPath)
	if !ok {
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q.AsOneOf[T]: T must be a q.OneOfN-derived sum type (e.g. `type Status q.OneOf3[A, B, C]`); got %s", types.TypeString(tv.Type, nil)),
		}, true
	}
	if d, ok := checkArmsDistinct(fset, sc, arms); ok {
		return d, true
	}
	innerTV, ok := info.Types[sc.InnerExpr]
	if !ok || innerTV.Type == nil {
		return Diagnostic{}, false
	}
	idx := -1
	for i, a := range arms.Types {
		if types.Identical(innerTV.Type, a) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q.AsOneOf[T]: value type %s is not one of T's variants (accepted: %s)", types.TypeString(innerTV.Type, nil), strings.Join(arms.Texts, ", ")),
		}, true
	}
	sc.OneOfArmIdx = idx + 1
	// Spell T as it should appear in the composite literal.
	qualifier := func(p *types.Package) string {
		if p == nil || p.Path() == pkgPath {
			return ""
		}
		return p.Name()
	}
	sc.OneOfTypeText = types.TypeString(tv.Type, qualifier)
	return Diagnostic{}, false
}

// checkArmsDistinct surfaces a diagnostic when T has duplicate arm
// types (e.g. q.OneOf2[int, int]) — the variant would be ambiguous
// at AsOneOf time and the type-tag dispatch would silently route
// every int to the first arm.
func checkArmsDistinct(fset *token.FileSet, sc *qSubCall, arms oneOfArms) (Diagnostic, bool) {
	for i := 0; i < len(arms.Types); i++ {
		for j := i + 1; j < len(arms.Types); j++ {
			if types.Identical(arms.Types[i], arms.Types[j]) {
				pos := fset.Position(sc.OuterCall.Pos())
				return Diagnostic{
					File: pos.Filename,
					Line: pos.Line,
					Col:  pos.Column,
					Msg:  fmt.Sprintf("q: OneOfN type has duplicate arm %s — variants must be type-distinct (positions %d and %d)", arms.Texts[i], i+1, j+1),
				}, true
			}
		}
	}
	return Diagnostic{}, false
}

// resolveMatchOneOf is invoked from resolveMatch when the matched
// value's type is a OneOfN-derived sum. It classifies each q.Case
// arm's cond as a tag-arm (cond's type matches one of the variants)
// and each q.OnType arm's handler param type as the variant. Then
// enforces coverage: every variant has at least one tag-arm OR the
// match has a q.Default fallback.
//
// Mixing q.OnType arms with q.Case arms is allowed; mixing q.OnType
// with non-tag value/predicate q.Case arms (i.e. arms whose cond is
// of the matched value's own type, or bool, or func()T/bool) is
// rejected — the dispatch shapes are incompatible.
func resolveMatchOneOf(fset *token.FileSet, sc *qSubCall, info *types.Info, pkgPath string, arms oneOfArms) (Diagnostic, bool) {
	sc.IsOneOfMatch = true
	sc.OneOfArmTypeTexts = arms.Texts
	// Detect Sealed (interface) flavour vs OneOfN (struct) flavour:
	// the rewriter emits a Go type switch instead of a Tag switch.
	if matchedTV, ok := info.Types[sc.InnerExpr]; ok && matchedTV.Type != nil {
		if named, isNamed := types.Unalias(matchedTV.Type).(*types.Named); isNamed {
			if _, isIface := named.Underlying().(*types.Interface); isIface {
				sc.OneOfIsInterface = true
			}
		}
	}

	qualifier := func(p *types.Package) string {
		if p == nil || p.Path() == pkgPath {
			return ""
		}
		return p.Name()
	}

	// Classify each non-default arm. A q.Case arm whose cond's type
	// matches a variant becomes a tag-arm. A q.OnType arm extracts T
	// from the handler's first parameter type.
	covered := map[int]bool{}
	for i := range sc.MatchCases {
		mc := &sc.MatchCases[i]
		if mc.IsDefault {
			continue
		}
		if mc.IsOnType {
			handlerTV, ok := info.Types[mc.HandlerExpr]
			if !ok || handlerTV.Type == nil {
				continue
			}
			sig, ok := handlerTV.Type.(*types.Signature)
			if !ok || sig.Params().Len() != 1 || sig.Results().Len() != 1 {
				pos := fset.Position(mc.HandlerExpr.Pos())
				return Diagnostic{
					File: pos.Filename,
					Line: pos.Line,
					Col:  pos.Column,
					Msg:  "q.OnType: handler must be a function with one parameter (the typed variant) and one return value (the result)",
				}, true
			}
			paramT := sig.Params().At(0).Type()
			idx := variantIndex(paramT, arms.Types)
			if idx < 0 {
				pos := fset.Position(mc.HandlerExpr.Pos())
				return Diagnostic{
					File: pos.Filename,
					Line: pos.Line,
					Col:  pos.Column,
					Msg:  fmt.Sprintf("q.OnType: handler parameter type %s is not a variant of the matched sum (accepted: %s)", types.TypeString(paramT, qualifier), strings.Join(arms.Texts, ", ")),
				}, true
			}
			mc.OnTypeArmIdx = idx + 1
			mc.OnTypeArmText = arms.Texts[idx]
			if r := sig.Results().At(0).Type(); r != nil {
				if sc.ResolvedString == "" {
					sc.ResolvedString = types.TypeString(r, qualifier)
				}
			}
			covered[idx] = true
			continue
		}
		// q.Case in a OneOf match: cond's TYPE selects the variant.
		// The cond value itself is ignored — typically a zero literal
		// like Pending{} or q.A[Pending]() to spell the type.
		condTV, ok := info.Types[mc.CondExpr]
		if !ok || condTV.Type == nil {
			continue
		}
		idx := variantIndex(condTV.Type, arms.Types)
		if idx < 0 {
			pos := fset.Position(mc.CondExpr.Pos())
			return Diagnostic{
				File: pos.Filename,
				Line: pos.Line,
				Col:  pos.Column,
				Msg:  fmt.Sprintf("q.Case on a q.OneOfN value: cond type %s is not a variant (accepted: %s)", types.TypeString(condTV.Type, qualifier), strings.Join(arms.Texts, ", ")),
			}, true
		}
		mc.OnTypeArmIdx = idx + 1
		mc.OnTypeArmText = arms.Texts[idx]
		covered[idx] = true
	}

	// Result-type inference: q.Case arms in OneOf mode produce
	// ResultExpr R-typed values like the regular path; q.OnType arms
	// produce R via the handler's return type. Fall back to the first
	// arm whose result type is resolvable.
	if sc.ResolvedString == "" {
		for i := range sc.MatchCases {
			mc := &sc.MatchCases[i]
			if mc.IsDefault || mc.IsOnType {
				continue
			}
			if tv, ok := info.Types[mc.ResultExpr]; ok && tv.Type != nil {
				sc.ResolvedString = types.TypeString(tv.Type, qualifier)
				break
			}
		}
	}
	if sc.ResolvedString == "" {
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

	// Coverage: every arm must have a tag-arm OR a q.Default exists.
	hasDefault := false
	for _, mc := range sc.MatchCases {
		if mc.IsDefault {
			hasDefault = true
			break
		}
	}
	if hasDefault {
		return Diagnostic{}, false
	}
	var missing []string
	for i := range arms.Types {
		if !covered[i] {
			missing = append(missing, arms.Texts[i])
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q: q.Match on q.OneOfN-derived value is missing arm(s) for: %s. Add q.Case(<variant>{}, …) / q.OnType(func(<variant>) …), or add a q.Default(…) arm.", strings.Join(missing, ", ")),
		}, true
	}
	return Diagnostic{}, false
}

// resolveSealedDirective validates a `var _ = q.Sealed[I](v1, v2, …)`
// directive at typecheck time and registers the closed set in the
// shared sealed-arms map.
//
// Validates:
//   - I is a defined named interface type with exactly one method.
//   - That method takes no args and returns no values (a marker).
//   - Each variadic arg's static type is a same-package named type.
//   - Each variant's type is type-distinct from the others.
//
// Populates sc.SealedMarkerName and sc.SealedVariantNames so the
// synthesis pass can emit the per-variant marker method bodies.
func resolveSealedDirective(fset *token.FileSet, sc *qSubCall, info *types.Info, pkgPath string, sealedMap map[*types.TypeName]oneOfArms) (Diagnostic, bool) {
	if sc.AsType == nil {
		return Diagnostic{}, false
	}
	tv, ok := info.Types[sc.AsType]
	if !ok || tv.Type == nil {
		return Diagnostic{}, false
	}
	pos := fset.Position(sc.OuterCall.Pos())
	iNamed, ok := tv.Type.(*types.Named)
	if !ok {
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q.Sealed[I]: I must be a defined named interface type; got %s", types.TypeString(tv.Type, nil)),
		}, true
	}
	iface, ok := iNamed.Underlying().(*types.Interface)
	if !ok {
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q.Sealed[I]: I (%s) must be an interface type; got underlying %s", iNamed.Obj().Name(), iNamed.Underlying().String()),
		}, true
	}
	// Must have exactly one method, no embedded interfaces.
	if iface.NumMethods() != 1 || iface.NumEmbeddeds() != 0 {
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q.Sealed[I]: I (%s) must have exactly one method (the marker), no embeddings; got %d methods, %d embeddings", iNamed.Obj().Name(), iface.NumMethods(), iface.NumEmbeddeds()),
		}, true
	}
	method := iface.Method(0)
	sig, ok := method.Type().(*types.Signature)
	if !ok || sig.Params().Len() != 0 || sig.Results().Len() != 0 {
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q.Sealed[I]: I's marker method (%s) must take no args and return no values", method.Name()),
		}, true
	}
	sc.SealedMarkerName = method.Name()

	// Validate variants and accumulate arms.
	qualifier := func(p *types.Package) string {
		if p == nil || p.Path() == pkgPath {
			return ""
		}
		return p.Name()
	}
	arms := oneOfArms{}
	for i, v := range sc.SealedVariants {
		vtv, ok := info.Types[v]
		if !ok || vtv.Type == nil {
			pos := fset.Position(v.Pos())
			return Diagnostic{
				File: pos.Filename,
				Line: pos.Line,
				Col:  pos.Column,
				Msg:  fmt.Sprintf("q.Sealed[I]: variant arg %d has no resolvable type — pass a zero value of the variant type (e.g. Variant{})", i+1),
			}, true
		}
		vt := vtv.Type
		named, isNamed := vt.(*types.Named)
		if !isNamed {
			pos := fset.Position(v.Pos())
			return Diagnostic{
				File: pos.Filename,
				Line: pos.Line,
				Col:  pos.Column,
				Msg:  fmt.Sprintf("q.Sealed[I]: variant arg %d (%s) must be a named type — pass a zero value of a defined type (e.g. Variant{})", i+1, types.TypeString(vt, nil)),
			}, true
		}
		obj := named.Obj()
		if obj.Pkg() == nil || obj.Pkg().Path() != pkgPath {
			pos := fset.Position(v.Pos())
			return Diagnostic{
				File: pos.Filename,
				Line: pos.Line,
				Col:  pos.Column,
				Msg:  fmt.Sprintf("q.Sealed[I]: variant %s must be declared in the same package as the q.Sealed call — Go disallows method declarations on types defined in another package, so the marker can't be synthesised on a foreign type", types.TypeString(vt, nil)),
			}, true
		}
		// Check distinct.
		for _, prev := range arms.Types {
			if types.Identical(prev, vt) {
				pos := fset.Position(v.Pos())
				return Diagnostic{
					File: pos.Filename,
					Line: pos.Line,
					Col:  pos.Column,
					Msg:  fmt.Sprintf("q.Sealed[I]: duplicate variant %s — variants must be type-distinct", obj.Name()),
				}, true
			}
		}
		arms.Types = append(arms.Types, vt)
		arms.Texts = append(arms.Texts, types.TypeString(vt, qualifier))
		sc.SealedVariantNames = append(sc.SealedVariantNames, obj.Name())
	}
	if len(arms.Types) == 0 {
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q.Sealed[I] (%s): no variants supplied — pass at least one variant zero value", iNamed.Obj().Name()),
		}, true
	}

	sealedMap[iNamed.Obj()] = arms
	return Diagnostic{}, false
}

// variantIndex returns the 0-based position of t within arms, or -1
// when t isn't one of the variants. Comparison uses types.Identical
// (so a defined type and its underlying are distinct, which is what
// we want).
func variantIndex(t types.Type, arms []types.Type) int {
	for i, a := range arms {
		if types.Identical(t, a) {
			return i
		}
	}
	return -1
}

// validateExhaustiveOneOf is invoked from validateExhaustive when the
// q.Exhaustive call is the X of an `.(type)` assertion in a type
// switch. Two shapes are accepted:
//
//   - q.Exhaustive(<x>.Value).(type) — OneOfN struct flavour. <x>'s
//     type is OneOfN-derived; coverage uses its variant list.
//   - q.Exhaustive(<m>).(type) — Sealed flavour. <m>'s static type
//     is the marker interface; coverage uses the registered closed
//     set.
//
// Validates each case clause type matches a variant and enforces
// coverage. default: opts out.
func validateExhaustiveOneOf(fset *token.FileSet, sw *ast.TypeSwitchStmt, sc *qSubCall, info *types.Info, pkgPath string, ones map[*types.TypeName]oneOfArms) (Diagnostic, bool) {
	innerTV, hasInner := info.Types[sc.InnerExpr]
	var arms oneOfArms
	var found bool
	// Sealed flavour: the inner expression is itself the marker
	// interface value (not <x>.Value). Look up by the type's TypeName.
	if hasInner && innerTV.Type != nil {
		if a, ok := armsForType(innerTV.Type, ones, pkgPath); ok {
			arms = a
			found = true
		}
	}
	// OneOfN struct flavour: the inner expression is <x>.Value.
	if !found {
		sel, ok := sc.InnerExpr.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil || sel.Sel.Name != "Value" {
			return Diagnostic{}, false
		}
		xtv, ok := info.Types[sel.X]
		if !ok || xtv.Type == nil {
			return Diagnostic{}, false
		}
		a, ok := armsForType(xtv.Type, ones, pkgPath)
		if !ok {
			// Not a OneOfN — let regular q.Exhaustive validation try.
			return Diagnostic{}, false
		}
		arms = a
	}
	if d, ok := checkArmsDistinct(fset, sc, arms); ok {
		return d, true
	}
	covered := map[int]bool{}
	hasDefault := false
	for _, stmt := range sw.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		if len(cc.List) == 0 {
			hasDefault = true
			continue
		}
		for _, e := range cc.List {
			tv, ok := info.Types[e]
			if !ok || tv.Type == nil {
				continue
			}
			idx := variantIndex(tv.Type, arms.Types)
			if idx < 0 {
				pos := fset.Position(e.Pos())
				return Diagnostic{
					File: pos.Filename,
					Line: pos.Line,
					Col:  pos.Column,
					Msg:  fmt.Sprintf("q.Exhaustive on sealed sum: case type %s is not a variant (accepted: %s)", types.TypeString(tv.Type, nil), strings.Join(arms.Texts, ", ")),
				}, true
			}
			covered[idx] = true
		}
	}
	if hasDefault {
		return Diagnostic{}, false
	}
	var missing []string
	for i := range arms.Types {
		if !covered[i] {
			missing = append(missing, arms.Texts[i])
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q.Exhaustive type switch on sealed sum is missing case(s) for: %s. Add the missing case(s), or use `default:` to opt out.", strings.Join(missing, ", ")),
		}, true
	}
	return Diagnostic{}, false
}

// findExhaustiveTypeSwitchParent looks for the enclosing TypeSwitchStmt
// whose tag is `<assign> := <expr>.(type)` where <expr> is sc.OuterCall
// (the q.Exhaustive call). Returns nil when sc isn't in such a position.
func findExhaustiveTypeSwitchParent(files []*ast.File, sc *qSubCall) *ast.TypeSwitchStmt {
	var found *ast.TypeSwitchStmt
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if found != nil {
				return false
			}
			sw, ok := n.(*ast.TypeSwitchStmt)
			if !ok {
				return true
			}
			// The TypeSwitchStmt's Assign holds either:
			//   AssignStmt: v := <call>.(type)
			//   ExprStmt:        <call>.(type)
			var ta *ast.TypeAssertExpr
			switch a := sw.Assign.(type) {
			case *ast.AssignStmt:
				if len(a.Rhs) != 1 {
					return true
				}
				ta, _ = a.Rhs[0].(*ast.TypeAssertExpr)
			case *ast.ExprStmt:
				ta, _ = a.X.(*ast.TypeAssertExpr)
			}
			if ta == nil || ta.Type != nil {
				return true
			}
			// ta.X is the expression being type-asserted. We need it to
			// be sc.OuterCall (the q.Exhaustive call).
			if ta.X == sc.OuterCall {
				found = sw
				return false
			}
			return true
		})
		if found != nil {
			break
		}
	}
	return found
}

// buildAsOneOfReplacement emits the composite literal for a
// q.AsOneOf[T](v) call:
//
//	q.AsOneOf[T](v) → T{Tag: <pos>, Value: v}
//
// Falls back to a defensive comment when the typecheck pass didn't
// run (rewriter_test path); the build still parses so the diagnostic
// can flow through.
func buildAsOneOfReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string) string {
	if sub.OneOfArmIdx == 0 || sub.OneOfTypeText == "" {
		return `/* q.AsOneOf: typecheck skipped — variant unresolved */ struct{}{}`
	}
	innerText := exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	return fmt.Sprintf("%s{Tag: %d, Value: %s}", sub.OneOfTypeText, sub.OneOfArmIdx, innerText)
}

// buildOneOfMatchReplacement emits the IIFE-wrapped switch for a
// q.Match call whose matched value is a OneOfN-derived sum. Two
// flavours:
//
//   - struct (OneOfN): switch on _v.Tag; arms unwrap via _v.Value.(T).
//   - interface (Sealed): Go type switch on _v.(type); arms bind the
//     unwrapped variant via the case clause's named binding.
//
//	struct flavour:
//	  (func() R {
//	      _v := <value>
//	      switch _v.Tag {
//	      case 1: return <r1>
//	      case 2: return (handler2)(_v.Value.(T2))
//	      default: return <defaultResult>      // when q.Default present
//	      }
//	      var _zero R; return _zero            // when no q.Default
//	  }())
//
//	interface flavour:
//	  (func() R {
//	      switch _v := <value>.(type) {
//	      case T1: return <r1>; _ = _v
//	      case T2: return (handler2)(_v)
//	      default: return <defaultResult>
//	      }
//	      var _zero R; return _zero
//	  }())
func buildOneOfMatchReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string) string {
	if sub.OneOfIsInterface {
		return buildSealedMatchReplacement(fset, src, sub, subs, subTexts)
	}
	valueText := exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	resultType := sub.ResolvedString
	if resultType == "" {
		resultType = "any"
	}

	// Group arms by tag so multiple arms with the same variant (rare,
	// but the user may pre-destructure) all funnel to the first one.
	type armEmit struct {
		idx  int
		body string
	}
	var armLines []armEmit
	var defaultText string
	hasDefault := false
	for _, mc := range sub.MatchCases {
		if mc.IsDefault {
			defaultText = exprTextSubst(fset, src, mc.ResultExpr, subs, subTexts)
			hasDefault = true
			continue
		}
		if mc.IsOnType {
			handlerText := exprTextSubst(fset, src, mc.HandlerExpr, subs, subTexts)
			body := fmt.Sprintf("return (%s)(_v.Value.(%s))", handlerText, mc.OnTypeArmText)
			armLines = append(armLines, armEmit{idx: mc.OnTypeArmIdx, body: body})
			continue
		}
		// q.Case in OneOf mode — cond's type chose the variant; the
		// cond value itself is dropped (kept around as `_ = <cond>` if
		// it could have side effects? No — the user wrote a zero
		// literal in practice. Drop entirely for cleanliness.)
		resultText := exprTextSubst(fset, src, mc.ResultExpr, subs, subTexts)
		body := "return " + resultText
		armLines = append(armLines, armEmit{idx: mc.OnTypeArmIdx, body: body})
	}

	// Stable order by tag for deterministic output.
	sort.SliceStable(armLines, func(a, b int) bool { return armLines[a].idx < armLines[b].idx })

	var caseStmts []string
	for _, a := range armLines {
		caseStmts = append(caseStmts, fmt.Sprintf("case %d: %s", a.idx, a.body))
	}
	cases := strings.Join(caseStmts, "; ")
	if hasDefault {
		return fmt.Sprintf("(func() %s { _v := %s; switch _v.Tag { %s; default: return %s } }())",
			resultType, valueText, cases, defaultText)
	}
	return fmt.Sprintf("(func() %s { _v := %s; switch _v.Tag { %s }; var _zero %s; return _zero }())",
		resultType, valueText, cases, resultType)
}

// buildSealedMatchReplacement emits the type-switch IIFE for q.Match
// on a Sealed-marker interface value.
func buildSealedMatchReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string) string {
	valueText := exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	resultType := sub.ResolvedString
	if resultType == "" {
		resultType = "any"
	}

	type armEmit struct {
		idx       int
		variant   string
		body      string
		bindsVar  bool // whether the case body uses _v (true for OnType, false for q.Case)
	}
	var armLines []armEmit
	var defaultText string
	hasDefault := false
	for _, mc := range sub.MatchCases {
		if mc.IsDefault {
			defaultText = exprTextSubst(fset, src, mc.ResultExpr, subs, subTexts)
			hasDefault = true
			continue
		}
		if mc.IsOnType {
			handlerText := exprTextSubst(fset, src, mc.HandlerExpr, subs, subTexts)
			armLines = append(armLines, armEmit{
				idx:      mc.OnTypeArmIdx,
				variant:  mc.OnTypeArmText,
				body:     fmt.Sprintf("return (%s)(_v)", handlerText),
				bindsVar: true,
			})
			continue
		}
		resultText := exprTextSubst(fset, src, mc.ResultExpr, subs, subTexts)
		armLines = append(armLines, armEmit{
			idx:      mc.OnTypeArmIdx,
			variant:  mc.OnTypeArmText,
			body:     "return " + resultText,
			bindsVar: false,
		})
	}

	sort.SliceStable(armLines, func(a, b int) bool { return armLines[a].idx < armLines[b].idx })

	var caseStmts []string
	for _, a := range armLines {
		body := a.body
		if !a.bindsVar {
			// q.Case form: `case T: _ = _v; return …` so the binding
			// stays referenced even when the result expression doesn't
			// touch the payload.
			body = "_ = _v; " + body
		}
		caseStmts = append(caseStmts, fmt.Sprintf("case %s: %s", a.variant, body))
	}
	cases := strings.Join(caseStmts, "; ")
	if hasDefault {
		return fmt.Sprintf("(func() %s { switch _v := (%s).(type) { %s; default: _ = _v; return %s } }())",
			resultType, valueText, cases, defaultText)
	}
	return fmt.Sprintf("(func() %s { switch _v := (%s).(type) { %s }; var _zero %s; return _zero }())",
		resultType, valueText, cases, resultType)
}

