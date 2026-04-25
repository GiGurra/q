# `q.DebugPrintln`, `q.DebugPrintlnAt`, `q.DebugSlogAttr`

The **Debug** family is for **dev-time / temporary** instrumentation — quick `dbg!`-style probes you sprinkle into code while figuring something out, then pull back out before shipping. The keys / labels include the call-site `file:line` to make the output unambiguous when a print or slog attr ends up on stderr without surrounding context.

> **For production logging, reach for [`q.SlogAttr` / `q.SlogFile` / `q.SlogLine`](#production-counterparts) instead.** Those produce clean attr keys (just the source text — no `file:line` prefix), suitable for the structured logs your service actually ships.

The Debug family auto-captures the source text and `file:line` of their argument at compile time, so you don't have to retype it as a label:

- **`q.DebugPrintln`** — Go's missing `dbg!` / `println!`. Prints the value with its source text and call site, returns it unchanged so the call can sit mid-expression.
- **`q.DebugSlogAttr`** — produces a `slog.Attr` keyed by the same auto-generated label. Use inside `slog.Info` / `slog.Error` / etc. to attach a labelled value to a structured-log call.

## Signatures

```go
func DebugPrintln[T any](v T) T
func DebugPrintlnAt[T any](label string, v T) T
func DebugSlogAttr[T any](v T) slog.Attr

var DebugWriter io.Writer = os.Stderr
```

`q.DebugPrintln(x)` is rewritten by the preprocessor to `q.DebugPrintlnAt("<file>:<line> <src-text>", x)`. `q.DebugPrintlnAt` is a plain runtime function — call it directly when you want a custom label.

`q.DebugSlogAttr(x)` is rewritten directly to `slog.Any("<file>:<line> <src-text>", x)`. There is no q runtime helper on the value path — the rewrite expands to stdlib slog.

## What `q.DebugPrintln` does

```go
u := loadUser(q.DebugPrintln(id))
```

rewrites to (say, `main.go` line 17):

```go
u := loadUser(q.DebugPrintlnAt("main.go:17 id", id))
// which at runtime prints to DebugWriter:
// main.go:17 id = 7
// and returns 7 unchanged, so loadUser sees the same value.
```

The `<src-text>` is the *literal source* of the argument — whatever you wrote between the parens. Useful when debugging arithmetic:

```go
q.DebugPrintln(n*2 + offset)   // prints "n*2 + offset = <value>"
```

## What `q.DebugSlogAttr` does

```go
slog.Info("loaded", q.DebugSlogAttr(userID))
```

rewrites to:

```go
slog.Info("loaded", slog.Any("main.go:17 userID", userID))
// → captured by your slog handler as a key-value pair with the
//   label `main.go:17 userID` mapping to the userID's value.
```

Use it whenever you want a *temporary* structured-log line that includes a value with location info baked into the key. For permanent / production-grade slog calls, prefer `q.SlogAttr` (clean key without the `file:line` prefix) — see below.

`q.DebugSlogAttr` does not pass the value through (it returns `slog.Attr`, not `T`). If you want mid-expression instrumentation, use `q.DebugPrintln`.

## Production counterparts

These are documented at [q.SlogAttr / q.SlogFile / q.SlogLine](slog.md):

- **`q.SlogAttr(v)`** — `slog.Any("v", v)`. Just the source text as the key. No file:line. Drop into shipping code.
- **`q.SlogFile()`** — `slog.Any("file", "main.go")`.
- **`q.SlogLine()`** — `slog.Any("line", 42)`.

The split is deliberate: Debug-flavored helpers carry call-site identity in the *key text* itself (which is noisy in production logs but unmistakable in stderr scrolling); the Slog-flavored helpers keep keys clean and let you attach location info as separate attrs only when you want it.

```go
// dev-time, while figuring out a bug:
slog.Info("step", q.DebugSlogAttr(intermediate))
// → key: "main.go:42 intermediate"

// production-grade equivalent:
slog.Info("step",
    q.SlogAttr(intermediate),
    q.SlogFile(),
    q.SlogLine())
// → keys: "intermediate", "file", "line"
```

## Configurable destination for `q.DebugPrintln`

`q.DebugWriter` is a package-level `io.Writer` that defaults to `os.Stderr`. Tests and CI can redirect:

```go
var buf bytes.Buffer
q.DebugWriter = &buf
// ... run things ...
assertMatches(buf.String())
```

`q.DebugSlogAttr` doesn't touch `DebugWriter` — its output destination is whatever `slog.Handler` you've configured.

## Statement forms

`q.DebugPrintln` works anywhere a value expression is valid — define, assign, return, hoist, chain method arg, another `q.*`'s arg. Not statement-only. No `q.DebugPrintlnE` — it's a tap/print, not a bubble, so the E vocabulary has nothing to shape.

`q.DebugSlogAttr` likewise works anywhere a `slog.Attr` value is wanted — typically as a varargs argument to `slog.Info` / `slog.Error` / `slog.With` / etc.

## Nesting

```go
return q.Try(loadUser(q.DebugPrintln(id)))
```

Prints `id`, then passes the same value into `loadUser`, then bubbles whatever `loadUser` returns. The print fires on *evaluation*, not on the error path.

## See also

- [q.SlogAttr / q.SlogFile / q.SlogLine](slog.md) — production-grade slog helpers (clean keys, no file:line prefix).
- [q.Try](try.md) — the most common mid-expression neighbour.
- [q.Trace](trace.md) — file:line prefix on bubbled errors (vs. q.DebugPrintln's print + pass-through).
