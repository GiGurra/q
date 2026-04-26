package preprocessor

// assemble.go — typecheck + rewriter for the q.Assemble family.
// Two entries:
//
//   - q.Assemble[T](recipes...)             -> (T, error)
//   - q.AssembleCtx[T](ctx, recipes...)     -> (T, error)
//
// Both ALWAYS return (T, error). Pure-form / chain-form variants were
// considered and dropped: q.Try / q.TryE already covers both shapes
// at the call site (`q.Try(q.Assemble[*Server](...))` /
// `q.TryE(q.Assemble[*Server](...)).Wrap("init")`), so a uniform
// errored entry keeps the surface tight and the runtime nil-check
// always has somewhere to bubble.
//
// Pipeline per call site:
//
//   1. Scanner captures the recipe expressions onto sc.AssembleRecipes
//      and the target type [T] onto sc.AsType. For q.AssembleCtx the
//      ctx expression is captured separately as sc.AssembleCtxArg.
//   2. resolveAssemble (typecheck pass) reads each recipe's resolved
//      type via go/types, classifies it as a function reference
//      (*types.Signature) or an inline value, builds a provider map
//      keyed by go/types' canonical type-string, topo-sorts via Kahn's
//      algorithm, and emits a single multi-line diagnostic combining
//      every problem found (missing dep / duplicate provider / cycle /
//      unused recipe / unsatisfiable target / wrong recipe shape /
//      ambiguous interface input).
//   3. The rewriter emits an IIFE that returns (T, error). Each step
//      binds to a `_qDep<N>` temporary; errored recipes get a
//      `_qAErr<N>` slot with bubble-on-failure. Outputs whose type
//      can hold nil (pointer / interface / slice / map / chan / func)
//      get a runtime nil-check on the bound _qDep<N> immediately
//      after the call — bubbles `fmt.Errorf("...: %w", q.ErrNil)`
//      when violated, catching the typed-nil-interface pitfall
//      before the value is implicitly converted at consumer call
//      sites.
//
// Diagnostic philosophy. Auto-DI fails differently from a typo: with
// N recipes a single mistake can propagate as ten "missing"
// symptoms downstream. resolveAssemble validates everything in one
// pass and emits one diagnostic that lists EVERY problem, plus the
// dependency tree the resolver actually sees. The user fixes once
// and reruns instead of round-tripping per problem.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"sort"
	"strings"
)

// assembleRecipeInfo holds resolved type info for one recipe argument.
// Built in original-arg order by resolveAssemble; reused by the
// diagnostic helpers (cycle trace, dep-tree render, provider list)
// so they share the same view of the graph.
//
// `valid` is false when the recipe failed a per-recipe shape check
// (no return values, wrong second return, variadic, unresolved type).
// Such recipes are excluded from the provider map and the topo sort
// so a single bad recipe doesn't poison the rest of the graph.
//
// resolvedInputKeys is the per-input *producer's outputKey* (filled
// after the assignability resolution pass). For an exact-type match
// the resolvedInputKey equals the input's typeKey; for an interface
// input satisfied by a concrete provider, the resolvedInputKey is
// the concrete type's key — that's what depVar maps in the emit
// stage, so the IIFE references the right _qDep<N>.
//
// idx == -1 is the sentinel for the synthetic ctx provider (only
// used for q.AssembleCtx). The rewriter emits its source text from
// sub.AssembleCtxArg rather than sub.AssembleRecipes[idx], and
// diagnostic helpers label it as "ctx (auto-provided)" so user
// recipes keep their natural 1-based numbering.
type assembleRecipeInfo struct {
	idx               int
	expr              ast.Expr
	isValue           bool
	errored           bool
	isResource        bool
	autoCleanup       cleanupKind
	inputs            []types.Type
	inputKeys         []string
	resolvedInputKeys []string
	output            types.Type
	outputKey         string
	valid             bool
}

// isAssembleFamily reports whether sc's family is one of the
// q.Assemble entries. Used by the typecheck dispatcher to pick
// resolveAssemble.
func isAssembleFamily(f family) bool {
	return f == familyAssemble || f == familyAssembleAll || f == familyAssembleStruct
}

// assembleFamilyLabel is the user-facing helper name for diagnostics.
func assembleFamilyLabel(f family) string {
	switch f {
	case familyAssemble:
		return "q.Assemble"
	case familyAssembleAll:
		return "q.AssembleAll"
	case familyAssembleStruct:
		return "q.AssembleStruct"
	}
	return "q.Assemble<?>"
}

