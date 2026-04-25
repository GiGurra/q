package preprocessor

// atcompiletime.go — synthesis pass for q.AtCompileTime.
//
// Per-package compile, after typecheck and before rewrite. Collects
// every q.AtCompileTime call site, topologically orders them by
// inter-call captures, builds ONE synthesized program that evaluates
// all calls together, runs it, and returns the resolved value text
// for each call.
//
// The synthesized program lives in `.q-comptime-<hash>/main.go`
// inside the user's module root. The leading `.` makes the Go
// toolchain ignore the directory for `./...` walks. Running inside
// the module root means the subprocess inherits the user's go.mod —
// all module-internal imports + dependencies + replace directives
// just work, no separate go.mod to synthesize.
//
// Phase 1 scope: primitive R, default JSONCodec only, inline-literal
// substitution at the call site (no companion file). Cross-call
// captures supported via topo-sort.
//
// Phase 2 scope: complex R via a chosen Codec, companion file
// (`_q_atcomptime.go`) with var + init() decode.
//
// Phase 3 scope: q.* in closure bodies (subprocess `go run` invoked
// with `-toolexec=q`), build cache.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// atCallInfo collects per-call data the synthesis pass needs.
type atCallInfo struct {
	Sub        *qSubCall
	Shape      *callShape
	File       *ast.File
	Closure    *ast.FuncLit
	ResultType string
	CodecExpr  string // empty when default JSONCodec[R]
	LHSObj     types.Object
	DepIdx     []int // indices of AtCompileTime calls captured by this closure
	OrderIdx   int   // post-topo-sort index (also the _qCt<N> suffix)

	// Comptime-call specific (familyComptimeCall):
	ComptimeFnIdent *ast.Ident       // the fn name at the call site (e.g. `fib`)
	ComptimeArgs    []ast.Expr       // call args
	ComptimeBinding *comptimeBinding // the decl backing this call
}

// atCTOutcome is the outcome of resolveAtCompileTimeCalls.
type atCTOutcome struct {
	// CompanionFile is the source for `_q_atcomptime.go` to be
	// added to the user-package compile. Empty when every resolved
	// call folds to an inline literal.
	CompanionFile string
	// ExtraImports lists package paths the companion file references
	// (so userpkg.go can extend the importcfg).
	ExtraImports []string
	// KeepAlivesByFile maps each user source file path to a list of
	// `<alias>.<symbol>` references the rewriter must append as
	// `var _ = <ref>` keep-alives. Required because the closure
	// substitution removes the only user-source references to
	// imports that were used only inside the closure body — Go
	// would otherwise reject the file with "imported and not used".
	KeepAlivesByFile map[string][]string
}

// resolveAtCompileTimeCalls is the entry point for the synthesis pass.
// Called from planUserPackage after typecheck and before rewrite.
//
// On success, every q.AtCompileTime sub-call's AtCTResolved field is
// populated with the rewriter-ready replacement text:
//   - inline-literal route (primitive R + default JSONCodec): a Go
//     literal like "42" or `"hello"`.
//   - companion-file route (everything else): a function-call reference
//     `_qCtFn<N>()` whose body in the companion file decodes the
//     embedded bytes via the codec and returns the value.
func resolveAtCompileTimeCalls(
	fset *token.FileSet,
	pkgPath, pkgName string,
	files []*ast.File,
	shapes []callShape,
	info *types.Info,
) (atCTOutcome, []Diagnostic, error) {
	// 0. Process q.Comptime decls: generate the IIFE-wrapped rewrite
	//    text for each, so `var X = q.Comptime(impl)` becomes a
	//    self-referential closure without triggering Go's init-cycle
	//    detector. This runs before the synthesis pass proper because
	//    the decl rewrites are pure AST manipulation (no subprocess
	//    needed) and the comptime call sites depend on knowing which
	//    bindings are valid.
	populateComptimeDeclRewrites(fset, shapes, info, pkgPath)

	// 1. Gather q.AtCompileTime calls.
	calls := collectAtCallInfos(fset, files, shapes, info)
	if len(calls) == 0 {
		return atCTOutcome{}, nil, nil
	}

	// 2. Build dep graph + topo-sort.
	if diags := buildAtCompileTimeDeps(fset, calls, info); len(diags) > 0 {
		return atCTOutcome{}, diags, nil
	}
	if diag, ok := topoSortAtCompileTime(fset, calls); ok {
		return atCTOutcome{}, []Diagnostic{diag}, nil
	}

	// 3. Resolve user module root (so we can place the synthesized
	//    main.go where it inherits the user's go.mod).
	modRoot, err := findModuleRoot(files, fset)
	if err != nil {
		return atCTOutcome{}, nil, fmt.Errorf("q.AtCompileTime: locate user module root: %w", err)
	}

	// 4. Synthesize the comptime main.go.
	mainSrc, keepAlivesByFile, mainDiags := synthesizeAtCompileTimeMain(fset, calls, info, pkgPath, pkgName)
	if len(mainDiags) > 0 {
		return atCTOutcome{}, mainDiags, nil
	}

	// 5. Write to <modRoot>/.q-comptime-<hash>/main.go and run.
	output, runDiag, runErr := runAtCompileTimeProgram(modRoot, pkgPath, mainSrc)
	if runErr != nil {
		return atCTOutcome{}, nil, runErr
	}
	if runDiag.Msg != "" {
		return atCTOutcome{}, []Diagnostic{runDiag}, nil
	}

	// 6. Parse the JSON array of per-call codec-encoded bytes.
	encoded, err := parseAtCompileTimeOutput(output)
	if err != nil {
		return atCTOutcome{}, nil, fmt.Errorf("q.AtCompileTime: parse subprocess output: %w", err)
	}
	if len(encoded) != len(calls) {
		return atCTOutcome{}, nil, fmt.Errorf("q.AtCompileTime: expected %d encoded results, got %d", len(calls), len(encoded))
	}

	// 7. Populate per-call resolved text. Build the companion file
	//    when any call uses the var+init route. Track which packages
	//    each result-type needs imported (for the companion file).
	compResultPkgs := collectTypePkgs(calls, info, pkgPath)
	var compBuilder atCompanionBuilder
	compBuilder.extraTypePkgs = compResultPkgs
	for i, call := range calls {
		raw := encoded[i]
		// Code-gen: extract the raw string contents (closure returned
		// Go source) and splice directly. The JSON envelope is just a
		// quoted string; unquote to get the source.
		if call.Sub.Family == familyAtCompileTimeCode {
			var src string
			if err := json.Unmarshal(raw, &src); err != nil {
				return atCTOutcome{}, nil, fmt.Errorf("q.AtCompileTimeCode: subprocess output for call %d isn't a valid JSON string: %w", i, err)
			}
			// Wrap in parens so the spliced source remains a single
			// expression even if it's a complex form.
			call.Sub.AtCTResolved = "(" + src + ")"
			continue
		}
		if call.CodecExpr == "" && isPrimitiveType(call.ResultType) {
			call.Sub.AtCTResolved = string(raw)
			continue
		}
		// Companion-file route: emit a function `_qCtFn<N>() R`
		// that decodes the embedded bytes via the codec and returns
		// the value. The call site rewrites to `_qCtFn<N>()`.
		// Function-call form (rather than `var _qCtValue<N>` ref)
		// is required so package-level user vars
		// (`var Lookup = q.AtCompileTime(...)`) see the decoded
		// value at var-init time — Go runs all package-level var
		// initializers before any init() function. With a func-call
		// initializer the decode runs on the first read (via the
		// var's initializer), not in a later init() that the var
		// has already missed.
		fnName := fmt.Sprintf("_qCtFn%d", call.OrderIdx)
		call.Sub.AtCTResolved = fnName + "()"
		call.Sub.AtCTIndex = call.OrderIdx
		companionType := call.ResultType
		if call.Sub.AsType != nil {
			if tv, ok := info.Types[call.Sub.AsType]; ok && tv.Type != nil {
				companionType = types.TypeString(tv.Type, func(p *types.Package) string {
					if p == nil || p.Path() == pkgPath {
						return ""
					}
					return p.Name()
				})
			}
		}
		compBuilder.add(fnName, companionType, call.CodecExpr, raw)
	}
	outcome := atCTOutcome{KeepAlivesByFile: keepAlivesByFile}
	if !compBuilder.empty() {
		src, imports := compBuilder.build(pkgName)
		outcome.CompanionFile = src
		outcome.ExtraImports = imports
	}
	return outcome, nil, nil
}

