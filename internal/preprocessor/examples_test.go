package preprocessor_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestExamples enforces that every runnable example under example/<name>/
// compiles AND prints exactly what its expected_run.txt promises. This is
// the doc-coverage harness: examples mirror the snippets in docs/api/<page>.md
// one-to-one, so a failing example means either the doc lies or the
// implementation regressed. Negative (must-fail-build) coverage lives under
// internal/preprocessor/testdata/cases/ — examples are positive only.
//
// Each example/<name>/ directory must contain:
//   - one or more *.go files forming `package main` (already present, since
//     they're part of the host module)
//   - expected_run.txt whose whitespace-trimmed content equals the trimmed
//     stdout produced by `go run -toolexec=q ./example/<name>`.
//
// A directory without expected_run.txt is skipped — useful while authoring
// a new example before pinning its output. CI does not let you forget,
// because docs/api/<page>.md → example/<page>/ is the contract.
// exampleBuildTag returns the build tag (if any) required by the
// example/<name>/ package. Most examples are plain `package main` and
// need no tag; a small set use APIs whose pre-rewrite source the Go
// typechecker can't accept (e.g. q.Sealed synthesises marker methods
// only visible after the toolexec pass), and those examples gate
// themselves behind a tag so plain `go build ./...` and `golangci-lint
// run ./...` skip them.
func exampleBuildTag(name string) (string, bool) {
	switch name {
	case "sealed":
		return "q_sealed_demo", true
	}
	return "", false
}

func TestExamples(t *testing.T) {
	root := repoRoot()
	exDir := filepath.Join(root, "example")
	entries, err := os.ReadDir(exDir)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("no example/ tree")
		}
		t.Fatal(err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			expectedPath := filepath.Join(exDir, name, "expected_run.txt")
			wantBytes, err := os.ReadFile(expectedPath)
			if err != nil {
				if os.IsNotExist(err) {
					t.Skipf("no expected_run.txt — example/%s is not yet pinned", name)
				}
				t.Fatal(err)
			}
			want := strings.TrimSpace(string(wantBytes))

			args := []string{"run", "-toolexec", qBin}
			// Per-example build tags — gates examples whose pre-rewrite
			// source can't typecheck under plain Go (e.g. q.Sealed
			// synthesises marker methods only the toolexec build sees).
			if tag, ok := exampleBuildTag(name); ok {
				args = append(args, "-tags", tag)
			}
			args = append(args, "./example/"+name)
			cmd := exec.Command("go", args...)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "GOCACHE="+goCache)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("example/%s failed: %v\n---\n%s", name, err, string(out))
			}
			got := strings.TrimSpace(string(out))
			if got != want {
				t.Fatalf("example/%s output mismatch.\nwant:\n%s\n---\ngot:\n%s", name, want, got)
			}
		})
	}
}
