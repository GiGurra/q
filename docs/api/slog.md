# Compile-time info: `q.SlogAttr` / `File` / `Line` / `Expr`

Two related families, both compile-time captures of source-level information:

- **Slog* family** — production-grade `slog.Attr` builders that auto-derive keys from source. Drop into shipping `slog.Info` / `slog.Error` / `slog.With` calls without retyping variable names as the slog key.
- **Plain-string / int family** — primitive returns (`string`, `int`) for use anywhere a literal value is wanted: error messages, labels, custom log formatters.

> Reaching for `dbg!`-style temporary instrumentation instead? See [`q.DebugPrintln` / `q.DebugSlogAttr`](debug.md). Those use the `file:line` prefix in keys so you can spot the source of a stray print at a glance — useful while debugging, noisy in production.

## Signatures

```go
func SlogAttr[T any](v T) slog.Attr
func SlogFile() slog.Attr
func SlogLine() slog.Attr
func SlogFileLine() slog.Attr

// Plain-string / int counterparts (not slog.Attr):
func File() string                 // basename, e.g. "main.go"
func Line() int                    // 42
func FileLine() string             // "main.go:42"
func Expr[T any](v T) string       // literal source text of v; v's value discarded
```

All are rewritten by the preprocessor at compile time. The Slog* family expands to stdlib `slog.Any` (with auto-injected `log/slog` import); the primitive-typed ones expand to plain Go literals (`"main.go"`, `42`, `"a + b"`).

## What each helper does

```go
slog.Info("loaded", q.SlogAttr(userID))
// → slog.Info("loaded", slog.Any("userID", userID))

slog.Info("located", q.SlogFile(), q.SlogLine())
// → slog.Info("located",
//        slog.Any("file", "main.go"),
//        slog.Any("line", 42))

slog.Info("located", q.SlogFileLine())
// → slog.Info("located", slog.Any("file", "main.go:42"))
```

| Helper                  | Rewrite                                                  |
|-------------------------|----------------------------------------------------------|
| `q.SlogAttr(v)`         | `slog.Any("<source-text-of-v>", v)` — the literal text inside the parens becomes the key. Works for variables, indexes, expressions, anything. |
| `q.SlogFile()`          | `slog.Any("file", "<basename>")` — basename of the source file (e.g. `"main.go"`, not the full path). |
| `q.SlogLine()`          | `slog.Any("line", <line-number-as-int>)` — captured at compile time, so it's a constant in the binary. |
| `q.SlogFileLine()`      | `slog.Any("file", "<basename>:<line>")` — combined location string in one attr; common Go log format. |
| `q.File()`              | `"<basename>"` — plain string literal. |
| `q.Line()`              | `<line-number>` — plain integer literal. |
| `q.FileLine()`          | `"<basename>:<line>"` — plain string literal. |
| `q.Expr(v)`             | `"<source-text-of-v>"` — plain string literal. **Argument's runtime value is discarded** — only the literal source text is captured. |

## Key-text examples

```go
slog.Info("debug",
    q.SlogAttr(id),                    // key: "id"
    q.SlogAttr(user.Email),            // key: "user.Email"
    q.SlogAttr(items[index]),          // key: "items[index]"
    q.SlogAttr(n * 2),                 // key: "n * 2"
    q.SlogAttr(time.Since(start)))     // key: "time.Since(start)"
```

The key is whatever you wrote between the parens, byte-for-byte. Pick a spelling at the call site that reads well as a slog key.

## When to use which

- **`q.SlogAttr`** — anywhere you'd otherwise type `slog.Any("varname", varname)`. The whole point: stop retyping the variable name.
- **`q.SlogFile` / `q.SlogLine`** — when you want call-site location as separate, parseable attrs. Both are constants captured at compile time, so there's no runtime stack walk (unlike `runtime.Caller`).

```go
// dev-time, "where exactly did this fire?" — file:line in key:
slog.Info("step", q.DebugSlogAttr(intermediate))

// production, "log where this was emitted" — file:line as separate attrs:
slog.Info("request handled",
    q.SlogAttr(reqID),
    q.SlogAttr(elapsed),
    q.SlogFile(),
    q.SlogLine())
```