// collectAtCallInfos gathers every q.AtCompileTime sub-call across all
// shapes in the package, capturing per-call AST nodes and the file
// each closure lives in (needed for import resolution and source-text
// extraction).
func collectAtCallInfos(fset *token.FileSet, files []*ast.File, shapes []callShape, info *types.Info) []*atCallInfo {
	// Build a Pos→file map so we can locate each closure's containing
	// file by its source-position offset.
	fileByPos := func(pos token.Pos) *ast.File {
		for _, f := range files {
			if f.Pos() <= pos && pos <= f.End() {
				return f
			}
		}
		return nil
	}

	var calls []*atCallInfo
	for i := range shapes {
		for j := range shapes[i].Calls {
			sc := &shapes[i].Calls[j]
			// Comptime call sites: fn(args) where fn is q.Comptime-marked.
			if sc.Family == familyComptimeCall {
				ident, ok := sc.ComptimeFnExpr.(*ast.Ident)
				if !ok || comptimeBindings == nil {
					continue
				}
				binding, ok := comptimeBindings[ident.Name]
				if !ok {
					continue
				}
				ci := &atCallInfo{
					Sub:             sc,
					Shape:           &shapes[i],
					File:            fileByPos(sc.OuterCall.Pos()),
					ComptimeFnIdent: ident,
					ComptimeArgs:    sc.ComptimeArgs,
					ComptimeBinding: binding,
				}
				if shapes[i].Form == formDefine && shapes[i].LHSExpr != nil {
					if id, ok := shapes[i].LHSExpr.(*ast.Ident); ok {
						if obj := info.Defs[id]; obj != nil {
							ci.LHSObj = obj
						}
					}
				}
				calls = append(calls, ci)
				continue
			}
			if sc.AtCTClosure == nil {
				continue
			}
			if sc.Family != familyAtCompileTime && sc.Family != familyAtCompileTimeCode {
				continue
			}
			ci := &atCallInfo{
				Sub:        sc,
				Shape:      &shapes[i],
				File:       fileByPos(sc.AtCTClosure.Pos()),
				Closure:    sc.AtCTClosure,
				ResultType: sc.AtCTResultText,
			}
			// Code-gen flavour: closure body returns Go source as a
			// `string`. Force ResultType to `string` regardless of
			// the user-supplied R, since the synthesized program
			// uses the closure's actual return type.
			if sc.Family == familyAtCompileTimeCode {
				ci.ResultType = "string"
			}
			if sc.AtCTCodecExpr != nil {
				ci.CodecExpr = exprAsSourceText(fset, sc.AtCTCodecExpr)
			}
			// Capture LHS object for cross-call dependency detection.
			if shapes[i].Form == formDefine && shapes[i].LHSExpr != nil {
				if id, ok := shapes[i].LHSExpr.(*ast.Ident); ok {
					if obj := info.Defs[id]; obj != nil {
						ci.LHSObj = obj
					}
				}
			}
			calls = append(calls, ci)
		}
	}
	return calls
}

// buildAtCompileTimeDeps walks each closure body for *ast.Ident
// references to other AtCompileTime calls' LHS bindings. Records the
// dep edges on each call's DepIdx slice. Returns diagnostics for any
// captured non-AtCompileTime variable.
func buildAtCompileTimeDeps(fset *token.FileSet, calls []*atCallInfo, info *types.Info) []Diagnostic {
	objToIdx := map[types.Object]int{}
	for i, c := range calls {
		if c.LHSObj != nil {
			objToIdx[c.LHSObj] = i
		}
	}

	var diags []Diagnostic
	for i, c := range calls {
		// Comptime call site — walk the args, not a closure body.
		// Each arg may reference other AtCompileTime LHS bindings
		// (cross-call captures); other free idents must be
		// package-level / builtins.
		if c.Sub.Family == familyComptimeCall {
			seen := map[int]bool{}
			for _, arg := range c.ComptimeArgs {
				ast.Inspect(arg, func(n ast.Node) bool {
					id, ok := n.(*ast.Ident)
					if !ok {
						return true
					}
					obj := info.Uses[id]
					if obj == nil {
						return true
					}
					if obj.Pkg() == nil {
						return true
					}
					if _, isPkg := obj.(*types.PkgName); isPkg {
						return true
					}
					if isPackageLevelObject(obj) {
						return true
					}
					if v, isVar := obj.(*types.Var); isVar && v.IsField() {
						return true
					}
					if depIdx, ok := objToIdx[obj]; ok {
						if depIdx == i {
							return true
						}
						if !seen[depIdx] {
							seen[depIdx] = true
							c.DepIdx = append(c.DepIdx, depIdx)
						}
						return true
					}
					pos := fset.Position(id.Pos())
					diags = append(diags, Diagnostic{
						File: pos.Filename,
						Line: pos.Line,
						Col:  pos.Column,
						Msg:  fmt.Sprintf("q: q.Comptime call site argument references local variable %q which is neither a package-level value nor another q.AtCompileTime / q.Comptime result — comptime args must be compile-time-resolvable", id.Name),
					})
					return false
				})
			}
			continue
		}
		// Walk the closure body. Any *ast.Ident whose resolved object
		// is not declared inside the closure must either be:
		//   - a package-level decl (allowed), or
		//   - the LHS of another AtCompileTime call (allowed; record
		//     dep edge), or
		//   - a built-in (allowed).
		// Anything else is a forbidden capture.
		closureScope := closureBodyObjects(c.Closure, info)
		// Identify all idents that are RHS of a SelectorExpr (i.e.
		// `<x>.<id>` — struct field, method, package-qualified
		// reference). Those resolve to *types.Var (field) or other
		// objects that aren't captures of the enclosing scope.
		selRHS := map[*ast.Ident]bool{}
		// Identify all idents that are the field name in a
		// composite literal (`SomeStruct{Field: value}` — Field is
		// an Ident referring to a field obj).
		compLitField := map[*ast.Ident]bool{}
		ast.Inspect(c.Closure.Body, func(n ast.Node) bool {
			switch nn := n.(type) {
			case *ast.SelectorExpr:
				selRHS[nn.Sel] = true
			case *ast.CompositeLit:
				for _, elt := range nn.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						if id, ok := kv.Key.(*ast.Ident); ok {
							compLitField[id] = true
						}
					}
				}
			}
			return true
		})
		seen := map[int]bool{}
		ast.Inspect(c.Closure.Body, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			if selRHS[id] || compLitField[id] {
				return true
			}
			obj := info.Uses[id]
			if obj == nil {
				return true
			}
			if closureScope[obj] {
				return true // declared inside this closure
			}
			if obj.Pkg() == nil {
				return true // builtin (e.g. len, cap)
			}
			// Package import reference — allowed.
			if _, isPkg := obj.(*types.PkgName); isPkg {
				return true
			}
			// Package-level decl in same package or imported package?
			if isPackageLevelObject(obj) {
				return true
			}
			// Type names declared at package scope (named types like
			// `Config`, `Status`) are package-level too — fall through
			// to isPackageLevelObject. But struct-field references
			// where the *types.Var.Pkg matches the user's package and
			// IsField is true should be allowed (they're not really
			// captures of a local variable).
			if v, isVar := obj.(*types.Var); isVar && v.IsField() {
				return true
			}
			// Dependency on another AtCompileTime call?
			if depIdx, ok := objToIdx[obj]; ok {
				if depIdx == i {
					pos := fset.Position(id.Pos())
					diags = append(diags, Diagnostic{
						File: pos.Filename,
						Line: pos.Line,
						Col:  pos.Column,
						Msg:  "q: q.AtCompileTime: closure references its own LHS binding",
					})
					return false
				}
				if !seen[depIdx] {
					seen[depIdx] = true
					c.DepIdx = append(c.DepIdx, depIdx)
				}
				return true
			}
			// Captured variable that doesn't fit any allowed shape.
			pos := fset.Position(id.Pos())
			diags = append(diags, Diagnostic{
				File: pos.Filename,
				Line: pos.Line,
				Col:  pos.Column,
				Msg:  fmt.Sprintf("q: q.AtCompileTime: closure body references local variable %q that is not itself a q.AtCompileTime result — comptime closures must be self-contained (only stdlib, package-level decls, and other q.AtCompileTime values are allowed)", id.Name),
			})
			return false
		})
	}
	return diags
}

