# `q.Recover` and `q.RecoverE`

Function-wide panic-to-error conversion. `defer q.Recover(&err)` at the top of a function catches any panic, wraps it in `*q.PanicError`, and assigns it to the named error return. Pure runtime — no preprocessor rewriting.

## Signatures

```go
func Recover(errPtr *error)
func RecoverE(errPtr *error) RecoverResult

type PanicError struct {
    Value any
    Stack []byte
}
```

The whole family is plain runtime code — Go's `recover()` sees the panic because `q.Recover` (or `RecoverE`'s terminal method) IS the deferred function.

## What `q.Recover` does

```go
func doWork(input Input) (err error) {
    defer q.Recover(&err)
    process(input)        // may panic
    return nil
}
```

At runtime:

1. If `process` returns normally → `err == nil`, function returns `nil`.
2. If `process` panics with value `r` → `q.Recover` catches it, assigns `&q.PanicError{Value: r, Stack: debug.Stack()}` to `err`, function returns that error.

Callers can unwrap the panic:

```go
var pe *q.PanicError
if errors.As(err, &pe) {
    log.Printf("panic value: %v", pe.Value)
    log.Printf("stack:\n%s", pe.Stack)
}
```

## Chain methods on `q.RecoverE`

Each method is terminal — it's the deferred function. The `recover()` inside each method is what catches the panic.

| Method                                    | Stored in `*errPtr` on panic                                           |
|-------------------------------------------|------------------------------------------------------------------------|
| `.Map(fn func(any) error)`                | `fn(panicValue)` — full custom translation                             |
| `.Err(replacement error)`                 | `replacement` — discard panic value and stack                           |
| `.ErrF(fn func(*PanicError) error)`       | `fn(&PanicError{…})` — see the wrapper, return a richer error          |
| `.Wrap(msg string)`                       | `fmt.Errorf("<msg>: %w", &PanicError{…})`                              |
| `.Wrapf(format string, args ...any)`      | `fmt.Errorf("<format>: %w", args..., &PanicError{…})`                  |

```go
defer q.RecoverE(&err).Map(func(r any) error {
    if s, ok := r.(BusinessRuleViolation); ok {
        return &APIError{Code: 400, Detail: s.String()}
    }
    return &APIError{Code: 500, Detail: fmt.Sprint(r)}
})
```

## Not `q.TryCatch`

[`q.TryCatch`](trycatch.md) recovers *inside* a block. `q.Recover` recovers at the *function* boundary, which is what you usually want — it composes with Go's error returns cleanly, and the caller decides what to do with the wrapped panic.

## Runtime-only, deliberately

The "chain method IS the deferred function" property is why this works without preprocessor rewriting. Don't refactor it into helper calls — `recover()` only sees panics when called directly from a deferred function, not transitively.

## See also

- [q.TryCatch](trycatch.md) — block-scoped counterpart.
- [q.Go](go.md) — goroutine-local recovery with println logging.
