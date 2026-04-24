# `q.TryCatch`

Java/Python-style try/catch via `panic` / `recover`. Statement-only. Framed as a demo of what the preprocessor can express — Go's explicit error returns are still the idiomatic choice for most real code.

## Signature

```go
func TryCatch(try func()) TryCatchResult
func (TryCatchResult) Catch(handler func(any))
```

The terminal `.Catch(handler)` call defines both scopes.

## What `q.TryCatch` does

```go
q.TryCatch(func() {
    risky()
    moreRisky()
}).Catch(func(r any) {
    log.Println("recovered:", r)
})
```

rewrites to:

```go
func() {
    defer func() {
        if _qR := recover(); _qR != nil {
            (func(r any) {
                log.Println("recovered:", r)
            })(_qR)
        }
    }()
    (func() {
        risky()
        moreRisky()
    })()
}()
```

An IIFE wraps `try` with a `defer recover()` that dispatches to `handler` if the panic value was non-nil.

## When to use

- Migrating from a panic-heavy library and you want to localise the recovery boundary.
- Demoing what the preprocessor can express in a single statement (the main reason this is shipped).

For production, prefer:
- [`q.Recover`](recover.md) — function-level panic-to-error conversion.
- Explicit `if err != nil { return err }` with `q.Try` / `q.TryE`.

## Statement forms

Stmt-only. The full chain returns nothing.

## See also

- [q.Recover](recover.md) — the production counterpart; function-wide not block-wide.
- [q.Go](go.md) — for goroutine-local recovery.