// closureBodyObjects returns the set of *types.Objects declared
// inside the closure body — parameters, named results, locals from
// := / var decls.
func closureBodyObjects(fn *ast.FuncLit, info *types.Info) map[types.Object]bool {
	out := map[types.Object]bool{}
	if fn == nil {
		return out
	}
	collect := func(id *ast.Ident) {
		if obj := info.Defs[id]; obj != nil {
			out[obj] = true
		}
	}
	if fn.Type != nil {
		if fn.Type.Params != nil {
			for _, f := range fn.Type.Params.List {
				for _, n := range f.Names {
					collect(n)
				}
			}
		}
		if fn.Type.Results != nil {
			for _, f := range fn.Type.Results.List {
				for _, n := range f.Names {
					collect(n)
				}
			}
		}
	}
	if fn.Body != nil {
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			collect(id)
			return true
		})
	}
	return out
}

// isPackageLevelObject reports whether obj is declared at package
// scope (any package). Locally-scoped vars are NOT package-level.
func isPackageLevelObject(obj types.Object) bool {
	pkg := obj.Pkg()
	if pkg == nil {
		return false
	}
	scope := pkg.Scope()
	if scope == nil {
		return false
	}
	return scope.Lookup(obj.Name()) == obj
}

// topoSortAtCompileTime performs Kahn's algorithm on the calls slice,
// reordering it in dependency-first order. Sets each call's OrderIdx
// to its post-sort index. Returns a non-zero diagnostic + ok=true on
// cycle detection.
func topoSortAtCompileTime(fset *token.FileSet, calls []*atCallInfo) (Diagnostic, bool) {
	n := len(calls)
	indegree := make([]int, n)
	// Edge: dep → dependent. So if call i depends on call j, edge j → i.
	out := make([][]int, n)
	for i, c := range calls {
		for _, dep := range c.DepIdx {
			out[dep] = append(out[dep], i)
			indegree[i]++
		}
	}
	queue := make([]int, 0, n)
	for i := range calls {
		if indegree[i] == 0 {
			queue = append(queue, i)
		}
	}
	order := make([]int, 0, n)
	for len(queue) > 0 {
		// Stable order by source position so output is deterministic.
		sort.SliceStable(queue, func(a, b int) bool {
			pa := fset.Position(calls[queue[a]].Closure.Pos())
			pb := fset.Position(calls[queue[b]].Closure.Pos())
			if pa.Filename != pb.Filename {
				return pa.Filename < pb.Filename
			}
			return pa.Offset < pb.Offset
		})
		i := queue[0]
		queue = queue[1:]
		order = append(order, i)
		for _, j := range out[i] {
			indegree[j]--
			if indegree[j] == 0 {
				queue = append(queue, j)
			}
		}
	}
	if len(order) != n {
		// Cycle: pick any unsorted call's position.
		for i := range calls {
			if indegree[i] > 0 {
				pos := fset.Position(calls[i].Closure.Pos())
				return Diagnostic{
					File: pos.Filename,
					Line: pos.Line,
					Col:  pos.Column,
					Msg:  "q: q.AtCompileTime: cyclic dependency between AtCompileTime calls (closure A captures closure B which transitively captures A)",
				}, true
			}
		}
	}
	// Reorder calls in place.
	sorted := make([]*atCallInfo, n)
	for k, i := range order {
		sorted[k] = calls[i]
		sorted[k].OrderIdx = k
	}
	copy(calls, sorted)
	// Rewrite DepIdx slices to point into the new ordering.
	posInOrder := map[int]int{}
	for k, i := range order {
		posInOrder[i] = k
	}
	// We need to rebuild DepIdx in terms of pre-sort indices, then
	// translate: old index → new index. The DepIdx slices currently
	// hold pre-sort indices.
	for _, c := range calls {
		newDeps := make([]int, 0, len(c.DepIdx))
		for _, d := range c.DepIdx {
			newDeps = append(newDeps, posInOrder[d])
		}
		c.DepIdx = newDeps
	}
	return Diagnostic{}, false
}