// resolveAssemble validates one q.Assemble call site and populates
// sc.AssembleSteps + sc.AssembleTargetTypeText + sc.AssembleTargetKey
// on success. On failure, returns one diagnostic that lists EVERY
// problem found (recipe-shape errors, duplicate providers, missing
// target, missing inputs, dependency cycles, ambiguous interface
// inputs, unused recipes), with enough context that the user can
// fix all of them in one round trip.
func resolveAssemble(fset *token.FileSet, sc *qSubCall, info *types.Info, pkgPath string) (Diagnostic, bool) {
	if sc.AsType == nil {
		return Diagnostic{}, false
	}
	qualifier := func(p *types.Package) string {
		if p == nil || p.Path() == pkgPath {
			return ""
		}
		return p.Name()
	}
	typeText := func(t types.Type) string { return types.TypeString(t, qualifier) }

	// Resolve T's type. If unresolved, the build's own type-checker
	// will surface a clearer message; skip silently.
	targetTV, ok := info.Types[sc.AsType]
	if !ok || targetTV.Type == nil {
		return Diagnostic{}, false
	}
	targetType := targetTV.Type
	sc.AssembleTargetTypeText = typeText(targetType)
	sc.AssembleTargetKey = typeKey(targetType)

	familyLabel := assembleFamilyLabel(sc.Family)
	errType := types.Universe.Lookup("error").Type()

	recipes := make([]assembleRecipeInfo, 0, len(sc.AssembleRecipes))
	var problems []string

	addProblem := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	for i, rExpr := range sc.AssembleRecipes {
		ri := assembleRecipeInfo{idx: i, expr: rExpr}
		tv, ok := info.Types[rExpr]
		if !ok || tv.Type == nil {
			addProblem("recipe #%d (%s) has unresolvable type — pass a function reference (e.g. `newDB`) or an inline value of a known type",
				i+1, snippet(fset, rExpr))
			recipes = append(recipes, ri)
			continue
		}

		if sig, isSig := tv.Type.(*types.Signature); isSig {
			results := sig.Results()
			switch results.Len() {
			case 0:
				addProblem("recipe #%d (%s) returns no values — recipes must return T, (T, error), or (T, func(), error)",
					i+1, snippet(fset, rExpr))
				recipes = append(recipes, ri)
				continue
			case 1:
				// Pure recipe — fine.
			case 2:
				if !types.Identical(results.At(1).Type(), errType) {
					addProblem("recipe #%d (%s) second return is %s; recipes must return T, (T, error), or (T, func(), error)",
						i+1, snippet(fset, rExpr), typeText(results.At(1).Type()))
					recipes = append(recipes, ri)
					continue
				}
				ri.errored = true
			case 3:
				// Resource-recipe shape: (T, func(), error). Accepted
				// in any Assemble-family call — pure (T, error) and
				// (T, func(), error) recipes mix freely. The chain
				// terminator (.Release / .NoRelease) decides what
				// happens with the cleanups.
				cleanupSig, isCleanupFn := results.At(1).Type().(*types.Signature)
				if !isCleanupFn || cleanupSig.Params().Len() != 0 || cleanupSig.Results().Len() != 0 {
					addProblem("recipe #%d (%s) second return is %s; for resource recipes the second return must be `func()`",
						i+1, snippet(fset, rExpr), typeText(results.At(1).Type()))
					recipes = append(recipes, ri)
					continue
				}
				if !types.Identical(results.At(2).Type(), errType) {
					addProblem("recipe #%d (%s) third return is %s; for resource recipes the third return must be the built-in `error`",
						i+1, snippet(fset, rExpr), typeText(results.At(2).Type()))
					recipes = append(recipes, ri)
					continue
				}
				ri.errored = true
				ri.isResource = true
			default:
				addProblem("recipe #%d (%s) returns %d values; recipes must return T, (T, error), or (T, func(), error)",
					i+1, snippet(fset, rExpr), results.Len())
				recipes = append(recipes, ri)
				continue
			}
			ri.output = results.At(0).Type()
			ri.outputKey = typeKey(ri.output)
			params := sig.Params()
			if sig.Variadic() {
				addProblem("recipe #%d (%s) is variadic; q.Assemble can't infer a fixed dep set for variadic inputs — wrap it in a fixed-arity adapter",
					i+1, snippet(fset, rExpr))
				recipes = append(recipes, ri)
				continue
			}
			for k := 0; k < params.Len(); k++ {
				pt := params.At(k).Type()
				ri.inputs = append(ri.inputs, pt)
				ri.inputKeys = append(ri.inputKeys, typeKey(pt))
			}
			ri.valid = true
		} else {
			ri.isValue = true
			ri.output = tv.Type
			ri.outputKey = typeKey(ri.output)
			ri.valid = true
		}

		recipes = append(recipes, ri)
	}

	// Build provider map. Group by output key so we can list ALL
	// duplicate clusters in one pass.
	providersByKey := map[string][]int{}
	for ridx, r := range recipes {
		if !r.valid {
			continue
		}
		providersByKey[r.outputKey] = append(providersByKey[r.outputKey], ridx)
	}
	providerOf := map[string]int{}
	dupKeys := make([]string, 0)
	for key, ridxs := range providersByKey {
		providerOf[key] = ridxs[0]
		if len(ridxs) > 1 {
			dupKeys = append(dupKeys, key)
		}
	}
	sort.Strings(dupKeys)
	for _, key := range dupKeys {
		// For q.AssembleAll, multiple recipes producing the target
		// type T is the success case (each contributes one element to
		// the result []T). Other duplicate keys (deps shared between
		// recipes) are still ambiguous and reported.
		if sc.Family == familyAssembleAll && key == sc.AssembleTargetKey {
			continue
		}
		ridxs := providersByKey[key]
		var labels []string
		for _, ridx := range ridxs {
			labels = append(labels, recipeLabel(fset, recipes[ridx]))
		}
		addProblem("duplicate provider for %s — recipes %s all produce it; pick one or define distinct named types per variant (e.g. `type PrimaryDB struct{ *DB }`) so each variant has its own provider key",
			typeText(recipes[ridxs[0]].output), strings.Join(labels, ", "))
	}

	// resolveInput finds the provider that satisfies a desired input
	// type. Two-step: exact-type first (covers concrete identity AND
	// any named-type brand variants), then assignability scan for
	// interface inputs.
	type inputResolution struct {
		resolvedKey string
		ambiguous   []int
		ok          bool
	}
	resolveInput := func(want types.Type, wantKey string) inputResolution {
		if _, ok := providerOf[wantKey]; ok {
			return inputResolution{resolvedKey: wantKey, ok: true}
		}
		_, isIface := want.Underlying().(*types.Interface)
		if !isIface {
			return inputResolution{}
		}
		var matches []int
		seenKey := map[string]bool{}
		for ridx, r := range recipes {
			if !r.valid || seenKey[r.outputKey] {
				continue
			}
			seenKey[r.outputKey] = true
			if types.AssignableTo(r.output, want) {
				matches = append(matches, ridx)
			}
		}
		switch len(matches) {
		case 0:
			return inputResolution{}
		case 1:
			return inputResolution{resolvedKey: recipes[matches[0]].outputKey, ok: true}
		default:
			return inputResolution{ambiguous: matches}
		}
	}

	// Resolve target T's provider(s).
	//
	// q.Assemble:    target T must have exactly one provider (exact
	//                type or single interface match). Zero or multiple
	//                providers → diagnostic.
	//
	// q.AssembleAll: target T can have any number of providers — every
	//                recipe whose output is assignable to T contributes
	//                one element to the result []T. Zero providers is
	//                still an error (the call would always return an
	//                empty slice with no useful work).
	if sc.Family == familyAssembleAll {
		var providerRidxs []int
		for ridx, r := range recipes {
			if !r.valid {
				continue
			}
			if types.AssignableTo(r.output, targetType) {
				providerRidxs = append(providerRidxs, ridx)
			}
		}
		if len(providerRidxs) == 0 {
			addProblem("target type %s has no providers — q.AssembleAll[T] needs at least one recipe whose output is assignable to T",
				sc.AssembleTargetTypeText)
		} else {
			sc.AssembleAllProviderRidxs = providerRidxs
		}
	} else if sc.Family == familyAssembleStruct {
		// Target T must be a struct type. Each field becomes a
		// separate dep target. resolveInput is reused per field; an
		// interface field with multiple providers is still flagged
		// as ambiguous (per-field "target ambiguity" diagnostic).
		st, ok := structUnderlying(targetType)
		if !ok {
			addProblem("target type %s is not a struct — q.AssembleStruct[T] requires T to be a struct type (use q.Assemble[T] for non-struct targets)",
				sc.AssembleTargetTypeText)
		} else if st.NumFields() == 0 {
			addProblem("target type %s has no fields — q.AssembleStruct[T] would always return the zero struct; use q.Assemble[T] or remove the call",
				sc.AssembleTargetTypeText)
		} else {
			fieldNames := make([]string, 0, st.NumFields())
			fieldKeys := make([]string, 0, st.NumFields())
			for i := 0; i < st.NumFields(); i++ {
				f := st.Field(i)
				if !f.Exported() && f.Pkg() != nil && f.Pkg().Path() != pkgPath {
					addProblem("field %s of %s is unexported in package %s — q.AssembleStruct[T] cannot set unexported fields from another package; either export the field or call q.AssembleStruct[T] from %s",
						f.Name(), sc.AssembleTargetTypeText, f.Pkg().Path(), f.Pkg().Path())
					continue
				}
				ft := f.Type()
				fk := typeKey(ft)
				res := resolveInput(ft, fk)
				if res.ok {
					fieldNames = append(fieldNames, f.Name())
					fieldKeys = append(fieldKeys, res.resolvedKey)
				} else if len(res.ambiguous) > 0 {
					var labels []string
					for _, ridx := range res.ambiguous {
						r := recipes[ridx]
						labels = append(labels, fmt.Sprintf("%s → %s", recipeLabel(fset, r), typeText(r.output)))
					}
					addProblem("field %s of %s (type %s) is satisfied by multiple providers: %s — narrow the recipe set or define distinct named types per variant",
						f.Name(), sc.AssembleTargetTypeText, typeText(ft), strings.Join(labels, ", "))
				} else {
					addProblem("field %s of %s (type %s) has no provider — add a recipe whose output is assignable to %s",
						f.Name(), sc.AssembleTargetTypeText, typeText(ft), typeText(ft))
				}
			}
			sc.AssembleStructFieldNames = fieldNames
			sc.AssembleStructFieldKeys = fieldKeys
		}
	} else {
		targetRes := resolveInput(targetType, sc.AssembleTargetKey)
		if !targetRes.ok && len(targetRes.ambiguous) == 0 {
			addProblem("target type %s is not produced by any recipe", sc.AssembleTargetTypeText)
		} else if len(targetRes.ambiguous) > 0 {
			var labels []string
			for _, ridx := range targetRes.ambiguous {
				r := recipes[ridx]
				labels = append(labels, fmt.Sprintf("%s → %s", recipeLabel(fset, r), typeText(r.output)))
			}
			addProblem("target interface %s is satisfied by multiple providers: %s — narrow the recipe set or define distinct named types per variant",
				sc.AssembleTargetTypeText, strings.Join(labels, ", "))
		} else {
			sc.AssembleTargetKey = targetRes.resolvedKey
		}
	}

	// Resolve every valid recipe's inputs.
	type missingInput struct {
		typeText string
		consumer []string
	}
	type ambiguousInput struct {
		typeText string
		consumer []string
		choices  []int
	}
	missingByKey := map[string]*missingInput{}
	ambiguousByKey := map[string]*ambiguousInput{}
	for ridx := range recipes {
		r := &recipes[ridx]
		if !r.valid {
			continue
		}
		r.resolvedInputKeys = make([]string, len(r.inputKeys))
		for k, ik := range r.inputKeys {
			res := resolveInput(r.inputs[k], ik)
			if res.ok {
				r.resolvedInputKeys[k] = res.resolvedKey
				continue
			}
			label := recipeLabel(fset, *r)
			if len(res.ambiguous) > 0 {
				ai := ambiguousByKey[ik]
				if ai == nil {
					ai = &ambiguousInput{typeText: typeText(r.inputs[k]), choices: res.ambiguous}
					ambiguousByKey[ik] = ai
				}
				ai.consumer = append(ai.consumer, label)
				continue
			}
			mi := missingByKey[ik]
			if mi == nil {
				mi = &missingInput{typeText: typeText(r.inputs[k])}
				missingByKey[ik] = mi
			}
			mi.consumer = append(mi.consumer, label)
		}
	}
	if len(missingByKey) > 0 {
		var missingKeys []string
		for k := range missingByKey {
			missingKeys = append(missingKeys, k)
		}
		sort.Strings(missingKeys)
		for _, k := range missingKeys {
			mi := missingByKey[k]
			addProblem("missing recipe for %s — needed by %s",
				mi.typeText, strings.Join(mi.consumer, ", "))
		}
	}
	if len(ambiguousByKey) > 0 {
		var keys []string
		for k := range ambiguousByKey {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			ai := ambiguousByKey[k]
			var choiceLabels []string
			for _, cidx := range ai.choices {
				cr := recipes[cidx]
				choiceLabels = append(choiceLabels, fmt.Sprintf("%s → %s", recipeLabel(fset, cr), typeText(cr.output)))
			}
			addProblem("interface input %s (needed by %s) is satisfied by multiple providers: %s — narrow the recipe set or define distinct named types per variant",
				ai.typeText, strings.Join(ai.consumer, ", "), strings.Join(choiceLabels, ", "))
		}
	}

	// Topo-sort via Kahn's algorithm — only over valid recipes whose
	// inputs all resolved.
	emitted := map[int]bool{}
	produced := map[string]bool{}
	var order []int
	validResolvedCount := 0
	for _, r := range recipes {
		if !r.valid {
			continue
		}
		allResolved := true
		for _, rk := range r.resolvedInputKeys {
			if rk == "" {
				allResolved = false
				break
			}
		}
		if allResolved {
			validResolvedCount++
		}
	}
	progressLoop := true
	for progressLoop && len(order) < validResolvedCount {
		progressLoop = false
		for ridx, r := range recipes {
			if !r.valid || emitted[ridx] {
				continue
			}
			ready := true
			for _, rk := range r.resolvedInputKeys {
				if rk == "" {
					ready = false
					break
				}
				if !produced[rk] {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			emitted[ridx] = true
			produced[r.outputKey] = true
			order = append(order, ridx)
			progressLoop = true
		}
	}
	var cycleRidx []int
	for ridx, r := range recipes {
		if !r.valid || emitted[ridx] {
			continue
		}
		blockedByMissing := false
		for _, rk := range r.resolvedInputKeys {
			if rk == "" {
				blockedByMissing = true
				break
			}
		}
		if blockedByMissing {
			continue
		}
		cycleRidx = append(cycleRidx, ridx)
	}
	if len(cycleRidx) > 0 {
		cyclePath := tracecycleAssemble(cycleRidx[0], recipes, providerOf)
		var arrows []string
		for _, ridx := range cyclePath {
			arrows = append(arrows, fmt.Sprintf("%s (%s)", typeText(recipes[ridx].output), recipeLabel(fset, recipes[ridx])))
		}
		if len(arrows) > 0 {
			arrows = append(arrows, arrows[0])
		}
		addProblem("dependency cycle: %s", strings.Join(arrows, " -> "))
	}

	// Walk the dep tree from T to find which recipes are actually
	// needed. Anything not reached → unused EXCEPT recipes that
	// provide context.Context — those are exempt because ctx is
	// expected to ride into the assembly purely for assembly-config
	// (debug, future hooks) even when no other recipe consumes it.
	// Also stash the ctx provider's outputKey on sc so the rewriter
	// can bind _qDbg from the corresponding _qDep<N>.
	ctxIface := findContextContextType(info)
	for _, r := range recipes {
		if !r.valid || ctxIface == nil {
			continue
		}
		if types.AssignableTo(r.output, ctxIface) {
			sc.AssembleCtxDepKey = r.outputKey
			break
		}
	}

	needed := map[int]bool{}
	var visit func(string)
	visit = func(key string) {
		ridx, ok := providerOf[key]
		if !ok || needed[ridx] {
			return
		}
		needed[ridx] = true
		for _, rk := range recipes[ridx].resolvedInputKeys {
			if rk != "" {
				visit(rk)
			}
		}
	}
	if sc.Family == familyAssembleAll {
		// Visit each provider recipe directly by ridx so multiple
		// recipes sharing an outputKey (interface case) all get
		// marked needed. Their transitive deps go through visit-by-
		// key, which is unambiguous because the dup check still
		// applies to non-target keys.
		for _, pridx := range sc.AssembleAllProviderRidxs {
			if needed[pridx] {
				continue
			}
			needed[pridx] = true
			for _, rk := range recipes[pridx].resolvedInputKeys {
				if rk != "" {
					visit(rk)
				}
			}
		}
	} else if sc.Family == familyAssembleStruct {
		for _, fk := range sc.AssembleStructFieldKeys {
			visit(fk)
		}
	} else if _, ok := providerOf[sc.AssembleTargetKey]; ok {
		visit(sc.AssembleTargetKey)
	}
	if len(problems) == 0 {
		var unused []string
		for ridx, r := range recipes {
			if !r.valid || needed[ridx] {
				continue
			}
			// Exempt context.Context — supplied for assembly-config
			// even if no consumer exists.
			if ctxIface != nil && types.AssignableTo(r.output, ctxIface) {
				continue
			}
			unused = append(unused, fmt.Sprintf("%s — provides %s",
				recipeLabel(fset, r), typeText(r.output)))
		}
		if len(unused) > 0 {
			sort.Strings(unused)
			tree := renderAssembleTreeForCall(sc, recipes, providerOf, typeText, fset)
			addProblem("unused recipe(s): %s\nThe target type %s requires:\n%s",
				strings.Join(unused, "; "), sc.AssembleTargetTypeText, tree)
		}
	}

	if len(problems) > 0 {
		showTree := false
		for _, p := range problems {
			if strings.HasPrefix(p, "missing recipe") || strings.HasPrefix(p, "target type") {
				showTree = true
				break
			}
		}
		var msg bytes.Buffer
		fmt.Fprintf(&msg, "q: %s[%s] cannot resolve the recipe graph:", familyLabel, sc.AssembleTargetTypeText)
		for _, p := range problems {
			fmt.Fprintf(&msg, "\n  - %s", p)
		}
		if showTree {
			tree := renderAssembleTreeForCall(sc, recipes, providerOf, typeText, fset)
			fmt.Fprintf(&msg, "\nWhat the resolver sees:\n%s", tree)
			fmt.Fprintf(&msg, "\nProviders supplied: %s", listProviders(recipes, typeText, fset))
		}
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  msg.String(),
		}, true
	}

	steps := make([]assembleStep, 0, len(order))
	for _, ridx := range order {
		r := recipes[ridx]
		steps = append(steps, assembleStep{
			RecipeIdx:       r.idx,
			IsValue:         r.isValue,
			Errored:         r.errored,
			IsResource:      r.isResource,
			AutoCleanup:     r.autoCleanup,
			InputKeys:       append([]string(nil), r.resolvedInputKeys...),
			OutputKey:       r.outputKey,
			OutputIsNilable: isNilableType(r.output),
			Label:           recipeLabel(fset, r),
		})
	}
	sc.AssembleSteps = steps
	return Diagnostic{}, false
}

// structUnderlying returns the *types.Struct view of t when t's
// underlying type is a struct (named or anonymous). When t is not a
// struct (named-of-non-struct, pointer, interface, etc.), returns
// (nil, false). q.AssembleStruct rejects non-struct targets via
// this check.
func structUnderlying(t types.Type) (*types.Struct, bool) {
	if t == nil {
		return nil, false
	}
	st, ok := t.Underlying().(*types.Struct)
	return st, ok
}

// findContextContextType returns the *types.Interface for
// `context.Context` if it appears anywhere in the typecheck info's
// resolved expression types — i.e. when some recipe input or value
// references context.Context, the importer has loaded the context
// package and the interface is available for assignability checks.
// Returns nil when no expression in the call site involves
// context.Context (e.g. an assembly with no ctx-aware recipes).
//
// Implementation note: we scan info.Types for the named type
// `context.Context` rather than triggering a fresh import, because
// the resolver's *types.Info is the only contextually-correct
// source — the importer used by checkErrorSlotsWithInfo isn't
// reachable from here. This is fast for typical recipe sets.
func findContextContextType(info *types.Info) types.Type {
	for _, tv := range info.Types {
		if tv.Type == nil {
			continue
		}
		named, ok := tv.Type.(*types.Named)
		if !ok {
			continue
		}
		obj := named.Obj()
		if obj == nil || obj.Pkg() == nil {
			continue
		}
		if obj.Name() == "Context" && obj.Pkg().Path() == "context" {
			return named
		}
	}
	return nil
}

// isNilableType reports whether t can hold a nil value at runtime.
// Pointer, interface, slice, map, chan, and function types are
// nilable; struct values, basic types, and arrays are not.
func isNilableType(t types.Type) bool {
	if t == nil {
		return false
	}
	switch t.Underlying().(type) {
	case *types.Pointer, *types.Interface, *types.Slice,
		*types.Map, *types.Chan, *types.Signature:
		return true
	}
	return false
}

// recipeLabel renders a recipe's diagnostic label as
// "#%d (snippet)" with 1-based numbering matching the order they
// appeared in the recipe list.
func recipeLabel(fset *token.FileSet, r assembleRecipeInfo) string {
	return fmt.Sprintf("#%d (%s)", r.idx+1, snippet(fset, r.expr))
}

// tracecycleAssemble walks the unresolved recipe graph until it
// revisits an already-seen recipe — that revisit IS the cycle.
// Returns the cycle as ordered recipe indices (open — caller closes).
func tracecycleAssemble(start int, recipes []assembleRecipeInfo, providerOf map[string]int) []int {
	seen := map[int]int{}
	var path []int
	cur := start
	for {
		if pos, ok := seen[cur]; ok {
			return path[pos:]
		}
		seen[cur] = len(path)
		path = append(path, cur)
		r := recipes[cur]
		var next int
		found := false
		for _, ik := range r.inputKeys {
			if pidx, ok := providerOf[ik]; ok {
				next = pidx
				found = true
				break
			}
		}
		if !found {
			return path
		}
		cur = next
	}
}

// renderAssembleTree formats the dependency tree rooted at T, using
// the providerOf map to resolve each input. Missing inputs are marked
// "??". Cycles are detected via a visit-set and rendered "(cycle)".
func renderAssembleTree(targetKey string, recipes []assembleRecipeInfo, providerOf map[string]int, typeText func(types.Type) string, fset *token.FileSet) string {
	var b bytes.Buffer
	visiting := map[int]bool{}
	var walk func(key string, depth int, label string)
	walk = func(key string, depth int, label string) {
		indent := strings.Repeat("  ", depth)
		ridx, ok := providerOf[key]
		if !ok {
			fmt.Fprintf(&b, "%s- %s ?? (no recipe provides this)\n", indent, label)
			return
		}
		r := recipes[ridx]
		var marker string
		switch {
		case r.isValue:
			marker = "value"
		case r.errored:
			marker = "(T, error)"
		default:
			marker = "fn"
		}
		fmt.Fprintf(&b, "%s- %s <- %s [%s]\n", indent, typeText(r.output), recipeLabel(fset, r), marker)
		if visiting[ridx] {
			fmt.Fprintf(&b, "%s  (cycle)\n", indent)
			return
		}
		visiting[ridx] = true
		for k, ik := range r.inputKeys {
			lookupKey := ik
			if k < len(r.resolvedInputKeys) && r.resolvedInputKeys[k] != "" {
				lookupKey = r.resolvedInputKeys[k]
			}
			walk(lookupKey, depth+1, fmt.Sprintf("input %s", typeText(r.inputs[k])))
		}
		delete(visiting, ridx)
	}
	rootRidx, ok := providerOf[targetKey]
	if ok {
		walk(targetKey, 0, typeText(recipes[rootRidx].output))
	} else {
		fmt.Fprintf(&b, "- target ?? (no recipe provides T)\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderAssembleTreeForCall renders the dep tree(s) appropriate for
// the call's family. q.Assemble has a single root (T's provider);
// q.AssembleAll has one root per target provider — each rendered as
// its own sub-tree so the user can see how each contributing element
// is built.
func renderAssembleTreeForCall(sc *qSubCall, recipes []assembleRecipeInfo, providerOf map[string]int, typeText func(types.Type) string, fset *token.FileSet) string {
	switch sc.Family {
	case familyAssembleAll:
		if len(sc.AssembleAllProviderRidxs) == 0 {
			return "- target ?? (no recipe provides T or anything assignable to T)"
		}
		var b bytes.Buffer
		for i, pridx := range sc.AssembleAllProviderRidxs {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(renderAssembleTree(recipes[pridx].outputKey, recipes, providerOf, typeText, fset))
		}
		return b.String()
	case familyAssembleStruct:
		if len(sc.AssembleStructFieldKeys) == 0 {
			return "- target ?? (no struct fields resolved)"
		}
		var b bytes.Buffer
		for i, fk := range sc.AssembleStructFieldKeys {
			if i > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "field %s:\n", sc.AssembleStructFieldNames[i])
			b.WriteString(renderAssembleTree(fk, recipes, providerOf, typeText, fset))
		}
		return b.String()
	default:
		return renderAssembleTree(sc.AssembleTargetKey, recipes, providerOf, typeText, fset)
	}
}

// listProviders returns a one-line summary of every valid provider
// for the diagnostic Providers-supplied line.
func listProviders(recipes []assembleRecipeInfo, typeText func(types.Type) string, fset *token.FileSet) string {
	var parts []string
	for _, r := range recipes {
		if !r.valid {
			continue
		}
		parts = append(parts, fmt.Sprintf("#%d→%s", r.idx+1, typeText(r.output)))
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, ", ")
}

// snippet renders a recipe expression as a short label for use in
// diagnostic messages. Uses go/printer rather than slicing source
// bytes — the typecheck pass runs before the rewriter has the source
// buffer, so exprText would crash on nil src. Truncates to 60 chars.
func snippet(fset *token.FileSet, e ast.Expr) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, e); err != nil {
		return "<expr>"
	}
	s := b.String()
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 60 {
		s = s[:57] + "..."
	}
	return s
}

// typeKey returns a stable canonical string for a types.Type, suitable
// for use as a map key when checking provider equivalence and dep
// resolution. Uses the fully-qualified package path form so two
// identically-named types from different packages don't collide.
func typeKey(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string {
		if p == nil {
			return ""
		}
		return p.Path()
	})
}

