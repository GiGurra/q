package preprocessor

// assemble.go — typecheck + rewriter for the q.Assemble family
// (q.Assemble[T], q.AssembleErr[T], q.AssembleE[T].<Method>(...)).
//
// Pipeline per call site:
//
//   1. Scanner captures the recipe expressions onto sc.AssembleRecipes
//      and the target type [T] onto sc.AsType. (See scanner.go.)
//   2. resolveAssemble (typecheck pass) reads each recipe's resolved
//      type via go/types, classifies it as a function reference
//      (*types.Signature) or an inline value, builds a provider map
//      keyed by go/types' canonical type-string, topo-sorts via Kahn's
//      algorithm, and emits a single multi-line diagnostic combining
//      every problem found (missing dep / duplicate provider / cycle /
//      unused recipe / unsatisfiable target / wrong recipe shape /
//      errored recipes in pure q.Assemble).
//   3. The rewriter emits an IIFE that calls the recipes in topo order
//      with each dep flowing through a `_qDep<N>` temporary; errored
//      recipes get a `_qAErr<N>` slot and a bubble-on-failure check
//      that returns the IIFE's zero plus the captured error.
//
// For q.Assemble (pure) the IIFE returns T. For q.AssembleErr the
// IIFE returns (T, error) so it composes naturally with q.Try. For
// q.AssembleE the same (T, error)-returning IIFE feeds a TryE-style
// chain dispatch (Wrap / Wrapf / Err / ErrF / Catch) at the outer
// call's bind site.
//
// Diagnostic philosophy. Auto-DI fails differently from a typo: the
// graph is large enough that "first error wins" leaves the user
// guessing whether fixing #1 will surface five more. resolveAssemble
// instead validates everything it can in one pass and emits one
// diagnostic that lists EVERY problem, plus context: who needs the
// missing type, what providers exist, the cycle's edges, and the
// satisfied-vs-unused split. The user fixes once and reruns.

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

// assembleRecipeInfo holds resolved type info for one recipe argument
// of a q.Assemble family call. Built in original-arg order by
// resolveAssemble; reused by the diagnostic helpers (cycle trace,
// dep-tree render, provider list) so they share the same view of the
// graph.
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
type assembleRecipeInfo struct {
	idx               int
	expr              ast.Expr
	isValue           bool
	errored           bool
	inputs            []types.Type
	inputKeys         []string
	resolvedInputKeys []string
	output            types.Type
	outputKey         string
	valid             bool
}

// isAssembleFamily reports whether sc's family is one of the q.Assemble
// entries. Used by the typecheck dispatcher to pick resolveAssemble.
func isAssembleFamily(f family) bool {
	switch f {
	case familyAssemble, familyAssembleErr, familyAssembleE:
		return true
	}
	return false
}

// assembleFamilyLabel is the user-facing helper name for diagnostics.
func assembleFamilyLabel(f family) string {
	switch f {
	case familyAssemble:
		return "q.Assemble"
	case familyAssembleErr:
		return "q.AssembleErr"
	case familyAssembleE:
		return "q.AssembleE"
	}
	return "q.Assemble<?>"
}