// findModuleRoot locates the user's go.mod file by searching upward
// from any of the package's source files.
func findModuleRoot(files []*ast.File, fset *token.FileSet) (string, error) {
	for _, f := range files {
		if f.Pos() == token.NoPos {
			continue
		}
		path := fset.Position(f.Pos()).Filename
		if path == "" {
			continue
		}
		dir := filepath.Dir(path)
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", fmt.Errorf("no go.mod found upward from any package source file")
}

// synthesizeAtCompileTimeMain produces the source for the comptime
// program. Each call becomes a `_qCtN := func() R { ... }()` line in
// topo order; cross-call captures are rewritten by replacing the
// captured-variable identifier spans with `_qCtN` references.
//
// Side effect: populates each call's keep-alive snippets via the
// closures' selector-expression usage. The synthesis pass returns a
// per-source-file map of <pkgalias>.<symbol> snippets that the
// rewriter must append as `var _ = <ref>` lines (otherwise imports
// used only inside AtCompileTime closures become unused after the
// closure is replaced by its resolved literal).
func synthesizeAtCompileTimeMain(fset *token.FileSet, calls []*atCallInfo, info *types.Info, userPkgPath, userPkgName string) (string, map[string][]string, []Diagnostic) {
	// Whether the user's package is `package main`. Main packages
	// cannot be imported, so the synthesized program can't reference
	// same-package types or values declared in main. Closures inside
	// main must stick to stdlib + builtins + cross-package imports.
	isMainPkg := userPkgName == "main"
	// Alias under which the user's package is imported in the
	// synthesized main.go. We always qualify same-package types and
	// idents through this alias so the synthesized file compiles
	// regardless of identifier collisions.
	const userAlias = "_qUserPkg"
	effectiveAlias := userAlias
	if isMainPkg {
		effectiveAlias = ""
	}
	// Qualifier used by types.TypeString: same-package types get the
	// userAlias prefix; cross-package types use the package name.
	qualifier := func(p *types.Package) string {
		if p == nil {
			return ""
		}
		if p.Path() == userPkgPath {
			return effectiveAlias
		}
		return p.Name()
	}
	// Re-resolve each call's R type via the qualifier so type names
	// emitted into the synthesized main.go are valid. Skip code-gen
	// calls — those keep ResultType=`string` since the closure body
	// returns Go source as a string regardless of R.
	for _, c := range calls {
		if c.Sub.Family == familyAtCompileTimeCode {
			continue
		}
		if c.Sub.Family == familyComptimeCall {
			// R = the call expression's result type from go/types.
			if tv, ok := info.Types[c.Sub.OuterCall]; ok && tv.Type != nil {
				c.ResultType = types.TypeString(tv.Type, qualifier)
			}
			continue
		}
		if c.Sub.AsType != nil {
			if tv, ok := info.Types[c.Sub.AsType]; ok && tv.Type != nil {
				c.ResultType = types.TypeString(tv.Type, qualifier)
			}
		}
	}
	// Map of capture object → producing call's `_qCt<N>` name.
	captureNames := map[types.Object]string{}
	for _, c := range calls {
		if c.LHSObj != nil {
			captureNames[c.LHSObj] = fmt.Sprintf("_qCt%d", c.OrderIdx)
		}
	}

	// Per-call expression text — the RHS of `_qCt<N> := <expr>`. For
	// AtCompileTime / AtCompileTimeCode this is `func() R { ... }()`;
	// for Comptime calls it's `<implName>(<args>)`.
	// Also collect all imports referenced across all closures and
	// per-file keep-alive snippets so the rewriter can preserve
	// imports used only inside AtCompileTime closures.
	importSet := map[string]string{} // import path → alias ("" for default)
	callExprs := make([]string, len(calls))
	var diags []Diagnostic
	keepAlives := map[string]map[string]bool{} // file path → set of <alias>.<sym> snippets
	usesUserPkg := false
	// Track unique comptime bindings referenced across all comptime
	// calls. Each gets one impl-declaration emitted in the synthesized
	// program.
	comptimeImpls := map[string]*comptimeImpl{}
	for i, c := range calls {
		if c.Sub.Family == familyComptimeCall {
			implName := "_qComptime_" + c.ComptimeFnIdent.Name
			if _, exists := comptimeImpls[c.ComptimeFnIdent.Name]; !exists {
				// Resolve impl's function type via types.
				fnTypeText := "func()"
				if tv, ok := info.Types[c.ComptimeBinding.Impl]; ok && tv.Type != nil {
					fnTypeText = types.TypeString(tv.Type, qualifier)
				}
				comptimeImpls[c.ComptimeFnIdent.Name] = &comptimeImpl{
					Binding: c.ComptimeBinding,
					Name:    implName,
					FnType:  fnTypeText,
				}
			}
			// Render each arg with cross-call captures substituted.
			argTexts, argImports, argSels, argUsesUser, argDiag := renderComptimeArgs(fset, c, captureNames, info, userPkgPath, effectiveAlias)
			if argUsesUser {
				usesUserPkg = true
			}
			if argDiag.Msg != "" {
				diags = append(diags, argDiag)
				continue
			}
			callExprs[i] = implName + "(" + strings.Join(argTexts, ", ") + ")"
			for path, alias := range argImports {
				if existing, ok := importSet[path]; ok && existing != "" && alias != "" && existing != alias {
					continue
				}
				if alias != "" {
					importSet[path] = alias
				} else if _, ok := importSet[path]; !ok {
					importSet[path] = ""
				}
			}
			if c.File != nil {
				path := fset.Position(c.File.Pos()).Filename
				if keepAlives[path] == nil {
					keepAlives[path] = map[string]bool{}
				}
				for snippet := range argSels {
					keepAlives[path][snippet] = true
				}
			}
			continue
		}
		body, imports, sels, usedUser, diag := renderClosureBody(fset, c, captureNames, info, userPkgPath, effectiveAlias)
		if usedUser {
			usesUserPkg = true
		}
		if diag.Msg != "" {
			diags = append(diags, diag)
			continue
		}
		callExprs[i] = "func() " + c.ResultType + " {\n" + indentBlock(body, "\t\t") + "\n\t}()"
		for path, alias := range imports {
			if existing, ok := importSet[path]; ok && existing != "" && alias != "" && existing != alias {
				// Conflicting aliases — pick the first.
				continue
			}
			if alias != "" {
				importSet[path] = alias
			} else if _, ok := importSet[path]; !ok {
				importSet[path] = ""
			}
		}
		if c.File != nil {
			path := fset.Position(c.File.Pos()).Filename
			if keepAlives[path] == nil {
				keepAlives[path] = map[string]bool{}
			}
			for snippet := range sels {
				keepAlives[path][snippet] = true
			}
		}
	}
	keepAlivesByFile := map[string][]string{}
	for path, set := range keepAlives {
		var snippets []string
		for s := range set {
			snippets = append(snippets, s)
		}
		sort.Strings(snippets)
		keepAlivesByFile[path] = snippets
	}
	if len(diags) > 0 {
		return "", nil, diags
	}

	// Always-needed imports for the synthesis program itself.
	importSet["encoding/json"] = ""
	importSet["fmt"] = ""
	importSet["os"] = ""
	// User package import (so closure bodies can reference user
	// types like `Config`, package-level functions, constants).
	// Skip when the user package is `main` — main packages aren't
	// importable. Skip too when no closure / R type actually
	// references user-package symbols (would otherwise be an
	// "imported and not used" error).
	userPkgUsedByR := false
	for _, c := range calls {
		if strings.Contains(c.ResultType, userAlias+".") {
			userPkgUsedByR = true
			break
		}
	}
	if userPkgPath != "" && !isMainPkg && (usesUserPkg || userPkgUsedByR) {
		importSet[userPkgPath] = userAlias
	}

	// Render imports block.
	var importPaths []string
	for p := range importSet {
		importPaths = append(importPaths, p)
	}
	sort.Strings(importPaths)
	var b strings.Builder
	b.WriteString("// Code generated by the q preprocessor for q.AtCompileTime. DO NOT EDIT.\n")
	b.WriteString("package main\n\n")
	b.WriteString("import (\n")
	for _, p := range importPaths {
		alias := importSet[p]
		if alias == "" {
			fmt.Fprintf(&b, "\t%q\n", p)
		} else {
			fmt.Fprintf(&b, "\t%s %q\n", alias, p)
		}
	}
	b.WriteString(")\n\n")
	// Emit one impl declaration per unique comptime function used.
	// Self-references to the user's name (`fib` inside the closure
	// referring to the package-level `fib` var) are rewritten to the
	// synthesised name (`_qComptime_fib`) so the recursion resolves
	// inside the synthesis program rather than chasing the user
	// package's symbol.
	implNames := make([]string, 0, len(comptimeImpls))
	for name := range comptimeImpls {
		implNames = append(implNames, name)
	}
	sort.Strings(implNames)
	for _, userName := range implNames {
		impl := comptimeImpls[userName]
		implSrc := renderComptimeImpl(fset, impl, comptimeImpls, info, userPkgPath, effectiveAlias)
		fmt.Fprintf(&b, "var %s %s\n", impl.Name, impl.FnType)
		fmt.Fprintf(&b, "var _ = func() bool { %s = %s; return true }()\n\n", impl.Name, implSrc)
	}
	b.WriteString("func main() {\n")
	for i, expr := range callExprs {
		c := calls[i]
		fmt.Fprintf(&b, "\t_qCt%d := %s\n", c.OrderIdx, expr)
		_ = c
	}
	b.WriteString("\tout := make([]json.RawMessage, 0, ")
	fmt.Fprintf(&b, "%d)\n", len(calls))
	for _, c := range calls {
		fmt.Fprintf(&b, "\tout = append(out, _qCtMarshal(_qCt%d))\n", c.OrderIdx)
	}
	b.WriteString("\tdata, err := json.Marshal(out)\n")
	b.WriteString("\tif err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }\n")
	b.WriteString("\tfmt.Println(string(data))\n")
	b.WriteString("\tos.Exit(0)\n")
	b.WriteString("}\n\n")
	b.WriteString("func _qCtMarshal[T any](v T) json.RawMessage {\n")
	b.WriteString("\tdata, err := json.Marshal(v)\n")
	b.WriteString("\tif err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }\n")
	b.WriteString("\treturn data\n")
	b.WriteString("}\n")
	return b.String(), keepAlivesByFile, nil
}

// renderClosureBody returns the source text of the closure's body
// (with captured-variable references rewritten to _qCt<N>) plus the
// imports referenced inside and the set of <pkgalias>.<symbol>
// snippets the rewriter should emit as keep-alives in the user file
// (so imports don't go unused after closure substitution).
func renderClosureBody(fset *token.FileSet, c *atCallInfo, captureNames map[types.Object]string, info *types.Info, userPkgPath, userAlias string) (string, map[string]string, map[string]bool, bool, Diagnostic) {
	body := c.Closure.Body
	if body == nil {
		return "", nil, nil, false, Diagnostic{}
	}
	bodyStart := fset.Position(body.Lbrace).Offset
	bodyEnd := fset.Position(body.Rbrace).Offset
	// Read source from disk via the file's printer.
	var srcBytes []byte
	if c.File != nil {
		path := fset.Position(c.File.Pos()).Filename
		data, err := os.ReadFile(path)
		if err == nil {
			srcBytes = data
		}
	}
	if srcBytes == nil {
		// Fall back to printer-based render.
		var b strings.Builder
		_ = printer.Fprint(&b, fset, body)
		return b.String(), nil, nil, false, Diagnostic{}
	}

	// Walk for capture rewrites and import references.
	type span struct {
		start, end int
		text       string
	}
	var spans []span
	importsUsed := map[string]string{} // path → alias
	keepAliveSnippets := map[string]bool{}
	usedUserPkg := false
	importLookup := map[string]string{}
	if c.File != nil {
		for _, imp := range c.File.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			alias := ""
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			// Default alias = last path segment.
			lookup := alias
			if lookup == "" {
				lookup = path
				if i := strings.LastIndex(lookup, "/"); i >= 0 {
					lookup = lookup[i+1:]
				}
			}
			importLookup[lookup] = path + "\x00" + alias
		}
	}
	// Identify SelectorExpr-RHS and composite-literal field idents
	// so we don't try to qualify them. Same logic as in
	// buildAtCompileTimeDeps.
	skipIdent := map[*ast.Ident]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch nn := n.(type) {
		case *ast.SelectorExpr:
			skipIdent[nn.Sel] = true
		case *ast.CompositeLit:
			for _, elt := range nn.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					if id, ok := kv.Key.(*ast.Ident); ok {
						skipIdent[id] = true
					}
				}
			}
		}
		return true
	})
	ast.Inspect(body, func(n ast.Node) bool {
		switch nn := n.(type) {
		case *ast.Ident:
			if skipIdent[nn] {
				return true
			}
			if obj := info.Uses[nn]; obj != nil {
				if name, ok := captureNames[obj]; ok {
					s := fset.Position(nn.Pos()).Offset
					e := fset.Position(nn.End()).Offset
					spans = append(spans, span{start: s, end: e, text: name})
					return true
				}
				// Same-package package-level decl: qualify with userAlias.
				if userAlias != "" && obj.Pkg() != nil && obj.Pkg().Path() == userPkgPath {
					if isPackageLevelObject(obj) {
						s := fset.Position(nn.Pos()).Offset
						e := fset.Position(nn.End()).Offset
						spans = append(spans, span{start: s, end: e, text: userAlias + "." + nn.Name})
						usedUserPkg = true
						return true
					}
				}
			}
		case *ast.SelectorExpr:
			if x, ok := nn.X.(*ast.Ident); ok {
				if obj := info.Uses[x]; obj != nil {
					if pkgName, isPkg := obj.(*types.PkgName); isPkg {
						p := pkgName.Imported().Path()
						alias := pkgName.Name()
						// Default alias?
						lastSeg := p
						if i := strings.LastIndex(lastSeg, "/"); i >= 0 {
							lastSeg = lastSeg[i+1:]
						}
						if alias == lastSeg {
							alias = ""
						}
						importsUsed[p] = alias
						// Skip keep-alive injection for pkg/q — the
						// rewriter already emits `var _ = q.ErrNil`
						// after every rewritten file, which keeps the
						// q import alive. Plus q.* often refers to
						// generic functions (q.Match, q.Case) which
						// can't be assigned to `_` without
						// instantiation.
						if p == qPkgImportPath {
							return true
						}
						// Record keep-alive snippet — use the object's
						// kind (TypeName vs Var/Func/Const) to choose
						// between `var _ X.Y` (type form) and
						// `var _ = X.Y` (value form). Skip generic
						// functions (uninstantiated) which can't be
						// used as expressions.
						selObj := info.Uses[nn.Sel]
						if fn, isFunc := selObj.(*types.Func); isFunc {
							if sig, ok := fn.Type().(*types.Signature); ok {
								if sig.RecvTypeParams() != nil || sig.TypeParams() != nil {
									return true
								}
							}
						}
						snippet := x.Name + "." + nn.Sel.Name
						if _, isType := selObj.(*types.TypeName); isType {
							keepAliveSnippets["T:"+snippet] = true
						} else {
							keepAliveSnippets["V:"+snippet] = true
						}
					}
				}
			}
		}
		return true
	})
	// Substitute spans (descending offset).
	sort.Slice(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
	bodyText := string(srcBytes[bodyStart : bodyEnd+1])
	for _, sp := range spans {
		rs := sp.start - bodyStart
		re := sp.end - bodyStart
		if rs < 0 || re > len(bodyText) {
			continue
		}
		bodyText = bodyText[:rs] + sp.text + bodyText[re:]
	}
	// Strip the surrounding `{` and `}`.
	bodyText = strings.TrimPrefix(bodyText, "{")
	bodyText = strings.TrimSuffix(bodyText, "}")
	bodyText = strings.TrimSpace(bodyText)
	return bodyText, importsUsed, keepAliveSnippets, usedUserPkg, Diagnostic{}
}