// buildAssembleReplacement emits the IIFE for q.Assemble or
// q.AssembleAll. Returns (text, fmtUsed) — fmtUsed is true when the
// body emits any fmt.Errorf call (currently from the runtime nil-
// check).
//
// q.Assemble shape — IIFE returns (T, error):
//
//	(func() (T, error) {
//	    _qDep1, _qAErr1 := newConfig()
//	    if _qAErr1 != nil { return *new(T), _qAErr1 }
//	    if _qDep1 == nil { return *new(T), fmt.Errorf("...: %w", q.ErrNil) }
//	    _qDep2 := newDB(_qDep1)
//	    if _qDep2 == nil { return *new(T), fmt.Errorf("...: %w", q.ErrNil) }
//	    return _qDep2, nil
//	}())
//
// q.AssembleAll shape — IIFE returns ([]T, error); each step builds
// the same way; the final return collects every target provider's
// _qDep<N> into a []T literal in recipe declaration order:
//
//	(func() ([]T, error) {
//	    _qDep0 := newAuth()
//	    _qDep1 := newLogging()
//	    _qDep2 := newMetrics()
//	    return []T{_qDep0, _qDep1, _qDep2}, nil
//	}())
//
// When debug tracing is enabled via q.WithAssemblyDebug on a ctx
// recipe, each step prints a trace line to the registered writer
// before invoking the recipe.
func buildAssembleReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string, alias string) (string, bool) {
	elemText := assembleTargetText(sub)
	returnText := elemText
	if sub.Family == familyAssembleAll {
		returnText = "[]" + elemText
	}
	body, fmtUsed := buildAssembleBody(fset, src, sub, subs, subTexts, returnText, alias)
	// IIFE always returns (T, func(), error). For .NoRelease() this
	// is the user-facing shape directly. For .Release() the rewriter
	// wraps it with bind+defer in renderAssembleRelease (block emit).
	return fmt.Sprintf("(func() (%s, func(), error) {%s\n}())", returnText, body), fmtUsed
}

