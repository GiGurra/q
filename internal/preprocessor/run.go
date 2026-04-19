// Package preprocessor implements the q -toolexec preprocessor.
//
// Run is the single entrypoint. It is called from cmd/q's main with
// os.Args[1:] and returns a shell-style exit code. Everything below is
// intentionally private: the preprocessor is distributed as the q
// binary, not as an importable library.
//
// Shape follows proven (github.com/GiGurra/proven) and rewire
// (github.com/GiGurra/rewire) closely: dispatch on the tool being
// wrapped, AST-based scanning of the source files the Go tool received,
// a minimal set of rewrites emitted to temp files that are appended (or
// substituted) to the tool's argv, and a final forward to the real
// tool. Nothing in the original source tree is modified.
//
// Phase 1 (this file plus qstub.go): inject the _q_atCompileTime
// linker stub when compiling pkg/q. Without that, every program that
// imports pkg/q fails to link — by design.
//
// Phase 2+ (rewriter.go, future): scan user packages, replace each
// q.NoErr/NonNil family call site with the inlined error-forwarding
// statements, hand the rewritten file to the compiler.
package preprocessor

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Run executes the preprocessor with the toolexec-style arguments
// (os.Args[1:]): the first element is the path to the underlying Go
// tool (compile, link, asm, …), and the rest are the tool's own
// arguments. Run returns the exit code to hand back to the OS.
func Run(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "q: expected toolexec invocation (<tool-path> [args...])")
		return 2
	}

	_, chain, toolPath, toolArgs, ok := parseChain(args)
	if !ok {
		_, _ = fmt.Fprintln(stderr, "q: --and-then present but no Go tool found in remaining args")
		return 1
	}

	plan, err := planCompile(toolPath, toolArgs)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "q: %v\n", err)
		return 1
	}
	if plan != nil && plan.Cleanup != nil {
		defer plan.Cleanup()
	}
	if plan != nil && len(plan.Diags) > 0 {
		for _, d := range plan.Diags {
			_, _ = fmt.Fprintln(stderr, d)
		}
		return 1
	}
	if plan != nil && plan.NewArgs != nil {
		toolArgs = plan.NewArgs
	}

	// Chain-aware exec: when --and-then was used, invoke the successor
	// with the Go tool (and possibly-rewritten args) appended, so the
	// chain threads through each preprocessor in order before reaching
	// the real tool. Stdio and env are forwarded intact in both paths.
	execName, execArgs := toolPath, toolArgs
	if len(chain.NextCmd) > 0 {
		execName = chain.NextCmd[0]
		execArgs = append(slices.Clone(chain.NextCmd[1:]), toolPath)
		execArgs = append(execArgs, toolArgs...)
	}

	cmd := exec.Command(execName, execArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		_, _ = fmt.Fprintf(stderr, "q: failed to run %s: %v\n", execName, err)
		return 1
	}
	return 0
}

// isCompileTool reports whether the tool being invoked is the Go
// compiler. The Go toolchain binaries live under $GOTOOLDIR; only the
// basename (with an optional .exe suffix on Windows) identifies the
// tool.
func isCompileTool(toolPath string) bool {
	base := strings.TrimSuffix(filepath.Base(toolPath), ".exe")
	return base == "compile"
}