// resolveAssemble validates one q.Assemble call site and populates
// sc.AssembleSteps + sc.AssembleTargetTypeText + sc.AssembleTargetKey
// on success. On failure, returns one diagnostic that lists EVERY
// problem found (recipe-shape errors, errored recipes in pure
// q.Assemble, duplicate providers, missing target, missing inputs,
// dependency cycles, unused recipes), with enough context that the
// user can fix all of them in one round trip.
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
				addProblem("recipe #%d (%s) returns no values — recipes must return T or (T, error)",
					i+1, snippet(fset, rExpr))
				recipes = append(recipes, ri)
				continue
			case 1:
				// Pure recipe — fine.
			case 2:
				if !types.Identical(results.At(1).Type(), errType) {
					addProblem("recipe #%d (%s) second return is %s; recipes must return T or (T, error) where the second value is the built-in `error`",
						i+1, snippet(fset, rExpr), typeText(results.At(1).Type()))
					recipes = append(recipes, ri)
					continue
				}
				ri.errored = true
			default:
				addProblem("recipe #%d (%s) returns %d values; recipes must return T or (T, error)",
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

	// Pure q.Assemble forbids errored recipes — there's no error path
	// to bubble.
	if sc.Family == familyAssemble {
		for _, r := range recipes {
			if r.valid && r.errored {
				addProblem("recipe #%d (%s) returns (%s, error); q.Assemble has no error path — use q.AssembleErr or q.AssembleE",
					r.idx+1, snippet(fset, r.expr), typeText(r.output))
			}
		}
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
	// Use first valid provider per key as the canonical map for graph
	// resolution; duplicates are reported separately.
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
		ridxs := providersByKey[key]
		var labels []string
		for _, ridx := range ridxs {
			labels = append(labels, fmt.Sprintf("#%d (%s)", recipes[ridx].idx+1, snippet(fset, recipes[ridx].expr)))
		}
		addProblem("duplicate provider for %s — recipes %s all produce it; pick one or use q.Tagged to brand the variants",
			typeText(recipes[ridxs[0]].output), strings.Join(labels, ", "))
	}

	// resolveInput finds the provider that satisfies a desired input
	// type. Two-step:
	//   1. Exact-type match via providerOf — fastest, also disambiguates
	//      tagged services (Tagged[*DB, _primary] is a distinct struct,
	//      not assignable from plain *DB).
	//   2. Assignability scan — for interface-typed inputs, walk every
	//      provider type-key and check types.AssignableTo. Returns the
	//      single matching provider's outputKey, or signals ambiguity
	//      when multiple distinct provider types satisfy.
	//
	// Returns:
	//   resolvedKey — the provider's outputKey (used by the rewriter
	//                 to look up _qDep<N>).
	//   ambiguous   — when len > 1, the ridxs of competing providers.
	//   ok          — true on a unique match.
	type inputResolution struct {
		resolvedKey string
		ambiguous   []int
		ok          bool
	}
	resolveInput := func(want types.Type, wantKey string) inputResolution {
		// 1. Exact-type match (covers concrete identity AND tagged
		//    services where the brand makes the type distinct).
		if _, ok := providerOf[wantKey]; ok {
			return inputResolution{resolvedKey: wantKey, ok: true}
		}
		// 2. Assignability scan. Walk one ridx per type-key cluster.
		//    Skip non-interface wants entirely — the only way a concrete
		//    type T2 satisfies a concrete T1 (T1 != T2) is via implicit
		//    conversion, and Go's assembler-input position requires
		//    interface assignability. (This also keeps the search cheap.)
		_, isIface := want.Underlying().(*types.Interface)
		if !isIface {
			return inputResolution{}
		}
		var matches []int
		seenKey := map[string]bool{}
		for _, r := range recipes {
			if !r.valid || seenKey[r.outputKey] {
				continue
			}
			seenKey[r.outputKey] = true
			if types.AssignableTo(r.output, want) {
				matches = append(matches, r.idx)
			}
		}
		switch len(matches) {
		case 0:
			return inputResolution{}
		case 1:
			// Map back from r.idx (original arg index) to a recipe in
			// `recipes` (same slice indexing — recipes[i].idx == i).
			return inputResolution{resolvedKey: recipes[matches[0]].outputKey, ok: true}
		default:
			return inputResolution{ambiguous: matches}
		}
	}

	// Resolve target T's provider.
	targetRes := resolveInput(targetType, sc.AssembleTargetKey)
	if !targetRes.ok && len(targetRes.ambiguous) == 0 {
		addProblem("target type %s is not produced by any recipe", sc.AssembleTargetTypeText)
	} else if len(targetRes.ambiguous) > 0 {
		var labels []string
		for _, ridx := range targetRes.ambiguous {
			r := recipes[ridx]
			labels = append(labels, fmt.Sprintf("#%d (%s) → %s", r.idx+1, snippet(fset, r.expr), typeText(r.output)))
		}
		addProblem("target interface %s is satisfied by multiple providers: %s — narrow the recipe set or use q.Tagged to disambiguate",
			sc.AssembleTargetTypeText, strings.Join(labels, ", "))
	} else {
		// Stash the resolved target key so the rewriter looks up the
		// concrete provider's _qDep<N>.
		sc.AssembleTargetKey = targetRes.resolvedKey
	}

	// Resolve every valid recipe's inputs. Build resolvedInputKeys in
	// place (used by the topo sort and the emit stage). Group missing
	// / ambiguous inputs by type so the user sees one entry per
	// problematic type.
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
			label := fmt.Sprintf("#%d (%s)", r.idx+1, snippet(fset, r.expr))
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
				choiceLabels = append(choiceLabels, fmt.Sprintf("#%d (%s) → %s", cr.idx+1, snippet(fset, cr.expr), typeText(cr.output)))
			}
			addProblem("interface input %s (needed by %s) is satisfied by multiple providers: %s — narrow the recipe set or use q.Tagged to disambiguate",
				ai.typeText, strings.Join(ai.consumer, ", "), strings.Join(choiceLabels, ", "))
		}
	}

	// Topo-sort via Kahn's algorithm — only over valid recipes whose
	// inputs all resolved. Operates on resolvedInputKeys so interface
	// inputs route to their concrete provider's outputKey.
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
					// Missing or ambiguous input — already reported.
					// Skip the topo entry so unrelated cycles surface
					// rather than this recipe being mis-classified.
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
	// Anything still unemitted with all-resolvable inputs is a cycle.
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
		// Find the actual cycle path. Pick any unemitted recipe and
		// walk its inputs through providerOf until we revisit it.
		cyclePath := tracecycleAssemble(cycleRidx[0], recipes, providerOf)
		var arrows []string
		for _, ridx := range cyclePath {
			arrows = append(arrows, fmt.Sprintf("%s (#%d)", typeText(recipes[ridx].output), recipes[ridx].idx+1))
		}
		// Close the cycle visually.
		if len(arrows) > 0 {
			arrows = append(arrows, arrows[0])
		}
		addProblem("dependency cycle: %s", strings.Join(arrows, " -> "))
	}

	// Walk the dep tree from T to find which recipes are actually
	// needed. Uses resolvedInputKeys so interface inputs route to
	// their concrete provider's outputKey — without this the walker
	// would mis-classify the concrete provider as "unused" simply
	// because its outputKey doesn't textually match the consumer's
	// input type-key.
	needed := map[int]bool{}
	if _, ok := providerOf[sc.AssembleTargetKey]; ok {
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
		visit(sc.AssembleTargetKey)
	}
	if len(problems) == 0 {
		// Only complain about unused recipes when the rest of the
		// graph is healthy — otherwise the user gets a confusing
		// "you supplied too many" alongside "you supplied too few".
		var unused []string
		for ridx, r := range recipes {
			if !r.valid || needed[ridx] {
				continue
			}
			unused = append(unused, fmt.Sprintf("#%d (%s) — provides %s",
				r.idx+1, snippet(fset, r.expr), typeText(r.output)))
		}
		if len(unused) > 0 {
			sort.Strings(unused)
			tree := renderAssembleTree(sc.AssembleTargetKey, recipes, providerOf, typeText)
			addProblem("unused recipe(s): %s\nThe target type %s requires:\n%s",
				strings.Join(unused, "; "), sc.AssembleTargetTypeText, tree)
		}
	}

	if len(problems) > 0 {
		// Always include the dep tree (or what we have of it) when
		// the missing-recipe / target-missing problems are present —
		// it grounds the user in what the resolver can see.
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
			tree := renderAssembleTree(sc.AssembleTargetKey, recipes, providerOf, typeText)
			fmt.Fprintf(&msg, "\nWhat the resolver sees:\n%s", tree)
			fmt.Fprintf(&msg, "\nProviders supplied: %s", listProviders(recipes, typeText))
		}
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  msg.String(),
		}, true
	}

	// Build the topo-ordered AssembleSteps slice. InputKeys carries
	// the *resolved* provider keys (so interface inputs route to their
	// concrete provider's _qDep<N>). OutputIsNilable + Label drive the
	// runtime nil-check the rewriter emits after each step.
	steps := make([]assembleStep, 0, len(order))
	for _, ridx := range order {
		r := recipes[ridx]
		steps = append(steps, assembleStep{
			RecipeIdx:       r.idx,
			IsValue:         r.isValue,
			Errored:         r.errored,
			InputKeys:       append([]string(nil), r.resolvedInputKeys...),
			OutputKey:       r.outputKey,
			OutputIsNilable: isNilableType(r.output),
			Label:           fmt.Sprintf("#%d (%s)", r.idx+1, snippet(fset, r.expr)),
		})
	}
	sc.AssembleSteps = steps
	return Diagnostic{}, false
}

