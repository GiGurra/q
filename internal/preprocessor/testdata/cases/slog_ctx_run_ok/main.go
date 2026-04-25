// Fixture: q.SlogCtx + q.SlogContextHandler + q.InstallSlogText.
// Verifies that:
//   - q.InstallSlogText wires up a handler that reads ctx-attached
//     slog.Attrs on every record
//   - q.SlogCtx accumulates attrs (later calls add to earlier ones)
//   - InfoContext picks up the accumulated attrs
//   - Without ctx-attrs, output is the same as a plain handler
//   - q.SlogAttr (compile-time auto-keyed) composes naturally as
//     the input to q.SlogCtx
package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	var buf bytes.Buffer

	// Install: text handler over the buffer, ctx-attr aware via the
	// q.SlogContextHandler wrap that InstallSlogText layers in.
	q.InstallSlogText(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})

	// 1) No ctx-attrs — just a plain log line.
	slog.InfoContext(context.Background(), "before-attach")

	// 2) Attach two attrs via q.SlogCtx, then log.
	reqID := "r-7"
	userID := 42
	ctx := q.SlogCtx(context.Background(),
		q.SlogAttr(reqID),
		q.SlogAttr(userID))
	slog.InfoContext(ctx, "with-attrs")

	// 3) Accumulate: nest another SlogCtx call adding more attrs.
	traceID := "t-99"
	ctx = q.SlogCtx(ctx, q.SlogAttr(traceID))
	slog.InfoContext(ctx, "after-accumulate")

	// 4) ctx without an InfoContext call (uses slog.Info instead) —
	// ctx-attrs are NOT auto-attached; only InfoContext / ErrorContext
	// (which carry ctx) trigger the handler's lookup.
	slog.Info("no-ctx-call")

	// 5) Empty SlogCtx call returns the input ctx unchanged.
	ctx2 := q.SlogCtx(context.Background())
	if ctx2 == context.Background() {
		fmt.Println("empty-slogctx: input returned unchanged")
	}

	// Dump captured slog output for assertion.
	fmt.Println("--- slog ---")
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		fmt.Println(line)
	}
}
