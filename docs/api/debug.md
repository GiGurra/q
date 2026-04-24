# `q.Debug` and `q.DebugAt`

Go's missing `dbg!` macro. Prints a value with its source text and call site, returns it unchanged so the call can sit mid-expression.

## Signatures

```go
func Debug[T any](v T) T
func DebugAt[T any](label string, v T) T

var DebugWriter io.Writer = os.Stderr
```

`q.Debug(x)` is rewritten by the preprocessor to `q.DebugAt("<file>:<line> <src-text>", x)`. `q.DebugAt` is a plain runtime function you can also call directly when you want a custom label.

## What `q.Debug` does

```go
u := loadUser(q.Debug(id))
```

rewrites to (say, `main.go` line 17):

```go
u := loadUser(q.DebugAt("main.go:17 id", id))
// which at runtime prints to DebugWriter:
// main.go:17 id = 7
// and returns 7 unchanged, so loadUser sees the same value.
```

The `<src-text>` is the *literal source* of the argument — whatever you wrote between the parens. Useful when debugging arithmetic:

```go
q.Debug(n * 2 + offset)   // prints "n * 2 + offset = <value>"
```

## Configurable destination

`q.DebugWriter` is a package-level `io.Writer` that defaults to `os.Stderr`. Tests and CI can redirect:

```go
var buf bytes.Buffer
q.DebugWriter = &buf
... run things ...
assertMatches(buf.String())
```

## Statement forms

Works anywhere a value expression is valid — define, assign, return, hoist, chain method arg, another `q.*`'s arg. Not statement-only. No `q.DebugE` — Debug is a tap/print, not a bubble, so the E vocabulary has nothing to shape.

## Nesting

```go
return q.Try(loadUser(q.Debug(id)))
```

Prints `id`, then passes the same value into `loadUser`, then bubbles whatever `loadUser` returns. Debug fires on *evaluation*, not on the error path.

## See also

- [q.Try](try.md) — the most common mid-expression neighbour.
