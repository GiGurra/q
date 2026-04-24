# `q.AwaitAll` / `q.AwaitAny` (and their Ctx + E variants)

Fan-in over multiple `q.Future[T]`s. Two complementary semantics:

- **`q.AwaitAll`** — wait for every future to succeed, return `[]T` in input order. Bubble the first error observed.
- **`q.AwaitAny`** — return the first future to succeed. If every future fails, bubble `errors.Join(...)` of all failures.

Each has `Ctx` + `E` variants — 8 helpers total, same spelling convention as the rest of q.

## Signatures

```go
// AwaitAll family — ([]T, error) bubble.
func AwaitAll[T any](futures ...Future[T]) []T
func AwaitAllE[T any](futures ...Future[T]) ErrResult[[]T]
func AwaitAllCtx[T any](ctx context.Context, futures ...Future[T]) []T
func AwaitAllCtxE[T any](ctx context.Context, futures ...Future[T]) ErrResult[[]T]

// AwaitAny family — (T, error) bubble.
func AwaitAny[T any](futures ...Future[T]) T
func AwaitAnyE[T any](futures ...Future[T]) ErrResult[T]
func AwaitAnyCtx[T any](ctx context.Context, futures ...Future[T]) T
func AwaitAnyCtxE[T any](ctx context.Context, futures ...Future[T]) ErrResult[T]

// Runtime helpers (NOT rewritten — callable directly):
func AwaitAllRaw[T any](futures ...Future[T]) ([]T, error)
func AwaitAllRawCtx[T any](ctx context.Context, futures ...Future[T]) ([]T, error)
func AwaitAnyRaw[T any](futures ...Future[T]) (T, error)
func AwaitAnyRawCtx[T any](ctx context.Context, futures ...Future[T]) (T, error)
```

## What `q.AwaitAll` does

```go
vs := q.AwaitAll(fA, fB, fC)
```

rewrites to:

```go
vs, _qErr1 := q.AwaitAllRaw(fA, fB, fC)
if _qErr1 != nil {
    return /* zeros */, _qErr1
}
```

Parallel wait: every future runs in its own goroutine (`q.Async` started them), the helper spawns one collector per future, and the first error short-circuits. Successful results are gathered in input order regardless of completion order.

## What `q.AwaitAny` does

```go
winner := q.AwaitAny(fA, fB, fC)
```

Returns the first future to succeed. If every future errors, bubbles `errors.Join(errA, errB, errC)` (in completion order). If `ctx` fires first (Ctx variant), bubbles `ctx.Err()` instead — any already-collected per-future errors are discarded.

This is **Promise.any semantics** (first success wins with failover), not Promise.race (first completion wins regardless).

## Variadic spread survives the rewrite

```go
fs := []q.Future[int]{q.Async(f1), q.Async(f2), q.Async(f3)}
vs := q.AwaitAll(fs...)             // ← spread preserved in the rewrite
```

The preprocessor detects the Ellipsis on the entry call and threads it through into the generated `q.AwaitAllRaw(fs...)` call.

## Fan-out / fan-in in a single line

```go
func fetchSizes(ctx context.Context, urls []string) ([]int, error) {
    ctx = q.Timeout(ctx, 2*time.Second)

    futures := make([]q.Future[int], len(urls))
    for i, url := range urls {
        futures[i] = q.Async(func() (int, error) { return fetchSize(ctx, url) })
    }
    return q.AwaitAllCtx(ctx, futures...), nil
}
```

No explicit `for _, f := range futures` loop with `q.Await`, no per-future bubble, no error-accumulation scaffolding.

## Goroutine-leak caveat (same as AwaitCtx)

When the Ctx variants bail on `ctx.Done`, the underlying `q.Async` goroutines keep running until `fn` returns naturally. Thread `ctx` into each `q.Async` closure when early cancellation of the spawned work matters.

## Chain methods

The E variants return `ErrResult[[]T]` (AwaitAll) or `ErrResult[T]` (AwaitAny). Vocabulary is identical to [`q.TryE`](try.md#chain-methods-on-qtrye).

```go
vs := q.AwaitAllE(fA, fB, fC).Wrap("gathering")
winner := q.AwaitAnyE(fA, fB, fC).Catch(func(e error) (int, error) {
    return 0, nil                           // every attempt failed; default to 0
})
```

## See also

- [q.Async](async.md) — spawning the futures.
- [q.Await](async.md) — single-future await.
- [q.AwaitCtx](await_ctx.md) — single-future await with ctx cancellation.
- [q.Timeout / q.Deadline](timeout.md) — derive a cancel-deferred ctx.
