package preprocessor

// userpkg.go — entry point for the rewriter pass.
//
// planUserPackage runs once per non-pkg/q compile invocation. For each
// source file in the compile argv that imports github.com/GiGurra/q/pkg/q,
// it parses, scans for recognised q.* call shapes, rewrites them to
// the inlined `if err != nil { return …, err }` form, writes the
// rewritten contents to a temp file, and substitutes the temp path
// for the original in the compile argv.
//
// Files that don't import pkg/q, or that import it but contain no
// q.* calls, are left untouched. If any q.* call is in an
// unsupported position, planUserPackage returns a Plan with diagnostics
// and no NewArgs — the build fails before the compiler runs, with
// `file:line:col: q: <message>` lines that editors and CI parsers
// pick up automatically.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
)

// planUserPackage is the per-package handler invoked from compile.go
// for every non-pkg/q compile. The returned *Plan is forwarded by
// run.go: NewArgs becomes the argv for the wrapped compile, Cleanup
// runs after, Diags abort the build.
func planUserPackage(pkgPath string, toolArgs []string) (*Plan, error) {
	sources := compileSourceFiles(toolArgs)
	if len(sources) == 0 {
		return nil, nil
	}

	type parsed struct {
		src    string
		file   *ast.File
		shapes []callShape
	}
	type rewritten struct {
		origPath string
		newPath  string
	}

	var diags []Diagnostic
	var parsedFiles []parsed
	var allShapes []callShape
	var allFiles []*ast.File

	fset := token.NewFileSet()
	for _, src := range sources {
		file, err := parser.ParseFile(fset, src, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", src, err)
		}
		shapes, srcDiags, err := scanFile(fset, src, file)
		if err != nil {
			return nil, err
		}
		diags = append(diags, srcDiags...)
		parsedFiles = append(parsedFiles, parsed{src: src, file: file, shapes: shapes})
		allFiles = append(allFiles, file)
		allShapes = append(allShapes, shapes...)
	}

	// Types-pass guard: every q.* call site must have the built-in
	// `error` interface at its error slot. Concrete types (like
	// `*MyErr`) are rejected here with a clear diagnostic — Go's
	// implicit interface conversion would otherwise mask a typed
	// nil as a non-nil `error`, making the rewritten bubble check
	// fire for a notionally-nil error. See typecheck.go.
	importcfgPath := compileImportcfg(toolArgs)
	info, slotDiags := checkErrorSlotsWithInfo(fset, pkgPath, importcfgPath, allFiles, allShapes)
	diags = append(diags, slotDiags...)

	if len(diags) > 0 {
		return &Plan{Diags: diags}, nil
	}

	// q.AtCompileTime synthesis pass: collect all comptime calls,
	// topo-sort, run a single subprocess to evaluate them all, and
	// populate AtCTResolved on each qSubCall before the rewriter runs.
	pkgName := ""
	for _, pf := range parsedFiles {
		if pf.file.Name != nil {
			pkgName = pf.file.Name.Name
			break
		}
	}
	atOutcome, atDiags, atErr := resolveAtCompileTimeCalls(fset, pkgPath, pkgName, allFiles, allShapes, info)
	if atErr != nil {
		return nil, atErr
	}
	diags = append(diags, atDiags...)
	if len(diags) > 0 {
		return &Plan{Diags: diags}, nil
	}

	var rewrittenFiles []rewritten
	var cleanups []func()
	importsToInject := map[string]bool{}

	cleanupAll := func() {
		for _, c := range cleanups {
			c()
		}
	}

	for _, pf := range parsedFiles {
		if len(pf.shapes) == 0 {
			continue
		}
		original, err := os.ReadFile(pf.src)
		if err != nil {
			cleanupAll()
			return nil, fmt.Errorf("read %s: %w", pf.src, err)
		}
		alias := qImportAlias(pf.file)
		absSrc, absErr := filepath.Abs(pf.src)
		if absErr != nil {
			absSrc = pf.src
		}
		var fileKeepAlives []string
		if atOutcome.KeepAlivesByFile != nil {
			fileKeepAlives = atOutcome.KeepAlivesByFile[absSrc]
			if fileKeepAlives == nil {
				fileKeepAlives = atOutcome.KeepAlivesByFile[pf.src]
			}
		}
		rewrittenBytes, addedImports, err := rewriteFile(fset, pf.file, original, pf.shapes, alias, absSrc, fileKeepAlives)
		if err != nil {
			cleanupAll()
			return nil, err
		}
		for _, p := range addedImports {
			importsToInject[p] = true
		}
		newPath, cleanup, err := writeTempGoFile("q-rewrite-*.go", rewrittenBytes)
		if err != nil {
			cleanupAll()
			return nil, err
		}
		cleanups = append(cleanups, cleanup)
		rewrittenFiles = append(rewrittenFiles, rewritten{origPath: pf.src, newPath: newPath})
	}

	// Synthesize a companion methods file if any q.Gen* directives
	// were detected. Reads from allShapes (post-typecheck), produces
	// a single _q_gen.go containing all requested methods, and
	// appends it to the compile argv. The synthesis runs before
	// the importcfg extension below so any imports the synthesized
	// file pulls in (encoding/json, fmt) get registered.
	genDirs := collectGenDirectives(fset, allShapes)
	var genCleanup func()
	if len(genDirs) > 0 {
		genSrc := synthesizeGenFile(pkgName, genDirs)
		if genSrc != "" {
			path, cleanup, err := writeTempGoFile("q-gen-*.go", []byte(genSrc))
			if err != nil {
				cleanupAll()
				return nil, err
			}
			genCleanup = cleanup
			rewrittenFiles = append(rewrittenFiles, rewritten{origPath: "", newPath: path})
			// Mark imports the synthesized file uses so importcfg
			// extension catches them.
			for _, d := range genDirs {
				switch d.family {
				case familyGenEnumJSONStrict:
					importsToInject["encoding/json"] = true
					importsToInject["fmt"] = true
				case familyGenEnumJSONLax:
					importsToInject["encoding/json"] = true
				}
			}
		}
	}

	// Append the q.AtCompileTime companion file (if any) so the
	// var+init() decode hooks land in the user's package.
	if atOutcome.CompanionFile != "" {
		path, cleanup, err := writeTempGoFile("q-atct-*.go", []byte(atOutcome.CompanionFile))
		if err != nil {
			cleanupAll()
			return nil, err
		}
		cleanups = append(cleanups, cleanup)
		rewrittenFiles = append(rewrittenFiles, rewritten{origPath: "", newPath: path})
		for _, p := range atOutcome.ExtraImports {
			importsToInject[p] = true
		}
	}

	if len(rewrittenFiles) == 0 {
		return nil, nil
	}

	newArgs := make([]string, len(toolArgs))
	copy(newArgs, toolArgs)
	for _, rw := range rewrittenFiles {
		if rw.origPath == "" {
			// Synthesized file with no original — append to argv.
			newArgs = append(newArgs, rw.newPath)
			continue
		}
		for i, a := range newArgs {
			if a == rw.origPath {
				newArgs[i] = rw.newPath
			}
		}
	}
	if genCleanup != nil {
		cleanups = append(cleanups, genCleanup)
	}

	// Extend the compile's importcfg with any packages we injected
	// into rewritten files but that the original sources didn't import
	// directly. Without this the compile fails with
	// `could not import fmt (open : no such file or directory)` —
	// importcfg lists only the package's direct imports + their
	// transitive deps, computed by `go build` before the toolexec
	// pass; injected imports were not visible at that point.
	if len(importsToInject) > 0 {
		var addPkgs []string
		for p := range importsToInject {
			addPkgs = append(addPkgs, p)
		}
		extendedArgs, importcfgCleanup, err := extendImportcfg(newArgs, addPkgs)
		if err != nil {
			cleanupAll()
			return nil, err
		}
		if importcfgCleanup != nil {
			cleanups = append(cleanups, importcfgCleanup)
		}
		newArgs = extendedArgs
	}

	return &Plan{
		NewArgs: newArgs,
		Cleanup: cleanupAll,
	}, nil
}
