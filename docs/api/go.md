# `q.Go`

Spawn a goroutine wrapped in a `defer recover()` that logs panics to stderr with the call site — so one badly-behaved goroutine doesn't take the whole process down. Statement-only.

## Signature

```go
func Go(fn func())
```

## What `q.Go` does

```go
q.Go(func() {
    process(task)
})
```

rewrites to (assuming the source is `worker.go` line 17):

```go
go func() {
    defer func() {
        if _qR := recover(); _qR != nil {
            println("q.Go panic at worker.go:17:", _qR)
        }
    }()
    (func() {
        process(task)
    })()
}()
```

The `println` builtin writes to stderr and needs no imports. The compile-time file/line means you always know which `q.Go` caused the panic even when the goroutine is long-lived and detached from its spawn site.

## Why not plain `go fn()`?

Plain `go fn()` lets a panic in `fn` crash the entire process — Go has no default recovery for goroutines. In a server you usually want to log the panic and keep serving. `q.Go` is the two-line boilerplate you'd write by hand, except the preprocessor captures the spawn site too.

## Statement forms

`q.Go` returns nothing — always a plain expression statement.

## See also

- [q.TryCatch](trycatch.md) — for in-goroutine recovery within a single statement.
- [q.Recover](recover.md) — for function-wide panic-to-error conversion.
