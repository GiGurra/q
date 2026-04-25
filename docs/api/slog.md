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

## See also

- [q.DebugPrintln / q.DebugSlogAttr](debug.md) — the dev-time / `dbg!`-style cousins. Same compile-time capture mechanism, different key shape.
- [q.Trace](trace.md) — compile-time `file:line` prefix on bubbled errors, the same idea applied to error wrapping.
