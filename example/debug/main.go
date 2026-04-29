// example/debug mirrors docs/api/debug.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/debug
//
// Both q.DebugWriter and slog are wired to stdout (with file:line
// stripped from DebugPrintln output and the time attr stripped from
// slog) so the assertion in expected_run.txt is deterministic
// regardless of where the example is built from.
package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"regexp"

	"github.com/GiGurra/q/pkg/q"
)

// dbgBuf captures q.DebugPrintln output. We strip the per-build file
// path prefix in main() so the line says e.g. "main.go:NN id = 7"
// rather than "/tmp/.../main.go:NN id = 7".
var dbgBuf bytes.Buffer

func init() {
	q.DebugWriter = &dbgBuf

	h := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	slog.SetDefault(slog.New(h))
}

func loadUser(id int) string { return fmt.Sprintf("user-%d", id) }

// ---------- "What q.DebugPrintln does" ----------
//
//	u := loadUser(q.DebugPrintln(id))
func debugPrintlnDemo(id int) string {
	u := loadUser(q.DebugPrintln(id))
	return u
}

// "Useful when debugging arithmetic":
//
//	q.DebugPrintln(n*2 + offset)
func debugArithmetic(n, offset int) {
	q.DebugPrintln(n*2 + offset)
}

// ---------- "What q.DebugSlogAttr does" ----------
//
//	slog.Info("loaded", q.DebugSlogAttr(userID))
func debugSlogAttrDemo(userID int) {
	slog.Info("loaded", q.DebugSlogAttr(userID))
}

// ---------- "Production counterparts — q.SlogAttr / SlogFile / SlogLine" ----------
//
//	slog.Info("step", q.SlogAttr(intermediate), q.SlogFile(), q.SlogLine())
func productionSlog(intermediate int) {
	slog.Info("step", q.SlogAttr(intermediate), q.SlogFile(), q.SlogLine())
}

// ---------- "Nesting — q.Try around q.DebugPrintln" ----------
//
//	return q.Try(loadUser(q.DebugPrintln(id)))
func nestingDemo(id int) (string, error) {
	return loadUser(q.DebugPrintln(id)), nil
}

func main() {
	fmt.Println("loaded:", debugPrintlnDemo(7))

	debugArithmetic(3, 5)

	debugSlogAttrDemo(42)

	productionSlog(99)

	if u, err := nestingDemo(11); err != nil {
		fmt.Printf("nesting: err=%s\n", err)
	} else {
		fmt.Println("nesting:", u)
	}

	// Print the captured DebugPrintln output with absolute paths
	// stripped down to the basename so the assertion is portable.
	cleaned := stripPath(dbgBuf.String())
	fmt.Print("---\nDebugWriter output:\n", cleaned)
}

// stripPath replaces "/path/to/main.go" with "main.go" so the
// expected_run.txt assertion is build-location independent. Likewise
// for the slog file=… and line=… attrs printed via q.SlogFile /
// q.SlogLine — they appear directly on stdout via the slog handler
// configured in init.
var pathRE = regexp.MustCompile(`/[\w./-]+/main\.go`)

func stripPath(s string) string { return pathRE.ReplaceAllString(s, "main.go") }
