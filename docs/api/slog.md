# `q.SlogAttr`, `q.SlogFile`, `q.SlogLine`

Production-grade `slog.Attr` builders that auto-derive their keys from compile-time information. Drop into shipping `slog.Info` / `slog.Error` / `slog.With` calls without retyping variable names as the slog key — and without polluting log output with debug-style `file:line` prefixes inside attr keys.

> Reaching for `dbg!`-style temporary instrumentation instead? See [`q.DebugPrintln` / `q.DebugSlogAttr`](debug.md). Those use the `file:line` prefix in keys so you can spot the source of a stray print at a glance — useful while debugging, noisy in production.

## Signatures

```go
func SlogAttr[T any](v T) slog.Attr
func SlogFile() slog.Attr
func SlogLine() slog.Attr
```

All three are rewritten by the preprocessor directly to stdlib `slog.Any` calls — no q runtime helper is on the value path. The `log/slog` import is auto-injected when any of these calls appear in a file.

## What each helper does

```go
slog.Info("loaded", q.SlogAttr(userID))
// → slog.Info("loaded", slog.Any("userID", userID))

slog.Info("located", q.SlogFile(), q.SlogLine())
// → slog.Info("located",
//        slog.Any("file", "main.go"),
//        slog.Any("line", 42))
```

| Helper                | Rewrite                                                  |
|-----------------------|----------------------------------------------------------|
| `q.SlogAttr(v)`       | `slog.Any("<source-text-of-v>", v)` — the literal text inside the parens becomes the key. Works for variables, indexes, expressions, anything. |
| `q.SlogFile()`        | `slog.Any("file", "<basename>")` — basename of the source file (e.g. `"main.go"`, not the full path). |
| `q.SlogLine()`        | `slog.Any("line", <line-number-as-int>)` — captured at compile time, so it's a constant in the binary. |

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

## Statement forms

All three are valid wherever a `slog.Attr` value is wanted — typically as varargs arguments to `slog.Info` / `slog.Error` / `slog.With`. Each is rewritten in place; nothing about q's other forms (define, assign, return, hoist) applies because these are pure expressions.

## See also

- [q.DebugPrintln / q.DebugSlogAttr](debug.md) — the dev-time / `dbg!`-style cousins. Same compile-time capture mechanism, different key shape.
- [q.Trace](trace.md) — compile-time `file:line` prefix on bubbled errors, the same idea applied to error wrapping.