// buildAssembleSubText returns the text to substitute at the chain
// call expression's source span. Branch on chain terminator:
//   - .NoRelease() — the IIFE itself (returns (T, func(), error)).
//   - .Release()   — a (T, error)-shaped placeholder expression that
//                    references the temps bound by the pre-statement
//                    block. The pre-statement block is emitted
//                    separately via buildAssembleReleaseBlock.
func buildAssembleSubText(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string, alias string, counter int) string {
	text, _ := buildAssembleReplacement(fset, src, sub, subs, subTexts, alias)
	if sub.AssembleChain != assembleChainRelease {
		return text
	}
	// For .Release(), the pre-statement block already bound:
	//   _qDep<N>, _qShutdown<N>, _qAErr<N> := <IIFE>
	// At the original call site we need a (T, error)-shaped value.
	// Emit a tiny lambda that returns the cached temps — works in
	// any expression position including assignment, return, function
	// arg, etc.
	returnText := assembleTargetText(sub)
	if sub.Family == familyAssembleAll {
		returnText = "[]" + returnText
	}
	depVar := fmt.Sprintf("_qADep%d", counter)
	errVar := fmt.Sprintf("_qAErr%d", counter)
	return fmt.Sprintf("(func() (%s, error) { return %s, %s })()", returnText, depVar, errVar)
}