// isNilableType reports whether t can hold a nil value at runtime.
// Pointer, interface, slice, map, chan, and function types are nilable
// in Go; struct values, basic types, and arrays are not. The rewriter
// uses this to decide whether to emit a runtime nil-check after a
// recipe call — and where there's no nilable possibility, the check
// is skipped to keep the IIFE tight.
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

// tracecycleAssemble walks the unresolved recipe graph from a known
// non-emitted recipe back through its dependency chain until it
// revisits an already-seen recipe — that point IS the cycle. Returns
// the cycle as an ordered slice of recipe indices (not closed —
// caller renders the closing arrow).
//
// The walk picks an arbitrary first-input each step. For multi-input
// cycles this may produce one of several valid renderings, but each
// is a real dependency loop.
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
		// Walk to the first input whose provider is in the
		// unresolved set (i.e. another node on the cycle).
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
			// Shouldn't happen for a cycle node, but avoid infinite
			// loop.
			return path
		}
		cur = next
	}
}

// renderAssembleTree formats the dependency tree rooted at T, using
// the providerOf map to resolve each input. Missing inputs are marked
// with "??". Used in diagnostics so the user can see at a glance what
// the resolver believes the graph looks like.
//
// Indents children with two spaces per depth level; cycles are
// detected via a visit-set and rendered as "(cycle)" so the tree
// stays finite.
func renderAssembleTree(targetKey string, recipes []assembleRecipeInfo, providerOf map[string]int, typeText func(types.Type) string) string {
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
		if r.isValue {
			marker = "value"
		} else if r.errored {
			marker = "(T, error)"
		} else {
			marker = "fn"
		}
		fmt.Fprintf(&b, "%s- %s <- recipe #%d [%s]\n", indent, typeText(r.output), r.idx+1, marker)
		if visiting[ridx] {
			fmt.Fprintf(&b, "%s  (cycle)\n", indent)
			return
		}
		visiting[ridx] = true
		for k, ik := range r.inputKeys {
			// Prefer the resolved key so the tree shows the *actual*
			// provider chain (interface inputs route to their concrete
			// provider's subtree). Falls back to the literal input
			// key when resolution failed — that path renders the "??
			// (no recipe provides this)" leaf.
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

// listProviders returns a one-line summary of every valid provider in
// the recipe set, used in diagnostics to ground the user in what
// they actually supplied.
func listProviders(recipes []assembleRecipeInfo, typeText func(types.Type) string) string {
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
	// Compress whitespace runs so multi-line expressions render on one
	// line.
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

// buildAssembleReplacement emits the IIFE replacement for q.Assemble
// (pure) and q.AssembleErr. Returns (text, fmtUsed) — fmtUsed is true
// when the body emits any fmt.Errorf call (currently from the
// runtime nil-check on errored / chain forms; pure q.Assemble panics
// on nil with a plain string and uses no fmt).
//
// Pure shape (q.Assemble):
//
//	(func() T {
//	    _qDep0 := newConfig()
//	    if _qDep0 == nil { panic("q.Assemble: recipe ... returned nil") }
//	    _qDep1 := newDB(_qDep0)
//	    if _qDep1 == nil { panic("q.Assemble: recipe ... returned nil") }
//	    return _qDep1
//	}())
//
// Errored shape (q.AssembleErr):
//
//	(func() (T, error) {
//	    _qDep0, _qAErr0 := newConfig()
//	    if _qAErr0 != nil { return *new(T), _qAErr0 }
//	    if _qDep0 == nil { return *new(T), fmt.Errorf("...: %w", q.ErrNil) }
//	    _qDep1, _qAErr1 := newDB(_qDep0)
//	    ...
//	    return _qDep1, nil
//	}())
func buildAssembleReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string, alias string) (string, bool) {
	t := assembleTargetText(sub)
	body, fmtUsed := buildAssembleBody(fset, src, sub, subs, subTexts, t, alias)
	if sub.Family == familyAssembleErr {
		return fmt.Sprintf("(func() (%s, error) {%s\n}())", t, body), fmtUsed
	}
	return fmt.Sprintf("(func() %s {%s\n}())", t, body), fmtUsed
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
// the IIFE opening brace. fmtUsed reports whether the body references
// fmt.Errorf — the caller propagates it so the import-injection pass
// adds `fmt` to the file when needed.
//
// Per step the body contains, in order:
//
//  1. The bind line: pure recipes get one LHS (`_qDep<N> := call(...)`),
//     errored recipes get two (`_qDep<N>, _qAErr<N> := call(...)`).
//  2. For errored recipes: `if _qAErr<N> != nil { return ... }` — bubble
//     the recipe's own error.
//  3. If the output type is nilable (pointer / interface / slice / map /
//     chan / func), a runtime nil-check on _qDep<N>:
//       - Pure family: `if _qDep<N> == nil { panic("...") }`. Pure
//         q.Assemble has no error return, so a typed-nil from a buggy
//         constructor surfaces as a panic at the recipe site rather
//         than propagating into the call graph as a non-nil interface
//         holding a nil concrete (Go's typed-nil-interface pitfall —
//         the same one q.Try guards against).
//       - Errored family: bubble `fmt.Errorf("...: %w", q.ErrNil)` so
//         callers can `errors.Is(err, q.ErrNil)`.
//     The check operates on _qDep<N> *before* it's passed to any
//     consumer recipe — Go's implicit concrete→interface conversion
//     happens at the call site, not the bind site, so checking the
//     bound value catches the typed-nil before it becomes a non-nil
//     interface.
func buildAssembleBody(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string, targetText, alias string) (string, bool) {
	var b bytes.Buffer
	depVar := map[string]string{}
	errored := sub.Family == familyAssembleErr || sub.Family == familyAssembleE
	familyName := assembleFamilyLabel(sub.Family)
	fmtUsed := false
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

		if step.Errored {
			errVar := fmt.Sprintf("_qAErr%d", n)
			fmt.Fprintf(&b, "\n\t%s, %s := %s", dep, errVar, callText)
			fmt.Fprintf(&b, "\n\tif %s != nil { return *new(%s), %s }", errVar, targetText, errVar)
		} else {
			fmt.Fprintf(&b, "\n\t%s := %s", dep, callText)
		}

		if step.OutputIsNilable {
			label := step.Label
			if label == "" {
				label = fmt.Sprintf("#%d", step.RecipeIdx+1)
			}
			if errored {
				msg := fmt.Sprintf(`%s: recipe %s returned nil`, familyName, label)
				fmt.Fprintf(&b, "\n\tif %s == nil { return *new(%s), fmt.Errorf(%q + %q, %s.ErrNil) }",
					dep, targetText, msg, ": %w", alias)
				fmtUsed = true
			} else {
				msg := fmt.Sprintf(`%s: recipe %s returned nil`, familyName, label)
				fmt.Fprintf(&b, "\n\tif %s == nil { panic(%q) }", dep, msg)
			}
		}
	}

	targetDep, ok := depVar[sub.AssembleTargetKey]
	if !ok {
		// Should never happen if resolveAssemble succeeded; emit a
		// syntactically-valid fallback so a downstream build error
		// surfaces with context rather than crashing the rewriter.
		targetDep = fmt.Sprintf("*new(%s)", targetText)
	}
	if errored {
		fmt.Fprintf(&b, "\n\treturn %s, nil", targetDep)
	} else {
		fmt.Fprintf(&b, "\n\treturn %s", targetDep)
	}
	return b.String(), fmtUsed
}

// assembleHasNilableStep reports whether any step in sub's resolved
// recipe sequence has a nilable output type. Used by the rewriter's
// in-place dispatch to flag fmt-import-needed for the AssembleErr/E
// path, which emits `fmt.Errorf("...: %w", q.ErrNil)` on the runtime
// nil-check. Pure q.Assemble's panic check uses a plain string and
// doesn't trigger this flag.
func assembleHasNilableStep(sub qSubCall) bool {
	for _, s := range sub.AssembleSteps {
		if s.OutputIsNilable {
			return true
		}
	}
	return false
}

// renderAssembleE produces the replacement for q.AssembleE chains.
// The IIFE body is identical to q.AssembleErr's; the chain method
// shapes the bubbled error at the outer bind site, exactly like
// renderTryE does for q.TryE.
func renderAssembleE(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, alias string, subs []qSubCall, subTexts []string) (string, bool, error) {
	results := sh.EnclosingFuncType.Results
	if results == nil || results.NumFields() == 0 {
		return "", false, fmt.Errorf("q.AssembleE used in a function with no return values; the bubble has nowhere to go")
	}
	zeros, err := zeroExprs(fset, src, results)
	if err != nil {
		return "", false, err
	}
	indent := indentOf(src, fset.Position(sh.Stmt.Pos()).Offset)
	errVar := fmt.Sprintf("_qErr%d", counter)

	t := assembleTargetText(sub)
	body, bodyFmtUsed := buildAssembleBody(fset, src, sub, subs, subTexts, t, alias)
	innerText := fmt.Sprintf("(func() (%s, error) {%s\n}())", t, body)

	bindLine := tryBindLine(fset, src, sh, errVar, innerText, indent, counter)

	switch sub.Method {
	case "Err":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.AssembleE(...).Err requires exactly one argument (the replacement error); got %d", len(sub.MethodArgs))
		}
		zeros[len(zeros)-1] = exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		return assembleErrBlock(bindLine, errVar, indent, zeros), bodyFmtUsed, nil
	case "ErrF":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.AssembleE(...).ErrF requires exactly one argument (an error-transform fn); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf("(%s)(%s)", fn, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), bodyFmtUsed, nil
	case "Wrap":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.AssembleE(...).Wrap requires exactly one argument (the message string); got %d", len(sub.MethodArgs))
		}
		msg := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		zeros[len(zeros)-1] = fmt.Sprintf(`fmt.Errorf("%%s: %%w", %s, %s)`, msg, errVar)
		return assembleErrBlock(bindLine, errVar, indent, zeros), true, nil // Wrap/Wrapf — fmt always used
	case "Wrapf":
		if len(sub.MethodArgs) < 1 {
			return "", false, fmt.Errorf("q.AssembleE(...).Wrapf requires at least one argument (the format string); got %d", len(sub.MethodArgs))
		}
		formatExpr, ok := sub.MethodArgs[0].(*ast.BasicLit)
		if !ok || formatExpr.Kind != token.STRING {
			return "", false, fmt.Errorf("q.AssembleE(...).Wrapf's first argument must be a string literal so the rewriter can splice in `: %%w`")
		}
		raw := formatExpr.Value
		formatWithW := raw[:len(raw)-1] + `: %w` + `"`
		argParts := []string{formatWithW}
		for _, a := range sub.MethodArgs[1:] {
			argParts = append(argParts, exprTextSubst(fset, src, a, subs, subTexts))
		}
		argParts = append(argParts, errVar)
		zeros[len(zeros)-1] = fmt.Sprintf("fmt.Errorf(%s)", joinWith(argParts, ", "))
		return assembleErrBlock(bindLine, errVar, indent, zeros), true, nil // Wrap/Wrapf — fmt always used
	case "Catch":
		if len(sub.MethodArgs) != 1 {
			return "", false, fmt.Errorf("q.AssembleE(...).Catch requires exactly one argument (a (T, error)-returning fn); got %d", len(sub.MethodArgs))
		}
		fn := exprTextSubst(fset, src, sub.MethodArgs[0], subs, subTexts)
		retErrVar := fmt.Sprintf("_qRet%d", counter)
		zeros[len(zeros)-1] = retErrVar
		recoveryLHS := lhsTextOrUnderscore(fset, src, sh, counter)
		return assembleCatchErrBlock(bindLine, recoveryLHS, errVar, retErrVar, fn, indent, zeros), false, nil
	}
	return "", false, fmt.Errorf("renderAssembleE: unknown method %q", sub.Method)
}