## `q.Expr` — capture an expression's source text without evaluating it

Useful in error messages or labels where you want the exact source spelling of a check that just failed:

```go
if !condition {
    return fmt.Errorf("check failed at %s: %s", q.FileLine(), q.Expr(condition))
}
// → fmt.Errorf("check failed at %s: %s", "main.go:42", "condition")
```

The value of the argument is discarded — `q.Expr(sideEffect())` does NOT call `sideEffect()` at runtime; the rewriter folds the call site to the literal string `"sideEffect()"`. This is the one helper in the family where the argument is consumed by the rewriter, not the runtime.

## Statement forms

The `Slog*` family is valid wherever a `slog.Attr` value is wanted — typically as varargs arguments to `slog.Info` / `slog.Error` / `slog.With`. The primitive-typed family (`q.File` / `q.Line` / `q.FileLine` / `q.Expr`) is valid wherever a string or int literal is. Each is rewritten in place; nothing about q's other forms (define, assign, return, hoist) applies because these are pure expressions.

## Context-aware logging

Three helpers wire up "every log call through this request automatically picks up the request's correlation ID / user ID / etc.":

```go
func SlogCtx(ctx context.Context, attrs ...slog.Attr) context.Context
func SlogContextHandler(base slog.Handler) slog.Handler

func InstallSlog(base slog.Handler)                                    // generic
func InstallSlogJSON(w io.Writer, opts *slog.HandlerOptions)            // sugar
func InstallSlogText(w io.Writer, opts *slog.HandlerOptions)            // sugar
```

Pure runtime helpers (no preprocessor magic). The pattern is the standard Go one: a wrapping `slog.Handler` reads attrs from `ctx.Value(...)` on every record. q gives you the ctx key, the wrapping handler, and three installers for the most common cases.

### Setup

Once at process startup, install a logger:

```go
// JSON to stderr, default options:
q.InstallSlogJSON(nil, nil)

// Text to a file with custom options:
q.InstallSlogText(logFile, &slog.HandlerOptions{Level: slog.LevelDebug})

// Or your own base handler:
q.InstallSlog(myCustomHandler)
```

All three call `slog.SetDefault(slog.New(q.SlogContextHandler(base)))`. The base handler is yours; q just adds the ctx-attr lookup on top.

### Per-request attr accumulation

Anywhere in request flow, attach attrs to the context:

```go
ctx = q.SlogCtx(ctx,
    q.SlogAttr(reqID),
    q.SlogAttr(userID))

slog.InfoContext(ctx, "processing")
// → record automatically carries reqID + userID
```

Repeat calls accumulate — a deeper `q.SlogCtx` adds attrs on top of whatever the parent context already had:

```go
ctx = q.SlogCtx(ctx, q.SlogAttr(traceID))
slog.InfoContext(ctx, "step done")
// → record carries reqID + userID + traceID (in source order)
```

The ctx is propagated as usual via Go's normal `context.Context` flow — through goroutines, RPC client middlewares, http.Request.WithContext, etc. Anywhere that ctx travels, the attrs travel with it.

### Caveat: only `*Context` slog calls trigger the lookup

`slog.Info(...)` does not carry a `context.Context`, so the handler has no way to find ctx-attrs. Use `slog.InfoContext(ctx, ...)` / `slog.ErrorContext(ctx, ...)` / etc. when you want the auto-attach.

### Composing with other handlers

q.SlogContextHandler is just a `slog.Handler` that wraps another. You can layer it with other wrappers (sampling, redaction, async) by nesting:

```go
q.InstallSlog(samplingHandler(redactingHandler(slog.NewJSONHandler(os.Stderr, nil))))
// q's ctx-attr lookup runs first, then sampling, then redaction.
```

If you want q's ctx-attr lookup *under* another wrapper instead of on top, build the chain manually:

```go
slog.SetDefault(slog.New(samplingHandler(q.SlogContextHandler(base))))
```

## See also

- [q.DebugPrintln / q.DebugSlogAttr](debug.md) — the dev-time / `dbg!`-style cousins. Same compile-time capture mechanism, different key shape.
- [q.Trace](trace.md) — compile-time `file:line` prefix on bubbled errors, the same idea applied to error wrapping.
