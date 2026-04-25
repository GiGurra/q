# `q.DebugPrintln`, `q.DebugPrintlnAt`, `q.DebugSlogAttr`

Two debug-time helpers that auto-capture the source text and `file:line` of their argument at compile time, so you don't have to retype it as a label:

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

Use it whenever you want a structured-log line that includes a *value* without retyping the variable name as the slog key. Several of these can sit alongside each other:

```go
slog.Info("step done",
    q.DebugSlogAttr(input),
    q.DebugSlogAttr(processed),
    q.DebugSlogAttr(elapsed))
```

The handler sees three `slog.Attr`s with auto-generated keys `main.go:42 input`, `main.go:42 processed`, `main.go:42 elapsed` — visible in JSON / text / any other slog format.

`q.DebugSlogAttr` does not pass the value through (it returns `slog.Attr`, not `T`). If you want mid-expression instrumentation, use `q.DebugPrintln`.

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

- [q.Try](try.md) — the most common mid-expression neighbour.
- [q.Trace](trace.md) — file:line prefix on bubbled errors (vs. q.DebugPrintln's print + pass-through).
