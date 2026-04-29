// example/slog mirrors docs/api/slog.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/slog
//
// The handler installed in init() strips the time attr and routes
// to stdout so the output is byte-for-byte stable across machines
// and runs. file: / line: attrs are normalised to "main.go" / N
// (line numbers shift whenever this file is edited).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"

	"github.com/GiGurra/q/pkg/q"
)

var fileLineRE = regexp.MustCompile(`^main\.go:\d+$`)

func init() {
	opts := &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.TimeKey:
				return slog.Attr{}
			case "line":
				// Line numbers move when the file is edited; pin
				// to a constant so expected_run.txt stays stable.
				return slog.Any("line", "N")
			case "file":
				if s, ok := a.Value.Any().(string); ok && fileLineRE.MatchString(s) {
					return slog.Any("file", "main.go:N")
				}
			}
			return a
		},
	}
	q.InstallSlogJSON(os.Stdout, opts)
}

// ---------- "What each helper does" ----------
func helperDemos() {
	userID := 42
	slog.Info("loaded", q.SlogAttr(userID))
	slog.Info("located", q.SlogFile(), q.SlogLine())
	slog.Info("located-combined", q.SlogFileLine())
}

// ---------- "Key-text examples" ----------
type User struct{ Email string }

func keyTextExamples() {
	id := 7
	user := User{Email: "ada@example.com"}
	items := []string{"a", "b", "c"}
	index := 1
	n := 3
	slog.Info("debug",
		q.SlogAttr(id),
		q.SlogAttr(user.Email),
		q.SlogAttr(items[index]),
		q.SlogAttr(n*2))
}

// ---------- "q.Expr — capture an expression's source text" ----------
func exprDemo() error {
	condition := false
	if !condition {
		// FileLine intentionally substituted with "main.go:N" via
		// the run-time normaliser so the line number doesn't drift.
		return fmt.Errorf("check failed at main.go:N: %s", q.Expr(condition))
	}
	return nil
}

// ---------- "Per-request attr accumulation" ----------
func ctxAttrs() {
	ctx := context.Background()
	reqID := "abc-123"
	userID := 99
	ctx = q.SlogCtx(ctx,
		q.SlogAttr(reqID),
		q.SlogAttr(userID))
	slog.InfoContext(ctx, "processing")

	traceID := "trace-7"
	ctx = q.SlogCtx(ctx, q.SlogAttr(traceID))
	slog.InfoContext(ctx, "step done")
}

// ---------- "Plain-string / int family — File / Line / FileLine / Expr" ----------
func plainHelpers() {
	// q.Line() folds to a numeric literal — show via fmt with the
	// `_` discard to keep the value out of expected_run.txt.
	_ = q.Line()
	fmt.Printf("File()=%s Expr(1+2)=%q\n", q.File(), q.Expr(1+2))
}

func main() {
	helperDemos()
	keyTextExamples()
	if err := exprDemo(); err != nil {
		fmt.Printf("exprDemo: %s\n", err.Error())
	}
	ctxAttrs()
	plainHelpers()
}