// populateComptimeDeclRewrites walks the per-package shapes for
// q.Comptime var decls and computes the IIFE-wrapped rewrite text
// for each. The rewriter consults the resulting AtCTResolved field
// when substituting the q.Comptime(...) call span.
//
// Why IIFE-wrap: a `var Fib = q.Comptime(func(n int) int { ... Fib(...) ... })`
// would trigger Go's init-cycle detector because Fib's initializer
// transitively references Fib via the closure body. We rewrite it to
// `var Fib = func() T { var _qfn T; _qfn = <impl with Fib→_qfn>; return _qfn }()`
// — a self-referential closure constructed inside an IIFE, so the
// outer Fib is set to a fully-bound function value with no init-time
// dependency on Fib itself.
func populateComptimeDeclRewrites(fset *token.FileSet, shapes []callShape, info *types.Info, pkgPath string) {
	for i := range shapes {
		sh := &shapes[i]
		for j := range sh.Calls {
			sc := &sh.Calls[j]
			if sc.Family != familyComptimeDecl || sc.AtCTClosure == nil {
				continue
			}
			lhsIdent, ok := sh.LHSExpr.(*ast.Ident)
			if !ok {
				continue
			}
			// Resolve T (the function type) from info.Types on the
			// FuncLit. Fall back to printer for the type expression.
			fnType := ""
			if tv, ok := info.Types[sc.AtCTClosure]; ok && tv.Type != nil {
				fnType = types.TypeString(tv.Type, func(p *types.Package) string {
					if p == nil || p.Path() == pkgPath {
						return ""
					}
					return p.Name()
				})
			}
			if fnType == "" {
				var sb strings.Builder
				_ = printer.Fprint(&sb, fset, sc.AtCTClosure.Type)
				fnType = sb.String()
			}
			// Read the FuncLit source from disk; substitute references
			// to the LHS binding ident with `_qfn`.
			path := fset.Position(sc.AtCTClosure.Pos()).Filename
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			litStart := fset.Position(sc.AtCTClosure.Pos()).Offset
			litEnd := fset.Position(sc.AtCTClosure.End()).Offset
			litText := string(data[litStart:litEnd])
			bindObj := info.Defs[lhsIdent]
			if bindObj != nil {
				type span struct {
					start, end int
				}
				var spans []span
				ast.Inspect(sc.AtCTClosure, func(n ast.Node) bool {
					id, ok := n.(*ast.Ident)
					if !ok {
						return true
					}
					if obj := info.Uses[id]; obj == bindObj {
						spans = append(spans, span{
							start: fset.Position(id.Pos()).Offset,
							end:   fset.Position(id.End()).Offset,
						})
					}
					return true
				})
				sort.Slice(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
				for _, sp := range spans {
					rs := sp.start - litStart
					re := sp.end - litStart
					if rs < 0 || re > len(litText) {
						continue
					}
					litText = litText[:rs] + "_qfn" + litText[re:]
				}
			}
			sc.AtCTResolved = "func() " + fnType + " { var _qfn " + fnType + "; _qfn = " + litText + "; return _qfn }()"
		}
	}
}

// comptimeImpl pairs a user-side comptime binding with its
// synthesised name + function-type text. The synthesis pass tracks
// one entry per unique comptime function referenced across all
// comptime call sites in the package.
type comptimeImpl struct {
	Binding *comptimeBinding
	Name    string // _qComptime_<userName>
	FnType  string // signature text, e.g. `func(int) int`
}

// renderComptimeArgs renders each comptime call argument to source
// text, substituting cross-call captured variables with their
// `_qCt<N>` references and same-package symbols with the userAlias
// qualifier (when applicable).
func renderComptimeArgs(fset *token.FileSet, c *atCallInfo, captureNames map[types.Object]string, info *types.Info, userPkgPath, userAlias string) ([]string, map[string]string, map[string]bool, bool, Diagnostic) {
	importsUsed := map[string]string{}
	keepAliveSnippets := map[string]bool{}
	usedUserPkg := false
	var srcBytes []byte
	if c.File != nil {
		path := fset.Position(c.File.Pos()).Filename
		data, err := os.ReadFile(path)
		if err == nil {
			srcBytes = data
		}
	}
	out := make([]string, len(c.ComptimeArgs))
	for argIdx, arg := range c.ComptimeArgs {
		if srcBytes == nil {
			var sb strings.Builder
			_ = printer.Fprint(&sb, fset, arg)
			out[argIdx] = sb.String()
			continue
		}
		argStart := fset.Position(arg.Pos()).Offset
		argEnd := fset.Position(arg.End()).Offset
		type span struct {
			start, end int
			text       string
		}
		var spans []span
		skipIdent := map[*ast.Ident]bool{}
		ast.Inspect(arg, func(n ast.Node) bool {
			switch nn := n.(type) {
			case *ast.SelectorExpr:
				skipIdent[nn.Sel] = true
			case *ast.CompositeLit:
				for _, elt := range nn.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						if id, ok := kv.Key.(*ast.Ident); ok {
							skipIdent[id] = true
						}
					}
				}
			}
			return true
		})
		ast.Inspect(arg, func(n ast.Node) bool {
			switch nn := n.(type) {
			case *ast.Ident:
				if skipIdent[nn] {
					return true
				}
				if obj := info.Uses[nn]; obj != nil {
					if name, ok := captureNames[obj]; ok {
						s := fset.Position(nn.Pos()).Offset
						e := fset.Position(nn.End()).Offset
						spans = append(spans, span{start: s, end: e, text: name})
						return true
					}
					if userAlias != "" && obj.Pkg() != nil && obj.Pkg().Path() == userPkgPath {
						if isPackageLevelObject(obj) {
							s := fset.Position(nn.Pos()).Offset
							e := fset.Position(nn.End()).Offset
							spans = append(spans, span{start: s, end: e, text: userAlias + "." + nn.Name})
							usedUserPkg = true
							return true
						}
					}
				}
			case *ast.SelectorExpr:
				if x, ok := nn.X.(*ast.Ident); ok {
					if obj := info.Uses[x]; obj != nil {
						if pkgName, isPkg := obj.(*types.PkgName); isPkg {
							p := pkgName.Imported().Path()
							alias := pkgName.Name()
							lastSeg := p
							if i := strings.LastIndex(lastSeg, "/"); i >= 0 {
								lastSeg = lastSeg[i+1:]
							}
							if alias == lastSeg {
								alias = ""
							}
							importsUsed[p] = alias
							if p == qPkgImportPath {
								return true
							}
							selObj := info.Uses[nn.Sel]
							if fn, isFunc := selObj.(*types.Func); isFunc {
								if sig, ok := fn.Type().(*types.Signature); ok {
									if sig.RecvTypeParams() != nil || sig.TypeParams() != nil {
										return true
									}
								}
							}
							snippet := x.Name + "." + nn.Sel.Name
							if _, isType := selObj.(*types.TypeName); isType {
								keepAliveSnippets["T:"+snippet] = true
							} else {
								keepAliveSnippets["V:"+snippet] = true
							}
						}
					}
				}
			}
			return true
		})
		sort.Slice(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
		argText := string(srcBytes[argStart:argEnd])
		for _, sp := range spans {
			rs := sp.start - argStart
			re := sp.end - argStart
			if rs < 0 || re > len(argText) {
				continue
			}
			argText = argText[:rs] + sp.text + argText[re:]
		}
		out[argIdx] = argText
	}
	return out, importsUsed, keepAliveSnippets, usedUserPkg, Diagnostic{}
}

