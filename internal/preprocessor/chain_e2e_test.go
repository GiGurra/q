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

// TestChainAndThen_InvokesSuccessor drives q with a real
// `-toolexec="<q> --and-then <shim>"` invocation and verifies TWO
// things, not just one:
//
//  1. A fixture with q.Try still builds and runs correctly (proves
//     q's rewriting survived the chain).
//  2. The shim actually ran (sentinel file non-empty). Without this
//     check, a regression where q silently dropped the successor on
//     some paths would still make the build pass — q's rewriting
//     alone is enough for the fixture to link and run.
//
// The shim is a POSIX shell script: append $1 (the tool path) to a
// log, then exec "$@" (passthrough). Proves the successor is on the
// exec path and that q handed the real Go tool off to it.
func TestChainAndThen_InvokesSuccessor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shim is sh-based")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("no sh on PATH: %v", err)
	}

	tmp := t.TempDir()
	sentinelPath := filepath.Join(tmp, "chain.log")
	shimPath := filepath.Join(tmp, "shim.sh")

	shim := "#!/bin/sh\nset -eu\nprintf '%s\\n' \"$1\" >> " + sentinelPath + "\nexec \"$@\"\n"
	if err := os.WriteFile(shimPath, []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}

	// Minimal fixture: uses bare q.Try so the rewriter has to fire
	// for the build to succeed. Print a marker on the success path
	// so we can also verify behavior survived unchanged.
	main := `package main

import (
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

func parse(s string) (int, error) {
	return q.Try(strconv.Atoi(s)), nil
}

func main() {
	v, err := parse("21")
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println("ok:", v*2)
}
`
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte(main), 0o644); err != nil {
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

	env := append(os.Environ(), "GOCACHE="+goCache)

	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = tmp
	tidy.Env = env
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}

	// -toolexec takes a single program-and-args string; Go tokenizes
	// on whitespace and prepends it to every tool call. q consumes
	// its own args up to the first --and-then, then hands the rest
	// off to the successor verbatim.
	toolexec := qBin + " --and-then " + shimPath
	build := exec.Command("go", "build", "-toolexec", toolexec, "-o", filepath.Join(tmp, "bin"), "./...")
	build.Dir = tmp
	build.Env = env
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("chained go build failed: %v\n---\n%s", err, out)
	}

	// Run the binary and check runtime behavior is unchanged.
	run := exec.Command(filepath.Join(tmp, "bin"))
	run.Env = env
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("running built binary failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "ok: 42" {
		t.Errorf("runtime output = %q, want %q", got, "ok: 42")
	}

	// Sentinel assertion: the shim was actually invoked by q.
	data, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("sentinel not written — shim never ran, so the chain dropped the successor: %v", err)
	}
	log := strings.TrimSpace(string(data))
	if log == "" {
		t.Fatal("sentinel empty — shim ran but logged nothing")
	}
	if !strings.Contains(log, string(os.PathSeparator)+"compile") {
		t.Fatalf("sentinel does not mention the compile tool — chain wiring suspect.\nlog:\n%s", log)
	}
}
