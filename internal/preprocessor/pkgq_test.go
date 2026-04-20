package preprocessor_test

// Runs pkg/q's own unit tests under `-toolexec=<qBin>`. The link
// gate in pkg/q (see `_qLink` / `_q_atCompileTime`) prevents a
// plain `go test ./pkg/q/...` from linking; this harness entry
// piggybacks on the TestMain-built cmd/q binary so real go-test
// runtime tests (like TestToErr_*) have a place to execute.
//
// Failures in the subprocess show up as this test's failure; the
// combined output of `go test` goes straight to the parent's log
// so individual failing TestToErr_* functions remain identifiable.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageQUnit(t *testing.T) {
	pkgDir := filepath.Join(repoRoot(), "pkg", "q")
	out, err := runIn(pkgDir, "go", "test", "-toolexec", qBin, "-tags", "qtoolexec", "-count=1", ".")
	if err != nil {
		t.Fatalf("pkg/q unit tests failed under -toolexec=q: %v\n---\n%s", err, strings.TrimSpace(out))
	}
}
