// Fixture: q.File / q.Line / q.FileLine / q.SlogFile / q.SlogLine /
// q.SlogFileLine all report the ORIGINAL source position, not the
// preprocessor-rewritten one.
//
// The trick: q.Try / q.Check expand at compile time into multi-line
// blocks (bind + check + bubble). After such an expansion, the
// rewritten file's line numbers are SHIFTED relative to the original.
// If the position helpers were wired to the rewritten file's
// positions, q.Line() called downstream of a q.Try would report a
// shifted line. The fixture pins exact line numbers.
package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	if err := positions(); err != nil {
		fmt.Println("ERR:", err)
	}

	// slog assertions in a separate function so its q.Try doesn't
	// shift positions() above.
	if err := slogPositions(); err != nil {
		fmt.Println("ERR slog:", err)
	}
}

// positions exercises every primitive position helper. q.Try and
// q.Check sit between calls to q.Line / q.File / q.FileLine to inject
// rewriter-induced line skew — the helpers MUST report the original
// source line, not the post-rewrite line.
func positions() error {
	// Line numbers below are pinned. Line 39 is q.Line.
	fmt.Println("Line:", q.Line())

	// q.Try expansion adds ~3 lines after rewrite.
	_ = q.Try(strconv.Atoi("42"))

	// Despite the q.Try expansion above shifting rewritten-file
	// positions, q.File / q.Line / q.FileLine here still report the
	// ORIGINAL source line (this comment + 2 are line 47).
	fmt.Println("File:", q.File())
	fmt.Println("Line2:", q.Line())
	fmt.Println("FileLine:", q.FileLine())

	// More expansion via q.Check.
	q.Check(noErr())

	// Final pin.
	fmt.Println("Line3:", q.Line())
	return nil
}

func slogPositions() error {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))

	// Line 75: q.SlogLine here.
	logger.Info("first", q.SlogLine(), q.SlogFile())

	_ = q.Try(strconv.Atoi("99")) // expands

	// Line 79 below — original. After the q.Try above, rewritten
	// position would be ~+3.
	logger.Info("second", q.SlogLine(), q.SlogFileLine())

	fmt.Println("--- slog ---")
	for _, line := range bytesLines(buf) {
		fmt.Println(line)
	}
	return nil
}

func noErr() error { return nil }

func bytesLines(buf bytes.Buffer) []string {
	s := buf.String()
	// Trim trailing newline, then split.
	if len(s) > 0 && s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