// renderComptimeImpl renders the impl func literal source for a
// comptime function, with self-references rewritten to the
// synthesised name (so recursion resolves inside the synthesis
// program). Cross-comptime-fn references (one comptime fn calling
// another) are rewritten to the corresponding synthesised name.
// Same-package decls are qualified with userAlias.
func renderComptimeImpl(fset *token.FileSet, impl *comptimeImpl, allImpls map[string]*comptimeImpl, info *types.Info, userPkgPath, userAlias string) string {
	lit := impl.Binding.Impl
	if lit == nil {
		return "nil"
	}
	var srcBytes []byte
	implPath := fset.Position(lit.Pos()).Filename
	if implPath != "" {
		if data, err := os.ReadFile(implPath); err == nil {
			srcBytes = data
		}
	}
	if srcBytes == nil {
		var sb strings.Builder
		_ = printer.Fprint(&sb, fset, lit)
		return sb.String()
	}
	litStart := fset.Position(lit.Pos()).Offset
	litEnd := fset.Position(lit.End()).Offset
	type span struct {
		start, end int
		text       string
	}
	var spans []span
	skipIdent := map[*ast.Ident]bool{}
	ast.Inspect(lit, func(n ast.Node) bool {
		switch nn := n.(type) {
		case *ast.SelectorExpr:
			skipIdent[nn.Sel] = true
		case *ast.CompositeLit:
			for _, elt := range nn.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					if id, ok := kv.Key.(*ast.Ident); ok {
						skipIdent[id] = true
					}
				}
			}
		}
		return true
	})
	// Resolve the user's binding ident object so we can detect
	// self-references via *types.Object identity (more robust than
	// name matching in case of shadowing).
	var bindObj types.Object
	if d := info.Defs[impl.Binding.Ident]; d != nil {
		bindObj = d
	}
	// Map of other-comptime-fn user-name → synthesised name.
	otherImplsByObj := map[types.Object]string{}
	for userName, ci := range allImpls {
		if userName == impl.Binding.Ident.Name {
			continue
		}
		if d := info.Defs[ci.Binding.Ident]; d != nil {
			otherImplsByObj[d] = ci.Name
		}
	}
	ast.Inspect(lit, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if skipIdent[id] {
			return true
		}
		obj := info.Uses[id]
		if obj == nil {
			return true
		}
		// Self-reference to this impl's binding.
		if bindObj != nil && obj == bindObj {
			s := fset.Position(id.Pos()).Offset
			e := fset.Position(id.End()).Offset
			spans = append(spans, span{start: s, end: e, text: impl.Name})
			return true
		}
		// Reference to another comptime fn's binding.
		if name, ok := otherImplsByObj[obj]; ok {
			s := fset.Position(id.Pos()).Offset
			e := fset.Position(id.End()).Offset
			spans = append(spans, span{start: s, end: e, text: name})
			return true
		}
		// Same-package package-level decl: qualify with userAlias.
		if userAlias != "" && obj.Pkg() != nil && obj.Pkg().Path() == userPkgPath {
			if isPackageLevelObject(obj) {
				s := fset.Position(id.Pos()).Offset
				e := fset.Position(id.End()).Offset
				spans = append(spans, span{start: s, end: e, text: userAlias + "." + id.Name})
				return true
			}
		}
		return true
	})
	sort.Slice(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
	implText := string(srcBytes[litStart:litEnd])
	for _, sp := range spans {
		rs := sp.start - litStart
		re := sp.end - litStart
		if rs < 0 || re > len(implText) {
			continue
		}
		implText = implText[:rs] + sp.text + implText[re:]
	}
	return implText
}

