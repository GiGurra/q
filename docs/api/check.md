# `q.Check` and `q.CheckE`

The `error`-only bubble — for functions that return just `error` (`file.Close`, `db.Ping`, `validate(input)`).

## Signatures

```go
func Check(err error)
func CheckE(err error) CheckResult
```

Both return nothing. Consequence: `q.Check(...)` and `q.CheckE(...)` only make sense as expression statements — `v := q.Check(...)` is a Go type error, caught by the compiler before the rewriter runs. That's deliberate: q only accepts what gopls accepts.

## What `q.Check` does

```go
q.Check(db.Ping())
```

rewrites to:

```go
_qErr1 := db.Ping()
if _qErr1 != nil {
    return /* zeros */, _qErr1
}
```

The enclosing function's last result must be `error` for the bubble to land anywhere.

## Chain methods on `q.CheckE`

All methods return void. Every one of them maps to the same shape as the `q.TryE` counterpart, minus the value.

| Method                                | Bubbled error                                         |
|---------------------------------------|-------------------------------------------------------|
| `.Err(replacement error)`             | `replacement`                                         |
| `.ErrF(fn func(error) error)`         | `fn(capturedErr)`                                     |
| `.Wrap(msg string)`                   | `fmt.Errorf("<msg>: %w", capturedErr)`                |
| `.Wrapf(format string, args ...any)`  | `fmt.Errorf("<format>: %w", args..., capturedErr)`    |
| `.Catch(fn func(error) error)`        | `fn(err)` — **`nil` suppresses the bubble**, non-nil bubbles that error in place of the original |

The `.Catch` signature is `func(error) error` (not `func(error) (T, error)` as in `TryE`) because there is no T to recover. Returning `nil` is the "swallow this" shape:

```go
q.CheckE(file.Close()).Catch(func(e error) error {
    if errors.Is(e, os.ErrClosed) {
        return nil                    // already closed — not an error we care about
    }
    return e                          // bubble everything else
})
```

## Examples

```go
func shutdown(db *sql.DB, file *os.File) error {
    q.Check(db.Ping())                          // bubble db.Ping's err unchanged
    q.CheckE(file.Close()).Wrap("closing log")  // wrap with message + %w
    q.Check(db.Close())
    return nil
}
```

## Not supported

- `v := q.Check(...)` — Check returns `()`; this is a Go type error.
- `return q.Check(...), 0, nil` — same reason, can't use void in a return tuple.
- `q.Check` as a return-position or hoist argument — both require a value.

## See also

- [Examples → Basic bubbling](../examples/basic.md) — includes a `Check`-based shutdown sequence
- [q.Try](try.md) — for `(T, error)` values
