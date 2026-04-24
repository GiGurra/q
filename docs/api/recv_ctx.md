# `q.RecvCtx` and `q.RecvCtxE`

Channel receive that honours context cancellation. Blocks on a `select` between `ctx.Done()` and the channel; whichever fires first wins.

## Signatures

```go
func RecvCtx[T any](ctx context.Context, ch <-chan T) T
func RecvCtxE[T any](ctx context.Context, ch <-chan T) ErrResult[T]

// Runtime helper (usable standalone, NOT rewritten):
func RecvRawCtx[T any](ctx context.Context, ch <-chan T) (T, error)
```

## What `q.RecvCtx` does

```go
v := q.RecvCtx(ctx, ch)
```

rewrites to:

```go
v, _qErr1 := q.RecvRawCtx(ctx, ch)
if _qErr1 != nil {
    return /* zeros */, _qErr1
}
```

`RecvRawCtx` is the runtime helper the rewriter inlines the call to. It's a plain function — `select { <-ctx.Done() | <-ch }` — so user code can call it directly when the raw `(T, error)` tuple is wanted.

Three outcomes:

| Event                     | Return value                              |
|---------------------------|-------------------------------------------|
| ch delivers `v`           | `(v, nil)`                                |
| ch is closed              | `(zero, q.ErrChanClosed)`                 |
| ctx is cancelled/deadline | `(zero, ctx.Err())` — `Canceled` or `DeadlineExceeded` |

## Chain methods on `q.RecvCtxE`

Reuses `ErrResult[T]` — identical vocabulary to `q.TryE` (the captured `err` is the Raw helper's output, which may be `ctx.Err()` or `q.ErrChanClosed`).

```go
v := q.RecvCtxE(ctx, ch).Wrap("waiting for job")
v := q.RecvCtxE(ctx, ch).Catch(func(e error) (Job, error) {
    if errors.Is(e, q.ErrChanClosed) {
        return Job{}, nil                // normal shutdown, recover with empty
    }
    return Job{}, e                      // bubble ctx cancellation
})
```

See [q.Try → chain methods](try.md#chain-methods-on-qtrye) for full method signatures.

## Distinguishing cancellation from close

Two sentinels to tell the cases apart:

```go
v, err := q.RecvRawCtx(ctx, ch)
switch {
case errors.Is(err, q.ErrChanClosed):
    // clean shutdown
case errors.Is(err, context.Canceled):
    // someone asked us to stop
case errors.Is(err, context.DeadlineExceeded):
    // we ran out of time
case err != nil:
    // shouldn't happen — Raw only returns these three
}
```

## See also

- [q.Recv](recv.md) — no ctx, just the close-sentinel bubble
- [q.Bubble](bubble.md) — standalone ctx checkpoint, no channel
- [q.AwaitCtx](await_ctx.md) — same idea for `q.Future`
