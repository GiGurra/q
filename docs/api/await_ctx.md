# `q.AwaitCtx` and `q.AwaitCtxE`

Future-await with context cancellation. Blocks on a `select` between `ctx.Done()` and the future's result channel.

## Signatures

```go
func AwaitCtx[T any](ctx context.Context, f Future[T]) T
func AwaitCtxE[T any](ctx context.Context, f Future[T]) ErrResult[T]

// Runtime helper (usable standalone, NOT rewritten):
func AwaitRawCtx[T any](ctx context.Context, f Future[T]) (T, error)
```

## What `q.AwaitCtx` does

```go
v := q.AwaitCtx(ctx, f)
```

rewrites to:

```go
v, _qErr1 := q.AwaitRawCtx(ctx, f)
if _qErr1 != nil {
    return /* zeros */, _qErr1
}
```

Three outcomes:

| Event                     | Return value                           |
|---------------------------|----------------------------------------|
| Future completes with `v` | `(v, nil)`                             |
| Future completes with err | `(zero, err)` — the future's own error |
| ctx is cancelled/deadline | `(zero, ctx.Err())`                    |

## Goroutine-leak caveat

If `ctx` fires first, `AwaitCtx` returns immediately — but the underlying goroutine (from `q.Async`) keeps running until `fn` finishes naturally. Go has no goroutine-kill primitive. To get true cancellation, thread the same `ctx` into the `q.Async` closure:

```go
f := q.Async(func() (User, error) { return fetchUser(ctx, id) })  // ← ctx inside
u := q.AwaitCtx(ctx, f)                                           // ← and outside
```

Without the inner `ctx`, `AwaitCtx` still gives caller-side timeout — but the work continues until `fetchUser` returns on its own. That's a Go constraint, not a q limitation.

## Chain methods on `q.AwaitCtxE`

Reuses `ErrResult[T]`. Same vocabulary as `q.TryE`:

```go
u := q.AwaitCtxE(ctx, f).Wrapf("fetching user %d", id)
u := q.AwaitCtxE(ctx, f).Catch(func(e error) (User, error) {
    if errors.Is(e, context.DeadlineExceeded) {
        return anonUser(), nil
    }
    return User{}, e
})
```

See [q.Try → chain methods](try.md#chain-methods-on-qtrye) for full method signatures.

## Fan-out with per-call deadlines

Typical pattern: spawn N futures, await each under a shared ctx. The rewritten sites stay flat:

```go
ctx = q.Timeout(ctx, 2*time.Second)

futures := make([]q.Future[int], len(urls))
for i, url := range urls {
    futures[i] = q.Async(func() (int, error) { return fetchSize(ctx, url) })
}

total := 0
for _, f := range futures {
    total += q.AwaitCtx(ctx, f)   // bubbles the first ctx-timeout (or any err)
}
```

## See also

- [q.Async](async.md) — spawning the futures
- [q.Await](async.md) — awaiting without ctx
- [q.RecvCtx](recv_ctx.md) — same idea for raw channels
- [q.Timeout / q.Deadline](timeout.md) — derive a cancel-deferred child ctx
