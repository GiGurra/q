// Fixture: q.DebugSlogAttr — auto-keyed slog.Attr. The
// preprocessor rewrites `q.DebugSlogAttr(x)` into
// `slog.Any("<file>:<line> <src>", x)`, expanding directly to
// stdlib slog (no q runtime helper involved). We feed a
// slog.TextHandler with a buffer destination and a ReplaceAttr that
// normalises the file:line into "N" so the captured output is
// stable across source edits.
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

	// Drop the time attr so the captured output is stable. Other
	// attrs (level, msg, our auto-keyed ones) flow through.
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

	// Bare call — auto-keyed by source text "id".
	logger.Info("loaded", q.DebugSlogAttr(id))

	// Expression as the arg — auto-keyed by source text "id*2".
	logger.Info("doubled", q.DebugSlogAttr(id*2))

	// Inside slog.Error too — same shape.
	logger.Error("failed", q.DebugSlogAttr(name))

	// Multiple in one call — both attrs land.
	logger.Info("pair", q.DebugSlogAttr(id), q.DebugSlogAttr(name))

	fmt.Println("--- slog ---")
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		fmt.Println(stripLineNumber(line))
	}
}

// stripLineNumber replaces every "main.go:<digits>" occurrence with
// "main.go:N" so the captured slog output is stable.
var lineRe = regexp.MustCompile(`main\.go:\d+`)

func stripLineNumber(s string) string {
	return lineRe.ReplaceAllString(s, "main.go:N")
}