// buildAssembleReleaseBlock emits the pre-statement block injected
// into the enclosing function for q.Assemble[T](...).Release(). The
// block binds the IIFE result to caller-scope temps and defers the
// shutdown closure so it fires when the enclosing function returns.
//
//   _qADep<N>, _qAShut<N>, _qAErr<N> := <IIFE returning (T, func(), error)>
//   defer _qAShut<N>()
//
// On the IIFE's success path, _qAShut<N> is sync.OnceFunc-wrapped so
// firing it via this defer is safe even if the user also calls it
// manually (e.g. wires it to context.AfterFunc).
func buildAssembleReleaseBlock(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string, alias string, counter int) string {
	text, _ := buildAssembleReplacement(fset, src, sub, subs, subTexts, alias)
	depVar := fmt.Sprintf("_qADep%d", counter)
	shutVar := fmt.Sprintf("_qAShut%d", counter)
	errVar := fmt.Sprintf("_qAErr%d", counter)
	return fmt.Sprintf("%s, %s, %s := %s\n\tdefer %s()", depVar, shutVar, errVar, text, shutVar)
}

// assembleTargetText returns the spelling of the target type T, with
// `any` as a fallback when typecheck couldn't resolve it.
func assembleTargetText(sub qSubCall) string {
	if sub.AssembleTargetTypeText != "" {
		return sub.AssembleTargetTypeText
	}
	return "any"
}

