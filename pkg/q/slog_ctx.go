package q

// slog_ctx.go — context-aware slog attrs and the installers that
// pair with them.
//
// Pattern. Stdlib slog records know about a context.Context (via
// slog.InfoContext / ErrorContext / etc.), but doesn't pull attrs
// out of the ctx automatically — that's the job of a custom
// slog.Handler that reads ctx.Value(...) on every Handle. q
// supplies the ctx-attr key, the wrapping handler, and a few
// installers that wire it all up against stdlib slog.
//
// Usage. At process startup, install:
//
//	q.InstallSlogJSON(os.Stderr, nil)
//	// or
//	q.InstallSlog(myCustomHandler)
//
// In request flow, accumulate attrs:
//
//	ctx = q.SlogCtx(ctx, slog.Any("reqID", reqID), slog.Any("userID", userID))
//	slog.InfoContext(ctx, "processing")  // record auto-carries reqID + userID
//
// All helpers here are plain runtime functions — not rewritten by
// the preprocessor. They live in qRuntimeHelpers so a standalone
// q.InstallSlog(...) call doesn't trip the "unsupported q.* shape"
// scanner safety net.

import (
	"context"
	"io"
	"log/slog"
	"os"
)

// slogCtxKey is the private context key under which q.SlogCtx
// accumulates slog.Attrs. Defined as a struct{} so it cannot
// collide with any other package's context value.
type slogCtxKey struct{}

// SlogCtx returns a child context with the supplied slog.Attrs
// attached. Any handler produced by q.SlogContextHandler (and any
// logger using it as its handler) will pick these up via the
// passed context on slog.InfoContext / ErrorContext / etc.
//
// Repeated calls accumulate: the returned ctx carries every attr
// from prior calls plus the new ones in source order. If attrs is
// empty the input ctx is returned unchanged.
func SlogCtx(ctx context.Context, attrs ...slog.Attr) context.Context {
	if len(attrs) == 0 {
		return ctx
	}
	existing, _ := ctx.Value(slogCtxKey{}).([]slog.Attr)
	merged := make([]slog.Attr, 0, len(existing)+len(attrs))
	merged = append(merged, existing...)
	merged = append(merged, attrs...)
	return context.WithValue(ctx, slogCtxKey{}, merged)
}

// slogAttrsFromCtx pulls the accumulated slog.Attrs out of ctx, or
// nil if no q.SlogCtx call has run on this context branch.
func slogAttrsFromCtx(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}
	attrs, _ := ctx.Value(slogCtxKey{}).([]slog.Attr)
	return attrs
}

// SlogContextHandler wraps base so that every Handle call adds the
// slog.Attrs accumulated on the request context (via q.SlogCtx)
// to the record before forwarding. When the context carries no
// q-attached attrs, the handler is a transparent pass-through —
// equivalent to using base directly.
//
// Stack with whatever handler-level wrapping you need (sampling,
// async, redaction, etc.) — q.SlogContextHandler is just one more
// slog.Handler in the chain.
func SlogContextHandler(base slog.Handler) slog.Handler {
	return &slogContextHandler{base: base}
}

type slogContextHandler struct {
	base slog.Handler
}

func (h *slogContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

func (h *slogContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if attrs := slogAttrsFromCtx(ctx); len(attrs) > 0 {
		r.AddAttrs(attrs...)
	}
	return h.base.Handle(ctx, r)
}

func (h *slogContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &slogContextHandler{base: h.base.WithAttrs(attrs)}
}

func (h *slogContextHandler) WithGroup(name string) slog.Handler {
	return &slogContextHandler{base: h.base.WithGroup(name)}
}

// InstallSlog wraps base with q.SlogContextHandler and installs the
// result as slog's default logger (slog.SetDefault). After this
// call, any package-level slog.InfoContext / ErrorContext / etc.
// will pick up attrs added to the ctx via q.SlogCtx.
//
// Call once at process startup. The underlying base handler
// remains in your control — q just adds the ctx-attr lookup on
// top.
func InstallSlog(base slog.Handler) {
	slog.SetDefault(slog.New(SlogContextHandler(base)))
}

// InstallSlogJSON is sugar for InstallSlog(slog.NewJSONHandler(w, opts)).
// Both arguments may be nil — w defaults to os.Stderr, opts to
// stdlib defaults.
func InstallSlogJSON(w io.Writer, opts *slog.HandlerOptions) {
	if w == nil {
		w = os.Stderr
	}
	InstallSlog(slog.NewJSONHandler(w, opts))
}

// InstallSlogText is sugar for InstallSlog(slog.NewTextHandler(w, opts)).
// Both arguments may be nil — w defaults to os.Stderr, opts to
// stdlib defaults.
func InstallSlogText(w io.Writer, opts *slog.HandlerOptions) {
	if w == nil {
		w = os.Stderr
	}
	InstallSlog(slog.NewTextHandler(w, opts))
}