// indentBlock prepends indent to each line of text.
func indentBlock(text, indent string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		lines[i] = indent + l
	}
	return strings.Join(lines, "\n")
}

// runAtCompileTimeProgram writes the synthesized main.go into a
// `.q-comptime-<hash>` directory under the user's module root, runs
// `go run` on it, and returns the captured stdout. Cleans up the
// directory afterward (always — even on failure).
func runAtCompileTimeProgram(modRoot, pkgPath, mainSrc string) ([]byte, Diagnostic, error) {
	hash := sha256.Sum256([]byte(pkgPath + "\x00" + mainSrc))
	dirName := ".q-comptime-" + hex.EncodeToString(hash[:8])
	dirPath := filepath.Join(modRoot, dirName)
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		return nil, Diagnostic{}, fmt.Errorf("mkdir %s: %w", dirPath, err)
	}
	defer func() { _ = os.RemoveAll(dirPath) }()

	mainPath := filepath.Join(dirPath, "main.go")
	if err := os.WriteFile(mainPath, []byte(mainSrc), 0o644); err != nil {
		return nil, Diagnostic{}, fmt.Errorf("write %s: %w", mainPath, err)
	}

	// Phase 3: invoke the subprocess with `-toolexec=<qBin>` so q.*
	// calls inside the closure body get rewritten before the
	// subprocess compiles. Without this flag, q.* calls in closures
	// would link-fail on the missing `_q_atCompileTime` symbol.
	qBin, qBinErr := os.Executable()
	args := []string{"run"}
	if qBinErr == nil && qBin != "" {
		args = append(args, "-toolexec="+qBin)
	}
	args = append(args, "./"+dirName)
	cmd := exec.Command("go", args...)
	cmd.Dir = modRoot
	// Strip GOFLAGS so the subprocess inherits a clean flag state
	// (the parent's flags would re-trigger toolexec via env, mixing
	// with the explicit -toolexec we set on the command line).
	env := os.Environ()
	filtered := env[:0]
	for _, e := range env {
		if strings.HasPrefix(e, "GOFLAGS=") {
			continue
		}
		filtered = append(filtered, e)
	}
	filtered = append(filtered, "GOFLAGS=")
	cmd.Env = filtered
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Surface the synthesized main.go in the diagnostic — without
		// it, code-gen / capture-rewrite bugs are nearly impossible to
		// debug from the toolchain's error output alone.
		mainSrcBytes, _ := os.ReadFile(mainPath)
		return nil, Diagnostic{
			Msg: fmt.Sprintf("q: q.AtCompileTime: subprocess failed: %v\n--- subprocess output ---\n%s\n--- synthesized main.go ---\n%s", err, string(out), string(mainSrcBytes)),
		}, nil
	}
	return out, Diagnostic{}, nil
}

