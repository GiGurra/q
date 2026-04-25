package preprocessor_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// End-to-end harness for the q preprocessor.
//
// Each directory under testdata/cases is a fixture:
//
//	testdata/cases/<name>/
//	    *.go                 # source files copied into a fresh tempdir
//	    expected_build.txt   # optional:
//	                         #   absent / empty -> build must succeed
//	                         #   non-empty      -> build must fail AND combined
//	                         #                     output must contain EVERY
//	                         #                     non-empty line as a substring
//	                         #                     (one per line; #-prefixed
//	                         #                     lines are comments)
//	    expected_run.txt     # optional, only meaningful when build succeeds:
//	                         #   absent     -> nothing is run
//	                         #   present    -> `go run ./...` from fixture root,
//	                         #                 stdout must equal the file content
//	                         #                 (whitespace-trimmed)
//
// The harness:
//   1. Builds cmd/q once (TestMain).
//   2. For each fixture, creates a tempdir, copies *.go files, writes a
//      synthesized go.mod with a local replace of github.com/GiGurra/q,
//      runs `go mod tidy` and `go build -toolexec=<binary> ./...`, then
//      optionally `go run ./...` to assert runtime behavior.
//
// Mirrors proven/internal/preprocessor/e2e_test.go closely; see that
// file's header for the rationale behind the isolated GOCACHE.

var (
	qBin    string
	goCache string
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "q-e2e-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to create tempdir:", err)
		os.Exit(1)
	}
	qBin = filepath.Join(dir, "q")

	// Isolated GOCACHE per harness run. Go's build-cache key does not
	// include toolexec behavior, so a prior plain build of pkg/q would
	// leave a stub-less .a in the host cache — fixture builds would
	// then fail to link against _q_atCompileTime non-deterministically
	// depending on host state. Same trick proven uses.
	goCache = filepath.Join(dir, "gocache")
	if err := os.MkdirAll(goCache, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "failed to create gocache:", err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}

	cmd := exec.Command("go", "build", "-o", qBin, "./cmd/q")
	cmd.Dir = repoRoot()
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintln(os.Stderr, "failed to build cmd/q:")
		fmt.Fprintln(os.Stderr, string(out))
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestFixtures(t *testing.T) {
	casesDir := filepath.Join(repoRoot(), "internal", "preprocessor", "testdata", "cases")
	entries, err := os.ReadDir(casesDir)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("no fixtures yet")
		}
		t.Fatal(err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			runFixture(t, filepath.Join(casesDir, name))
		})
	}
}

func runFixture(t *testing.T, fixtureDir string) {
	t.Helper()
	tmp := t.TempDir()

	if err := copyGoTree(fixtureDir, tmp); err != nil {
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

	if out, err := runIn(tmp, "go", "mod", "tidy"); err != nil {
		t.Fatalf("go mod tidy failed: %v\n---\n%s", err, out)
	}

	buildOut, buildErr := runIn(tmp, "go", "build", "-toolexec", qBin, "./...")
	gotBuild := strings.TrimSpace(buildOut)

	wantBuild := readOptional(filepath.Join(fixtureDir, "expected_build.txt"))

	if wantBuild == "" {
		if buildErr != nil {
			t.Fatalf("expected build to succeed; got error: %v\n---\n%s", buildErr, gotBuild)
		}
	} else {
		if buildErr == nil {
			t.Fatalf("expected build to fail (substrings %q) but it succeeded.\noutput:\n%s", wantBuild, gotBuild)
		}
		// Every non-empty, non-comment line in expected_build.txt is a
		// required substring — supports asserting on multi-problem
		// diagnostics (e.g. q.Assemble's combined-errors output) where
		// each problem is one line.
		for _, want := range parseExpectedSubstrings(wantBuild) {
			if !strings.Contains(gotBuild, want) {
				t.Fatalf("build failed but output missing expected substring.\nwant:\n%s\n---\ngot:\n%s", want, gotBuild)
			}
		}
		// build was expected to fail; nothing to run.
		return
	}

	wantRun, hasRun := readOptionalRaw(filepath.Join(fixtureDir, "expected_run.txt"))
	if !hasRun {
		return
	}

	runOut, _ := runIn(tmp, "go", "run", "-toolexec", qBin, "./...")
	gotRun := strings.TrimSpace(runOut)
	wantRunTrim := strings.TrimSpace(wantRun)
	if gotRun != wantRunTrim {
		t.Fatalf("run output mismatch.\nwant:\n%s\n---\ngot:\n%s", wantRunTrim, gotRun)
	}
}

// parseExpectedSubstrings splits the expected_build.txt content into
// the list of substrings that must each appear in the build output.
// Each non-empty, non-comment line is one required substring. Lines
// starting with `#` are treated as comments. Empty input returns an
// empty slice — caller's responsibility to skip the whole assertion
// when the file is absent or blank.
func parseExpectedSubstrings(s string) []string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

func readOptional(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readOptionalRaw(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func copyGoTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func runIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOCACHE="+goCache)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
