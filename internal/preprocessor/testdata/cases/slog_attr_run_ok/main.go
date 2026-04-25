// Fixture: q.SlogAttr / q.SlogFile / q.SlogLine — production-grade
// slog helpers that auto-derive the attr key from compile-time
// information.
//
//   - q.SlogAttr(v) → slog.Any("<src-text-of-v>", v)
//   - q.SlogFile()  → slog.Any("file", "<basename>")
//   - q.SlogLine()  → slog.Any("line", <line-number>)
//
// Captured output is normalised: the line number after "line=" is
// rewritten to "N" so the expected_run.txt stays stable across edits.
package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	var buf bytes.Buffer

	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	logger := slog.New(handler)

	id := 7
	name := "alice"

	// Source text as the slog key — no file:line prefix.
	logger.Info("loaded", q.SlogAttr(id))

	// Expressions become the key verbatim.
	logger.Info("doubled", q.SlogAttr(id*2))

	// Several attrs at once.
	logger.Info("pair", q.SlogAttr(id), q.SlogAttr(name))

	// q.SlogFile + q.SlogLine — location info as separate attrs.
	logger.Info("located", q.SlogFile(), q.SlogLine())

	// Mix all three.
	logger.Info("mixed",
		q.SlogAttr(id),
		q.SlogFile(),
		q.SlogLine())

	fmt.Println("--- slog ---")
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		fmt.Println(stripLineNumber(line))
	}
}

// stripLineNumber replaces the integer after "line=" with "N" so
// the expected output is stable across source edits.
var lineRe = regexp.MustCompile(`line=\d+`)

func stripLineNumber(s string) string {
	return lineRe.ReplaceAllString(s, "line=N")
}
