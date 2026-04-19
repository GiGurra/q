package preprocessor_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runWithCache runs a command in dir with GOCACHE pointed at cache, so
// callers needing a guaranteed-empty cache (typically for negative
// link-gate assertions) can isolate from the harness-shared goCache.
func runWithCache(dir, cache, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOCACHE="+cache)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestLinkGateFailsWithoutPreprocessor is the negative half of the
// fixture suite. The fixture suite proves builds with -toolexec=q
// succeed; this test proves the same source fails to link WITHOUT
// -toolexec=q, with the diagnostic naming the missing
// _q_atCompileTime symbol. Together they document the gate's
// contract: forgetting the preprocessor is a loud, deterministic
// build failure.
//
// Uses its own isolated GOCACHE because the harness-shared cache may
// hold a stub-containing pkg/q.a from a prior toolexec build —
// reusing it would let this test link successfully and the negative
// assertion would silently regress. Same hazard documented in
// proven/CLAUDE.md under "cache discipline".
func TestLinkGateFailsWithoutPreprocessor(t *testing.T) {
	tmp := t.TempDir()
	isolatedCache := t.TempDir()

	src := `package main

import (
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

func parseAndDouble(s string) (int, error) {
	n := q.Try(strconv.Atoi(s))
	return n * 2, nil
}

func main() { _, _ = parseAndDouble("21") }
`
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	goMod := fmt.Sprintf(`module fixture

go 1.26

require github.com/GiGurra/q v0.0.0

replace github.com/GiGurra/q => %s
`, repoRoot())
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := runWithCache(tmp, isolatedCache, "go", "mod", "tidy"); err != nil {
		t.Fatalf("go mod tidy failed: %v\n---\n%s", err, out)
	}

	// No -toolexec — the link should fail on the unresolved gate symbol.
	out, err := runWithCache(tmp, isolatedCache, "go", "build", "./...")
	if err == nil {
		t.Fatalf("expected build to fail without -toolexec=q, but it succeeded.\noutput:\n%s", out)
	}
	const want = "_q_atCompileTime"
	if !strings.Contains(out, want) {
		t.Fatalf("build failed but output missing %q.\ngot:\n%s", want, out)
	}
}
