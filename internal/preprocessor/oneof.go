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
func resolveMatchOneOf(fset *token.FileSet, sc *qSubCall, info *types.Info, pkgPath string, arms oneOfArms, ones map[*types.TypeName]oneOfArms) (Diagnostic, bool) {
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

	// Pre-compute the leaf list for nested-sum support (Phase C). When
	// any arm targets a leaf at depth > 1, the rewriter emits nested
	// switches grouped by outer tag.
	leaves := flattenArms(arms, sc.OneOfIsInterface, ones, pkgPath, nil, nil, map[string]bool{})

	// nestedMode is set true if any arm targets a leaf at depth > 1.
	// Once flipped, ALL arms must resolve via the leaf list (no mixing
	// immediate-arm + leaf in the same q.Match — the dispatch shape
	// would be ambiguous).
	nestedMode := false

	// findArm returns the leaf path for armType, preferring an
	// immediate-arm match (depth 1) when one exists. This lets the
	// user write `q.OnType(func(ImmediateArm) ...)` to handle a sum
	// arm "as a unit" while writing `q.OnType(func(Leaf) ...)` for
	// fine-grained leaves; both work because the immediate-arm match
	// is the depth-1 leaf in the flattened list.
	findArm := func(armType types.Type) *leafPath {
		// Prefer exact match at any depth — type-distinct arm guarantee
		// means no two leaves have the same type.
		for i := range leaves {
			if types.Identical(leaves[i].LeafType, armType) {
				return &leaves[i]
			}
		}
		return nil
	}

	// Classify each non-default arm.
	for i := range sc.MatchCases {
		mc := &sc.MatchCases[i]
		if mc.IsDefault {
			continue
		}
		var armType types.Type
		var armPos token.Pos
		var armOrigin string
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
			armType = sig.Params().At(0).Type()
			armPos = mc.HandlerExpr.Pos()
			armOrigin = "q.OnType handler parameter"
			if r := sig.Results().At(0).Type(); r != nil {
				if sc.ResolvedString == "" {
					sc.ResolvedString = types.TypeString(r, qualifier)
				}
			}
		} else {
			condTV, ok := info.Types[mc.CondExpr]
			if !ok || condTV.Type == nil {
				continue
			}
			armType = condTV.Type
			armPos = mc.CondExpr.Pos()
			armOrigin = "q.Case cond"
		}

		leaf := findArm(armType)
		if leaf == nil {
			pos := fset.Position(armPos)
			return Diagnostic{
				File: pos.Filename,
				Line: pos.Line,
				Col:  pos.Column,
				Msg:  fmt.Sprintf("%s type %s is not a variant of the matched sum or any of its nested arms", armOrigin, types.TypeString(armType, qualifier)),
			}, true
		}
		// Always populate immediate-level fields (path[0]) for the
		// flat-emit path to work for depth-1 arms.
		mc.OnTypeArmIdx = leaf.Path[0]
		mc.OnTypeArmText = leaf.Steps[0].ArmText
		// Carry the full descent for the nested-emit path.
		mc.NestedPath = leaf.Path
		mc.NestedSteps = make([]nestedMatchStep, len(leaf.Steps))
		for j, s := range leaf.Steps {
			mc.NestedSteps[j] = nestedMatchStep{ArmText: s.ArmText, IsIface: s.IsIface}
		}
		if len(leaf.Path) > 1 {
			nestedMode = true
		}
	}

	if nestedMode {
		return resolveMatchNested(fset, sc, info, pkgPath, leaves, qualifier)
	}

	// Flat (depth-1) coverage check — every immediate arm must have
	// at least one tag-arm OR a q.Default exists.
	covered := map[int]bool{}
	for _, mc := range sc.MatchCases {
		if mc.IsDefault {
			continue
		}
		if mc.OnTypeArmIdx > 0 {
			covered[mc.OnTypeArmIdx-1] = true
		}
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

// resolveMatchNested handles q.Match coverage when at least one arm
// targets a leaf at depth > 1 in a nested sum (Phase C). The
// per-arm classifier already ran in resolveMatchOneOf and populated
// each matchCase.NestedPath. This pass enforces coverage over the
// flat leaf set: every leaf must be reachable by some arm or the
// match must include q.Default.
//
// "Reachable" means: there exists a non-default arm whose
// NestedPath is a prefix of the leaf's path. The depth-1 case is
// the trivial "exact match"; deeper paths reach further into the
// nested sum.
func resolveMatchNested(fset *token.FileSet, sc *qSubCall, info *types.Info, pkgPath string, leaves []leafPath, qualifier types.Qualifier) (Diagnostic, bool) {
	hasDefault := false
	for _, mc := range sc.MatchCases {
		if mc.IsDefault {
			hasDefault = true
			break
		}
	}

	// Result type: prefer the first OnType handler's return type;
	// fall back to q.Default's result expression.
	if sc.ResolvedString == "" {
		for _, mc := range sc.MatchCases {
			if mc.IsDefault || !mc.IsOnType {
				continue
			}
			if tv, ok := info.Types[mc.HandlerExpr]; ok && tv.Type != nil {
				if sig, ok := tv.Type.(*types.Signature); ok && sig.Results().Len() == 1 {
					sc.ResolvedString = types.TypeString(sig.Results().At(0).Type(), qualifier)
					break
				}
			}
		}
	}
	if sc.ResolvedString == "" {
		for _, mc := range sc.MatchCases {
			if !mc.IsDefault {
				continue
			}
			if tv, ok := info.Types[mc.ResultExpr]; ok && tv.Type != nil {
				sc.ResolvedString = types.TypeString(tv.Type, qualifier)
				break
			}
		}
	}

	if hasDefault {
		return Diagnostic{}, false
	}
	// Coverage: every leaf must have a covering arm.
	armPaths := [][]int{}
	for _, mc := range sc.MatchCases {
		if mc.IsDefault {
			continue
		}
		if len(mc.NestedPath) == 0 {
			continue
		}
		armPaths = append(armPaths, mc.NestedPath)
	}
	covers := func(armPath, leafPath []int) bool {
		if len(armPath) > len(leafPath) {
			return false
		}
		for i, v := range armPath {
			if leafPath[i] != v {
				return false
			}
		}
		return true
	}
	var missing []string
	for _, leaf := range leaves {
		anyCover := false
		for _, ap := range armPaths {
			if covers(ap, leaf.Path) {
				anyCover = true
				break
			}
		}
		if !anyCover {
			missing = append(missing, leaf.LeafText)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  fmt.Sprintf("q: q.Match (nested-sum dispatch) is missing arm(s) for leaf(s): %s. Add q.OnType(func(<leaf>) …) for each, or add a q.Default(…) arm.", strings.Join(missing, ", ")),
		}, true
	}
	return Diagnostic{}, false
}

// leafPath is one entry in the flattened-leaf list for a (possibly
// nested) sum. Path is the 1-based descent indices from the root
// type to the leaf (e.g. [1, 2] = "outer arm 1, inner arm 2"). Steps
// is parallel to Path: each entry describes the type at that level
// of descent, used by the rewriter to emit the right type assertion
// (`_vN.Value.(StepType)`) and the right inner-switch shape. The
// LeafType is the final variant the path resolves to.
type leafStep struct {
	ArmType  types.Type // the arm type at this level
	ArmText  string     // textual spelling for the type assertion
	IsIface  bool       // parent at this level is interface (Sealed) → use type switch
}

type leafPath struct {
	LeafType types.Type
	LeafText string
	Path     []int
	Steps    []leafStep
}

// flattenArms walks `arms` (the immediate-arm list of some root sum)
// recursively, returning every leaf with its descent path. An arm is
// a "leaf" if it's NOT itself a sum-derived type; otherwise we
// recurse into its arm list. Each step records whether the parent
// at that level was a struct (OneOf) or an interface (Sealed) so
// the rewriter can pick the right dispatch shape.
//
// Uses `seen` to break cycles — a recursive sum (`type T q.OneOf2[A, T]`)
// would otherwise loop. When a cycle is detected the cycling arm is
// treated as a leaf (its descent stops there).
func flattenArms(arms oneOfArms, parentIsIface bool, ones map[*types.TypeName]oneOfArms, pkgPath string, prefix []int, prefixSteps []leafStep, seen map[string]bool) []leafPath {
	var out []leafPath
	for i, t := range arms.Types {
		path := append(append([]int{}, prefix...), i+1)
		step := leafStep{ArmType: t, ArmText: arms.Texts[i], IsIface: parentIsIface}
		steps := append(append([]leafStep{}, prefixSteps...), step)

		key := types.TypeString(t, nil)
		if seen[key] {
			out = append(out, leafPath{LeafType: t, LeafText: arms.Texts[i], Path: path, Steps: steps})
			continue
		}
		subArms, ok := armsForType(t, ones, pkgPath)
		if !ok {
			out = append(out, leafPath{LeafType: t, LeafText: arms.Texts[i], Path: path, Steps: steps})
			continue
		}
		// Recurse — record the parent's flavour for the NEXT level.
		nextParentIsIface := false
		if named, ok := types.Unalias(t).(*types.Named); ok {
			if _, isIface := named.Underlying().(*types.Interface); isIface {
				nextParentIsIface = true
			}
		}
		subSeen := map[string]bool{}
		for k := range seen {
			subSeen[k] = true
		}
		subSeen[key] = true
		out = append(out, flattenArms(subArms, nextParentIsIface, ones, pkgPath, path, steps, subSeen)...)
	}
	return out
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
		// Only enqueue for marker-method synthesis if the variant
		// doesn't already declare one. Hand-written markers let users
		// keep their pre-rewrite source IDE-clean (no red squiggles
		// from the Go-side typecheck not seeing the synthesised
		// method); the synthesis pass would otherwise emit a
		// duplicate-method compile error.
		if !variantHasMarker(vt, sc.SealedMarkerName) {
			sc.SealedVariantNames = append(sc.SealedVariantNames, obj.Name())
		}
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

// variantHasMarker reports whether the variant type already declares
// a method named markerName with no params and no results — i.e. the
// user has hand-written the marker themselves so the synthesis pass
// must skip it (or the Go compiler would see two declarations of the
// same method on the same receiver).
//
// Checks both T and *T's method sets (Go promotes value-receiver
// methods through pointer types). Param/result shapes other than
// "no args, no results" don't count — they couldn't satisfy the
// marker contract.
func variantHasMarker(vt types.Type, markerName string) bool {
	if markerName == "" {
		return false
	}
	candidates := []types.Type{vt}
	if _, isPtr := vt.(*types.Pointer); !isPtr {
		candidates = append(candidates, types.NewPointer(vt))
	}
	for _, t := range candidates {
		mset := types.NewMethodSet(t)
		for i := 0; i < mset.Len(); i++ {
			sel := mset.At(i)
			if sel.Obj().Name() != markerName {
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
			if sig.Params().Len() == 0 && sig.Results().Len() == 0 {
				return true
			}
		}
	}
	return false
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
	// Nested-sum dispatch (Phase C): any arm with a path deeper than 1
	// triggers the grouped-by-outer-tag emit.
	for _, mc := range sub.MatchCases {
		if !mc.IsDefault && len(mc.NestedPath) > 1 {
			return buildNestedMatchReplacement(fset, src, sub, subs, subTexts)
		}
	}
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

// buildNestedMatchReplacement emits a multi-level switch IIFE for
// q.Match on a nested sum (Phase C). Each arm carries a NestedPath
// of 1-based descent indices through the sum tree. The rewriter
// groups arms by their outer prefix at each level and emits a
// switch (Tag-switch for struct flavour, type-switch for interface
// flavour) recursively.
//
// Example shape for q.Either[ErrSet, OkSet] with leaf arms:
//
//	(func() string {
//	    _v0 := result
//	    switch _v0.Tag {
//	    case 1: {
//	        _v1 := _v0.Value.(ErrSet)
//	        switch _v1.Tag {
//	        case 1: return (handler)(_v1.Value.(NotFound))
//	        case 2: return (handler)(_v1.Value.(Forbidden))
//	        }
//	    }
//	    case 2: {
//	        _v1 := _v0.Value.(OkSet)
//	        switch _v1.Tag {
//	        case 1: return (handler)(_v1.Value.(Created))
//	        case 2: return (handler)(_v1.Value.(Updated))
//	        }
//	    }
//	    }
//	    var _zero string; return _zero
//	}())
func buildNestedMatchReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string) string {
	valueText := exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	resultType := sub.ResolvedString
	if resultType == "" {
		resultType = "any"
	}

	type nestedArm struct {
		Path     []int
		Steps    []nestedMatchStep
		Handler  string // OnType handler text (empty for q.Case)
		Result   string // q.Case result text (empty for OnType)
		IsOnType bool
	}
	var arms []nestedArm
	var defaultText string
	hasDefault := false
	for _, mc := range sub.MatchCases {
		if mc.IsDefault {
			defaultText = exprTextSubst(fset, src, mc.ResultExpr, subs, subTexts)
			hasDefault = true
			continue
		}
		a := nestedArm{
			Path:     mc.NestedPath,
			Steps:    mc.NestedSteps,
			IsOnType: mc.IsOnType,
		}
		if mc.IsOnType {
			a.Handler = exprTextSubst(fset, src, mc.HandlerExpr, subs, subTexts)
		} else {
			a.Result = exprTextSubst(fset, src, mc.ResultExpr, subs, subTexts)
		}
		arms = append(arms, a)
	}

	sort.SliceStable(arms, func(i, j int) bool {
		x, y := arms[i].Path, arms[j].Path
		for k := 0; k < len(x) && k < len(y); k++ {
			if x[k] != y[k] {
				return x[k] < y[k]
			}
		}
		return len(x) < len(y)
	})

	// emit walks the arm group at level `level` (0-based descent).
	// `parentIsIface` is the dispatch flavour at THIS level (i.e.
	// whether _v<level-1> is an interface that we type-switch on, or
	// a struct whose .Tag we Tag-switch on). At level 0 the "parent"
	// is the matched value itself.
	var emit func(level int, arms []nestedArm, parentIsIface bool, parentArmText string) string
	emit = func(level int, arms []nestedArm, parentIsIface bool, parentArmText string) string {
		// Group arms by their path[level].
		groups := map[int][]nestedArm{}
		var orderedKeys []int
		for _, a := range arms {
			if len(a.Path) <= level {
				continue
			}
			k := a.Path[level]
			if _, exists := groups[k]; !exists {
				orderedKeys = append(orderedKeys, k)
			}
			groups[k] = append(groups[k], a)
		}
		sort.Ints(orderedKeys)

		var caseStmts []string
		for _, k := range orderedKeys {
			groupArms := groups[k]
			armText := groupArms[0].Steps[level].ArmText
			// Split: arms terminating at this level vs arms going deeper.
			var direct []nestedArm
			var deeper []nestedArm
			for _, a := range groupArms {
				if len(a.Path) == level+1 {
					direct = append(direct, a)
				} else {
					deeper = append(deeper, a)
				}
			}
			var caseBody string
			if len(direct) > 0 {
				// Leaf — emit the arm body using the in-scope variable.
				// At level L, the in-scope variable (the value being
				// dispatched) is referenced as follows:
				//   - parentIsIface: _v<level> is the case-bound typed
				//     value (Go's `case T: ` clause exposes it under the
				//     switch-statement's binding name).
				//   - !parentIsIface (struct flavour): _v<level-1>.Value
				//     of static type any; the leaf value is
				//     _v<level-1>.Value.(armText) — armText IS the leaf
				//     type for a terminating arm.
				var leafAccess string
				if parentIsIface {
					// Sealed flavour at this level: case clause already
					// binds _v<level> to the typed leaf value.
					leafAccess = fmt.Sprintf("_v%d", level)
				} else {
					// Struct flavour: the variable in scope at level L
					// is _vL (bound by the level-L switch's preamble).
					// Its .Value is the variant; type-assert into armText.
					leafAccess = fmt.Sprintf("_v%d.Value.(%s)", level, armText)
				}
				if direct[0].IsOnType {
					caseBody = fmt.Sprintf("return (%s)(%s)", direct[0].Handler, leafAccess)
				} else {
					caseBody = fmt.Sprintf("_ = %s; return %s", leafAccess, direct[0].Result)
				}
			} else {
				// Descend. The flavour at the NEXT level is the flavour
				// of the type at THIS level (groupArms[0].Steps[level].IsIface).
				armIsIface := groupArms[0].Steps[level].IsIface
				caseBody = emit(level+1, deeper, armIsIface, armText)
			}
			if parentIsIface {
				caseStmts = append(caseStmts, fmt.Sprintf("case %s: %s", armText, caseBody))
			} else {
				caseStmts = append(caseStmts, fmt.Sprintf("case %d: %s", k, caseBody))
			}
		}

		body := strings.Join(caseStmts, "\n")
		// Bind the variable at this level + emit the switch.
		if level == 0 {
			if parentIsIface {
				return fmt.Sprintf("switch _v0 := (%s).(type) {\n%s\n}", valueText, body)
			}
			return fmt.Sprintf("_v0 := %s; switch _v0.Tag {\n%s\n}", valueText, body)
		}
		// Inner level: parentArmText was already type-asserted at the
		// PARENT case clause to bind a variable of that type.
		if parentIsIface {
			// The case-bound _v<level-1> (already the typed value).
			return fmt.Sprintf("{ switch _v%d := _v%d.(type) {\n%s\n} }", level, level-1, body)
		}
		// Struct parent: bind _v<level> by descending into Value.
		return fmt.Sprintf("{ _v%d := _v%d.Value.(%s); switch _v%d.Tag {\n%s\n} }", level, level-1, parentArmText, level, body)
	}

	body := emit(0, arms, sub.OneOfIsInterface, "")
	if hasDefault {
		return fmt.Sprintf("(func() %s { %s; _ = %s; return %s }())",
			resultType, body, valueText, defaultText)
	}
	return fmt.Sprintf("(func() %s { %s; var _zero %s; return _zero }())",
		resultType, body, resultType)
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

