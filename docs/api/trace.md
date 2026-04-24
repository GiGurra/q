# `q.Trace` and `q.TraceE`

The `q.Try` family with a call-site `file:line` prefix injected into the bubbled error. Captured at compile time by the preprocessor — plain Go would need a stack walk at runtime to get the same information.

## Signatures

```go
func Trace[T any](v T, err error) T
func TraceE[T any](v T, err error) TraceResult[T]
```

## What `q.Trace` does

```go
row := q.Trace(db.Query(id))
```

rewrites to (assuming the source is `users.go` line 42):

```go
row, _qErr1 := db.Query(id)
if _qErr1 != nil {
    return /* zeros */, fmt.Errorf("users.go:42: %w", _qErr1)
}
```

The `%w` keeps the original error reachable via `errors.Is` / `errors.As`.

## Chain methods on `q.TraceE`

Each method composes *over* the location prefix — the prefix is always present, the method shapes what follows.

| Method                                | Bubbled error                                               |
|---------------------------------------|-------------------------------------------------------------|
| `.Err(replacement error)`             | `fmt.Errorf("<file>:<line>: %w", replacement)`              |
| `.ErrF(fn func(error) error)`         | `fmt.Errorf("<file>:<line>: %w", fn(capturedErr))`          |
| `.Wrap(msg string)`                   | `fmt.Errorf("<file>:<line>: <msg>: %w", capturedErr)`       |
| `.Wrapf(format string, args ...any)`  | `fmt.Errorf("<file>:<line>: <format>: %w", args..., err)`   |
| `.Catch(fn func(error) (T, error))`   | Recover `(v, nil)` or bubble `fmt.Errorf("<file>:<line>: %w", newErr)` |

```go
row := q.TraceE(db.Query(id)).Wrapf("loading user %d", id)
// → "users.go:42: loading user 7: <inner>"
```

## Statement forms

Same five positions as `q.Try`. All bubbles carry the prefix.

## Why not just use `%w` everywhere?

The compile-time file/line is what makes Trace useful — you get `runtime.Caller(0)`-style locality without paying `runtime.Caller(0)`'s cost at runtime. The prefix is baked in as a string literal during rewriting.

## See also

- [q.Try](try.md) — the same shape without the prefix.
- [Design](../design.md) — how the rewriter captures call-site positions.