// parseAtCompileTimeOutput extracts the JSON array of per-call
// codec-encoded results from the subprocess stdout. The subprocess
// writes one JSON array on its final line.
func parseAtCompileTimeOutput(stdout []byte) ([]json.RawMessage, error) {
	// The subprocess may print other lines before the final JSON
	// array (warnings, etc.). Walk lines from the end and pick the
	// first one that parses as a JSON array.
	lines := strings.Split(strings.TrimRight(string(stdout), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "[") {
			continue
		}
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(line), &arr); err == nil {
			return arr, nil
		}
	}
	return nil, fmt.Errorf("no JSON array in subprocess output:\n%s", string(stdout))
}

// isPrimitiveType reports whether typeText names a Go primitive type
// for which the JSON encoding IS valid Go-source syntax for the
// literal. JSON `42` is `42` in Go, JSON `"hi"` is `"hi"`, JSON
// `true` is `true`, JSON `3.14` is `3.14`. Anything else needs the
// var+init() route.
func isPrimitiveType(typeText string) bool {
	switch strings.TrimSpace(typeText) {
	case "string", "bool",
		"int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"byte", "rune",
		"float32", "float64":
		return true
	}
	return false
}

// exprAsSourceText prints an AST expression to source text using
// go/printer.
func exprAsSourceText(fset *token.FileSet, expr ast.Expr) string {
	var b strings.Builder
	_ = printer.Fprint(&b, fset, expr)
	return b.String()
}

// collectTypePkgs walks each call's R type recursively and returns
// the set of (pkgPath → pkgName) pairs the companion file's `var`
// declarations need to reference. Same-package types under
// `package main` aren't importable, so they're skipped — the
// companion file lives in the same package as the user's main and
// can reference those types unqualified.
func collectTypePkgs(calls []*atCallInfo, info *types.Info, userPkgPath string) map[string]string {
	out := map[string]string{}
	var visit func(t types.Type, seen map[types.Type]bool)
	visit = func(t types.Type, seen map[types.Type]bool) {
		if t == nil || seen[t] {
			return
		}
		seen[t] = true
		switch tt := t.(type) {
		case *types.Named:
			if pkg := tt.Obj().Pkg(); pkg != nil && pkg.Path() != userPkgPath {
				out[pkg.Path()] = pkg.Name()
			}
			visit(tt.Underlying(), seen)
		case *types.Pointer:
			visit(tt.Elem(), seen)
		case *types.Slice:
			visit(tt.Elem(), seen)
		case *types.Array:
			visit(tt.Elem(), seen)
		case *types.Map:
			visit(tt.Key(), seen)
			visit(tt.Elem(), seen)
		case *types.Chan:
			visit(tt.Elem(), seen)
		case *types.Struct:
			for i := 0; i < tt.NumFields(); i++ {
				visit(tt.Field(i).Type(), seen)
			}
		}
	}
	for _, c := range calls {
		if c.Sub.AsType == nil {
			continue
		}
		if tv, ok := info.Types[c.Sub.AsType]; ok && tv.Type != nil {
			visit(tv.Type, map[types.Type]bool{})
		}
	}
	return out
}

// atCompanionBuilder accumulates wrapper functions for non-primitive
// AtCompileTime calls and renders the companion file. Each call gets
// a `func _qCtFn<N>() R { /* decode */ return v }` that the rewriter
// substitutes at the call site. Function-call form means the decode
// runs on first read — works equally for package-level vars (which
// run before init()) and function-local sites.
type atCompanionBuilder struct {
	fns           []string          // function bodies
	usesQ         bool
	extraTypePkgs map[string]string // path → name (for var-type imports)
}

func (b *atCompanionBuilder) empty() bool { return len(b.fns) == 0 }

func (b *atCompanionBuilder) add(fnName, resultType, codecExpr string, encoded []byte) {
	codec := codecExpr
	if codec == "" {
		// Default JSONCodec[R].
		codec = fmt.Sprintf("q.JSONCodec[%s]()", resultType)
	}
	if strings.Contains(codec, "q.") {
		b.usesQ = true
	}
	// Encode `encoded` as a Go-quoted []byte literal.
	literal := "[]byte(" + strconv.Quote(string(encoded)) + ")"
	body := fmt.Sprintf(
		"func %s() %s {\n\tvar v %s\n\tdata := %s\n\tif err := (%s).Decode(data, &v); err != nil {\n\t\tpanic(\"q.AtCompileTime decode failed: \" + err.Error())\n\t}\n\treturn v\n}",
		fnName, resultType, resultType, literal, codec,
	)
	b.fns = append(b.fns, body)
}

func (b *atCompanionBuilder) build(pkgName string) (string, []string) {
	var sb strings.Builder
	sb.WriteString("// Code generated by the q preprocessor for q.AtCompileTime. DO NOT EDIT.\n\n")
	fmt.Fprintf(&sb, "package %s\n\n", pkgName)
	type imp struct {
		path  string
		alias string
	}
	imps := []imp{}
	if b.usesQ {
		imps = append(imps, imp{qPkgImportPath, "q"})
	}
	for path, name := range b.extraTypePkgs {
		// Use default-alias (empty alias) when the package's name is
		// the last path segment; explicit alias otherwise. The
		// extra-pkg map captures the actual package name so we can
		// just emit it as the import alias when there's a chance of
		// collision. We use the package name as the alias here.
		imps = append(imps, imp{path, name})
	}
	importPaths := []string{}
	if len(imps) > 0 {
		sort.Slice(imps, func(i, j int) bool { return imps[i].path < imps[j].path })
		sb.WriteString("import (\n")
		for _, im := range imps {
			fmt.Fprintf(&sb, "\t%s %q\n", im.alias, im.path)
			importPaths = append(importPaths, im.path)
		}
		sb.WriteString(")\n\n")
	}
	for _, fn := range b.fns {
		sb.WriteString(fn)
		sb.WriteString("\n\n")
	}
	return sb.String(), importPaths
}