// buildAssembleBody builds the IIFE body lines (no enclosing braces).
// Returned text starts with a newline and is indented one tab past
// the IIFE opening brace.
//
// Per step the body contains, in order:
//
//  1. The bind line: pure recipes get one LHS (`_qDep<N> := call(...)`),
//     errored recipes get two (`_qDep<N>, _qAErr<N> := call(...)`).
//  2. For errored recipes: `if _qAErr<N> != nil { return ..., _qAErr<N> }`.
//  3. If output is nilable (pointer / interface / slice / map / chan /
//     func): runtime nil-check on _qDep<N>:
//       `if _qDep<N> == nil { return *new(T), fmt.Errorf("...: %w", q.ErrNil) }`.
//     Catches the typed-nil-interface pitfall before _qDep<N> flows
//     into a consumer recipe via implicit interface conversion.
//
// For q.AssembleCtx, an optional debug-trace prelude (`if _qDbg :=
// q.AssemblyDebugWriter(_qDepCtx); _qDbg != nil { ... }`) is emitted
// once at the top, then per step. The debug helper returns nil when
// q.WithAssemblyDebug wasn't called on the ctx, so the no-debug path
// is one ctx.Value lookup per call.
func buildAssembleBody(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string, targetText, alias string) (string, bool) {
	var b bytes.Buffer
	depVar := map[string]string{}
	depByRecipe := map[int]string{}
	familyName := assembleFamilyLabel(sub.Family)
	fmtUsed := false

	// All Assemble-family IIFEs return (T, func(), error). Cleanups
	// from resource recipes (or auto-cleanup-from-T) are pushed onto
	// _qCleanups in topo order; the IIFE wraps the reverse-iteration
	// in sync.OnceFunc on the success path and returns it as the
	// shutdown closure. On error/nil-check failure the IIFE fires
	// the partial cleanups in-place and returns a no-op closure.
	fmt.Fprintf(&b, "\n\tvar _qCleanups []func()")
	zeroExpr := fmt.Sprintf("*new(%s)", targetText)
	fireAllInline := "for _qI := len(_qCleanups)-1; _qI >= 0; _qI-- { _qCleanups[_qI]() }"
	errReturn := func(errExpr string) string {
		return fmt.Sprintf("%s; return %s, func(){}, %s", fireAllInline, zeroExpr, errExpr)
	}

	// Find the ctx step (idx == -1, only present for AssembleCtx) so
	// the debug-trace prelude can reference its _qDep<N> name.
	ctxDepName := ""

	for n, step := range sub.AssembleSteps {
		recipeExpr := sub.AssembleRecipes[step.RecipeIdx]
		recipeText := exprTextSubst(fset, src, recipeExpr, subs, subTexts)

		var callText string
		if step.IsValue {
			callText = recipeText
		} else {
			args := make([]string, len(step.InputKeys))
			for k, ik := range step.InputKeys {
				args[k] = depVar[ik]
			}
			callText = fmt.Sprintf("%s(%s)", recipeText, strings.Join(args, ", "))
		}

		dep := fmt.Sprintf("_qDep%d", n)
		depVar[step.OutputKey] = dep
		depByRecipe[step.RecipeIdx] = dep

		// Optional pre-call trace prelude.
		if ctxDepName != "" {
			fmt.Fprintf(&b, "\n\tif _qDbg != nil { fmt.Fprintf(_qDbg, %q, %q, %q) }",
				"[%s] step %s\n", familyName, step.Label)
			fmtUsed = true
		}

		switch {
		case step.IsResource:
			// (T, func(), error) recipe shape — bind all three;
			// push the cleanup before any nil-check so partial
			// failure auto-cleans the now-allocated resource too.
			cleanupVar := fmt.Sprintf("_qCleanup%d", n)
			errVar := fmt.Sprintf("_qAErr%d", n)
			fmt.Fprintf(&b, "\n\t%s, %s, %s := %s", dep, cleanupVar, errVar, callText)
			fmt.Fprintf(&b, "\n\tif %s != nil { %s }", errVar, errReturn(errVar))
			fmt.Fprintf(&b, "\n\tif %s != nil { _qCleanups = append(_qCleanups, %s) }", cleanupVar, cleanupVar)
		case step.Errored:
			errVar := fmt.Sprintf("_qAErr%d", n)
			fmt.Fprintf(&b, "\n\t%s, %s := %s", dep, errVar, callText)
			fmt.Fprintf(&b, "\n\tif %s != nil { %s }", errVar, errReturn(errVar))
		default:
			fmt.Fprintf(&b, "\n\t%s := %s", dep, callText)
		}

		if sub.AssembleCtxDepKey != "" && step.OutputKey == sub.AssembleCtxDepKey {
			ctxDepName = dep
			fmt.Fprintf(&b, "\n\t_qDbg := %s.AssemblyDebugWriter(%s)", alias, dep)
			fmt.Fprintf(&b, "\n\tif _qDbg != nil { fmt.Fprintf(_qDbg, %q, %q) }",
				"[%s] ctx provided\n", familyName)
			fmtUsed = true
		}

		if step.OutputIsNilable {
			label := step.Label
			if label == "" {
				label = fmt.Sprintf("#%d", step.RecipeIdx+1)
			}
			msg := fmt.Sprintf(`%s: recipe %s returned nil`, familyName, label)
			nilErr := fmt.Sprintf("fmt.Errorf(%q + %q, %s.ErrNil)", msg, ": %w", alias)
			fmt.Fprintf(&b, "\n\tif %s == nil { %s }", dep, errReturn(nilErr))
			fmtUsed = true
		}
	}

	successShutdown := fmt.Sprintf("sync.OnceFunc(func() { %s })", fireAllInline)

	if sub.Family == familyAssembleAll {
		parts := make([]string, 0, len(sub.AssembleAllProviderRidxs))
		for _, ridx := range sub.AssembleAllProviderRidxs {
			if dep, ok := depByRecipe[ridx]; ok {
				parts = append(parts, dep)
			}
		}
		fmt.Fprintf(&b, "\n\treturn %s{%s}, %s, nil", targetText, strings.Join(parts, ", "), successShutdown)
		return b.String(), fmtUsed
	}

	if sub.Family == familyAssembleStruct {
		parts := make([]string, 0, len(sub.AssembleStructFieldKeys))
		for i, fk := range sub.AssembleStructFieldKeys {
			if dep, ok := depVar[fk]; ok {
				parts = append(parts, fmt.Sprintf("%s: %s", sub.AssembleStructFieldNames[i], dep))
			}
		}
		fmt.Fprintf(&b, "\n\treturn %s{%s}, %s, nil", targetText, strings.Join(parts, ", "), successShutdown)
		return b.String(), fmtUsed
	}

	targetDep, ok := depVar[sub.AssembleTargetKey]
	if !ok {
		targetDep = fmt.Sprintf("*new(%s)", targetText)
	}
	fmt.Fprintf(&b, "\n\treturn %s, %s, nil", targetDep, successShutdown)
	return b.String(), fmtUsed
}

// assembleHasNilableStep reports whether any step has a nilable
// output. Used by the rewriter to flag fmtUsed for the in-place
// dispatch (the runtime nil-check emits fmt.Errorf).
func assembleHasNilableStep(sub qSubCall) bool {
	for _, s := range sub.AssembleSteps {
		if s.OutputIsNilable {
			return true
		}
	}
	return false
}
