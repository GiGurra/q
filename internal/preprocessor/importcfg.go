package preprocessor

// importcfg.go — extend the compile invocation's -importcfg file when
// the rewriter injects an `import "<pkg>"` into a user source.
//
// Background. `go build` constructs each compile's importcfg from the
// source files' *direct* imports plus their transitive deps. When the
// preprocessor adds `import "fmt"` (for a Wrap / Wrapf rewrite) or
// `import "errors"` (for a NotNilE.Wrap rewrite) into a user file
// that didn't already import them, the compile fails with:
//
//	could not import fmt (open : no such file or directory)
//
// because the importcfg has no entry pointing at fmt's compiled .a.
//
// Fix. Resolve the missing packages' export paths via `go list -export`
// (which uses the same build cache the surrounding build is filling),
// write a copy of the original importcfg with the extra entries
// appended, and substitute the new path into the compile argv.
//
// Performance: `go list -export` shells out, so we cache results
// process-wide in a small map keyed on package path. A single
// preprocessor process handles many compile invocations, so the cost
// amortises to one shell-out per (preprocessor process × needed
// stdlib package). For the bare go-build case (preprocessor binary
// per build), this is at most two extra `go list` calls (fmt and
// errors) per build.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// pkgExportCache maps a Go import path to the file path of its
// compiled archive (.a), as reported by `go list -export`. Populated
// lazily by lookupExport.
var (
	pkgExportCacheMu sync.Mutex
	pkgExportCache   = map[string]string{}
)

// lookupExport returns the on-disk path of the compiled archive for
// the given import path. Caches results process-wide.
func lookupExport(importPath string) (string, error) {
	pkgExportCacheMu.Lock()
	if got, ok := pkgExportCache[importPath]; ok {
		pkgExportCacheMu.Unlock()
		return got, nil
	}
	pkgExportCacheMu.Unlock()

	cmd := exec.Command("go", "list", "-export", "-f", "{{.Export}}", importPath)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		return "", fmt.Errorf("go list -export %s: %w (%s)", importPath, err, strings.TrimSpace(stderr))
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", fmt.Errorf("go list -export %s returned an empty path", importPath)
	}

	pkgExportCacheMu.Lock()
	pkgExportCache[importPath] = path
	pkgExportCacheMu.Unlock()
	return path, nil
}

// extendImportcfg returns toolArgs with the -importcfg flag's value
// pointing at a temporary copy of the original importcfg that
// additionally lists every package in addPkgs. The returned cleanup
// removes the temp file.
//
// If addPkgs is empty, returns toolArgs unchanged. If the original
// importcfg already lists every package in addPkgs, also returns
// unchanged — the rewriter calls this unconditionally when a fmt /
// errors injection happened, but the original file may have already
// imported the package, in which case the importcfg already has the
// entry and there's nothing to do.
func extendImportcfg(toolArgs, addPkgs []string) ([]string, func(), error) {
	if len(addPkgs) == 0 {
		return toolArgs, nil, nil
	}
	importcfgPath := compileImportcfg(toolArgs)
	if importcfgPath == "" {
		// No -importcfg flag — nothing to extend. The compile is
		// almost certainly going to fail anyway, but that's the
		// caller's problem and not ours to swallow.
		return toolArgs, nil, nil
	}

	original, err := os.ReadFile(importcfgPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read importcfg %s: %w", importcfgPath, err)
	}

	missing := missingPkgs(original, addPkgs)
	if len(missing) == 0 {
		return toolArgs, nil, nil
	}

	var lines []string
	for _, pkg := range missing {
		path, err := lookupExport(pkg)
		if err != nil {
			return nil, nil, err
		}
		lines = append(lines, fmt.Sprintf("packagefile %s=%s\n", pkg, path))
	}

	extended := append([]byte(nil), original...)
	if len(extended) > 0 && extended[len(extended)-1] != '\n' {
		extended = append(extended, '\n')
	}
	for _, line := range lines {
		extended = append(extended, []byte(line)...)
	}

	tmpPath, cleanup, err := writeTempFile("q-importcfg-*.txt", extended)
	if err != nil {
		return nil, nil, err
	}

	newArgs := make([]string, len(toolArgs))
	copy(newArgs, toolArgs)
	for i := 0; i+1 < len(newArgs); i++ {
		if newArgs[i] == "-importcfg" {
			newArgs[i+1] = tmpPath
			break
		}
	}
	return newArgs, cleanup, nil
}

// missingPkgs returns the subset of want that are not already listed
// in the importcfg bytes. Match is on the literal `packagefile <path>=`
// prefix, which is exactly the line shape `go build` emits.
func missingPkgs(importcfg []byte, want []string) []string {
	have := map[string]bool{}
	for _, line := range strings.Split(string(importcfg), "\n") {
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
		have[rest[:eq]] = true
	}
	var missing []string
	for _, pkg := range want {
		if !have[pkg] {
			missing = append(missing, pkg)
		}
	}
	return missing
}

// writeTempFile is the importcfg analogue of writeTempGoFile — same
// shape, no .go suffix expected.
func writeTempFile(pattern string, content []byte) (string, func(), error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", nil, err
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", nil, err
	}
	path := f.Name()
	return path, func() { _ = os.Remove(path) }, nil
}
