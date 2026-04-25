# `q.Timeout` and `q.Deadline`

Derive a child context whose `cancel` is automatically `defer`-registered in the enclosing function. Hides the `cancel` juggling that the standard idiom requires.

## Signatures

```go
func Timeout(ctx context.Context, dur time.Duration) context.Context
func Deadline(ctx context.Context, t time.Time) context.Context
```

The signatures are pure Go — `gopls` sees ordinary `context.Context`-returning functions. The rewriter's job is the cancel plumbing.

## Difference between them

| Helper       | Takes           | Use when                                                                      |
|--------------|-----------------|-------------------------------------------------------------------------------|
| `q.Timeout`  | `time.Duration` | "This operation shouldn't take more than X" — a fresh relative timeout.       |
| `q.Deadline` | `time.Time`     | Propagating an inherited deadline — HTTP request header, parent job end-time. |

Internally, `WithTimeout(ctx, d)` is just `WithDeadline(ctx, time.Now().Add(d))`.

## What `q.Timeout` does

Two shapes, both valid:

```go
// Shadowing form — most common.
ctx = q.Timeout(ctx, 5*time.Second)

// Rewrites to:
// var _qCancel1 context.CancelFunc
// ctx, _qCancel1 = context.WithTimeout(ctx, 5*time.Second)
// defer _qCancel1()

// Define form — preserve parent under a different name.
tight := q.Timeout(parent, 5*time.Second)

// Rewrites to:
// tight, _qCancel1 := context.WithTimeout(parent, 5*time.Second)
// defer _qCancel1()
```

The `_qCancelN` identifier is hidden — callers never see or name it. The `defer` fires when the enclosing function returns, cancelling the child context and releasing its timer.

## When *not* to use `q.Timeout`

For "cancel early from another goroutine" flows, write the idiom by hand:

```go
ctx, cancel := context.WithCancel(parent)
// pass `cancel` somewhere that invokes it later
```

`q.Timeout` / `q.Deadline` hide the cancel function, which is the wrong default when outside code needs to invoke it. The helpers target the overwhelming case — you know the maximum duration up front and want auto-cleanup — not the general-purpose WithCancel.

## Only in define or assign position

```go
ctx = q.Timeout(ctx, dur)      // ✓ assign
tight := q.Timeout(ctx, dur)   // ✓ define
```

Rejected:

```go
doThing(q.Timeout(ctx, dur))   // ✗ hoist — preprocessor can't place the defer
return q.Timeout(ctx, dur)     // ✗ return position
```

Both cases bail with a preprocessor diagnostic — the `defer cancel()` has no sensible home.

## Example

```go
func fetch(parent context.Context, url string) (Result, error) {
    ctx := q.Timeout(parent, 2*time.Second)                  // auto-cancel at return
    req := q.Try(http.NewRequestWithContext(ctx, "GET", url, nil))
    resp := q.TryE(http.DefaultClient.Do(req)).Wrap("fetch")
    defer resp.Body.Close()
    return q.Try(parseResult(resp.Body)), nil
}
```

Without `q.Timeout`, the same function would declare `ctx, cancel := context.WithTimeout(...)` and a `defer cancel()` as separate lines — mechanical noise that `q.Timeout` folds away.

## See also

- [q.CheckCtx](bubble.md) — cancellation checkpoints at natural yield points
- [q.RecvCtx](recv_ctx.md) / [q.AwaitCtx](await_ctx.md) — ctx-aware blocking primitives
