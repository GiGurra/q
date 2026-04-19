package preprocessor

import (
	"path/filepath"
	"reflect"
	"testing"
)

// toolPath returns an absolute path that points into the real
// $GOROOT/pkg/tool/$GOOS_$GOARCH/ directory, which is what
// findGoToolIndex accepts as a Go tool location.
func toolPath(name string) string {
	return filepath.Join(goToolDir(), name)
}

func TestParseChain_Classic(t *testing.T) {
	_, chain, tool, toolArgs, ok := parseChain([]string{toolPath("compile"), "-p", "foo", "a.go"})
	if !ok {
		t.Fatalf("parse failed")
	}
	if len(chain.NextCmd) != 0 {
		t.Errorf("expected no chain, got %v", chain.NextCmd)
	}
	if tool != toolPath("compile") {
		t.Errorf("tool=%q", tool)
	}
	if !reflect.DeepEqual(toolArgs, []string{"-p", "foo", "a.go"}) {
		t.Errorf("toolArgs=%v", toolArgs)
	}
}

func TestParseChain_OneHop(t *testing.T) {
	args := []string{
		"--and-then", "rewire",
		toolPath("compile"), "-p", "foo", "a.go",
	}
	_, chain, tool, toolArgs, ok := parseChain(args)
	if !ok {
		t.Fatalf("parse failed")
	}
	if !reflect.DeepEqual(chain.NextCmd, []string{"rewire"}) {
		t.Errorf("chain=%v", chain.NextCmd)
	}
	if tool != toolPath("compile") {
		t.Errorf("tool=%q", tool)
	}
	if !reflect.DeepEqual(toolArgs, []string{"-p", "foo", "a.go"}) {
		t.Errorf("toolArgs=%v", toolArgs)
	}
}

func TestParseChain_MultiHop(t *testing.T) {
	args := []string{
		"--q-flag",
		"--and-then", "rewire", "--rewire-flag",
		"--and-then", "third", "--third-flag",
		toolPath("compile"), "-p", "foo", "a.go",
	}
	qArgs, chain, tool, toolArgs, ok := parseChain(args)
	if !ok {
		t.Fatalf("parse failed")
	}
	if !reflect.DeepEqual(qArgs, []string{"--q-flag"}) {
		t.Errorf("qArgs=%v", qArgs)
	}
	wantChain := []string{"rewire", "--rewire-flag", "--and-then", "third", "--third-flag"}
	if !reflect.DeepEqual(chain.NextCmd, wantChain) {
		t.Errorf("chain=%v want=%v", chain.NextCmd, wantChain)
	}
	if tool != toolPath("compile") {
		t.Errorf("tool=%q", tool)
	}
	if !reflect.DeepEqual(toolArgs, []string{"-p", "foo", "a.go"}) {
		t.Errorf("toolArgs=%v", toolArgs)
	}
}

func TestParseChain_AsmTool(t *testing.T) {
	args := []string{
		"--and-then", "rewire",
		toolPath("asm"), "input.s",
	}
	_, chain, tool, toolArgs, ok := parseChain(args)
	if !ok {
		t.Fatalf("parse failed")
	}
	if !reflect.DeepEqual(chain.NextCmd, []string{"rewire"}) {
		t.Errorf("chain=%v", chain.NextCmd)
	}
	if tool != toolPath("asm") {
		t.Errorf("tool=%q", tool)
	}
	if !reflect.DeepEqual(toolArgs, []string{"input.s"}) {
		t.Errorf("toolArgs=%v", toolArgs)
	}
}

func TestParseChain_AbsolutePrePath(t *testing.T) {
	args := []string{
		"--and-then", "/usr/local/bin/rewire", "--flag",
		toolPath("compile"), "-p", "foo",
	}
	_, chain, tool, _, ok := parseChain(args)
	if !ok {
		t.Fatalf("parse failed")
	}
	if !reflect.DeepEqual(chain.NextCmd, []string{"/usr/local/bin/rewire", "--flag"}) {
		t.Errorf("chain=%v", chain.NextCmd)
	}
	if tool != toolPath("compile") {
		t.Errorf("tool=%q", tool)
	}
}

func TestParseChain_FlagValueNamedLikeTool(t *testing.T) {
	// Regression: a preprocessor flag value like `--output /tmp/compile`
	// must NOT be misclassified as the go-compile tool. The real
	// tool is under $GOROOT/pkg/tool/..., and the directory check in
	// findGoToolIndex must skip the bogus path.
	args := []string{
		"--and-then", "instrumenter", "--output", "/tmp/compile",
		toolPath("compile"), "-p", "foo",
	}
	_, chain, tool, _, ok := parseChain(args)
	if !ok {
		t.Fatalf("parse failed")
	}
	wantChain := []string{"instrumenter", "--output", "/tmp/compile"}
	if !reflect.DeepEqual(chain.NextCmd, wantChain) {
		t.Errorf("chain=%v want=%v", chain.NextCmd, wantChain)
	}
	if tool != toolPath("compile") {
		t.Errorf("tool=%q — decoy path won; real tool misidentified", tool)
	}
}

func TestParseChain_ExeSuffix(t *testing.T) {
	// Verify .exe basename stripping on a POSIX absolute path; true
	// Windows paths fail filepath.IsAbs on Linux, but the stripping
	// code runs identically on any OS so POSIX is sufficient.
	args := []string{
		"--and-then", "rewire",
		toolPath("compile.exe"), "-p", "foo",
	}
	_, _, tool, _, ok := parseChain(args)
	if !ok {
		t.Fatalf("parse failed")
	}
	if tool != toolPath("compile.exe") {
		t.Errorf("tool=%q", tool)
	}
}

func TestParseChain_NoToolAfterAndThen(t *testing.T) {
	args := []string{"--and-then", "rewire", "--flag"}
	_, _, _, _, ok := parseChain(args)
	if ok {
		t.Fatalf("expected parse failure for chain with no tool")
	}
}
