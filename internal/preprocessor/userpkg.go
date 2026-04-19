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
	"go/parser"
	"go/token"
	"os"
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

	type rewritten struct {
		origPath string
		newPath  string
	}

	var diags []Diagnostic
	var rewrittenFiles []rewritten
	var cleanups []func()
	importsToInject := map[string]bool{}

	cleanupAll := func() {
		for _, c := range cleanups {
			c()
		}
	}

	fset := token.NewFileSet()
	for _, src := range sources {
		file, err := parser.ParseFile(fset, src, nil, parser.ParseComments)
		if err != nil {
			cleanupAll()
			return nil, fmt.Errorf("parse %s: %w", src, err)
		}
		shapes, srcDiags, err := scanFile(fset, src, file)
		if err != nil {
			cleanupAll()
			return nil, err
		}
		diags = append(diags, srcDiags...)
		if len(shapes) == 0 {
			continue
		}

		original, err := os.ReadFile(src)
		if err != nil {
			cleanupAll()
			return nil, fmt.Errorf("read %s: %w", src, err)
		}
		alias := qImportAlias(file)
		rewrittenBytes, addedImports, err := rewriteFile(fset, file, original, shapes, alias)
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
		rewrittenFiles = append(rewrittenFiles, rewritten{origPath: src, newPath: newPath})
	}

	if len(diags) > 0 {
		cleanupAll()
		return &Plan{Diags: diags}, nil
	}
	if len(rewrittenFiles) == 0 {
		return nil, nil
	}

	newArgs := make([]string, len(toolArgs))
	copy(newArgs, toolArgs)
	for _, rw := range rewrittenFiles {
		for i, a := range newArgs {
			if a == rw.origPath {
				newArgs[i] = rw.newPath
			}
		}
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
