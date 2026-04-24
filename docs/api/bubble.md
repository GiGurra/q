# `q.Bubble` and `q.BubbleE`

Context-cancellation checkpoint. Statement-only: at the call site, `ctx.Err()` is checked, and a non-nil value bubbles out of the enclosing function.

## Signatures

```go
func Bubble(ctx context.Context)
func BubbleE(ctx context.Context) CheckResult
```

Both return nothing — same rule as `q.Check`: only valid as an expression statement. `v := q.Bubble(ctx)` is a Go type error.

## What `q.Bubble` does

```go
q.Bubble(ctx)
```

rewrites to:

```go
_qErr1 := (ctx).Err()
if _qErr1 != nil {
    return /* zeros */, _qErr1
}
```

The bubbled error is whatever `ctx.Err()` returns — `context.Canceled` when the context has been cancelled explicitly, `context.DeadlineExceeded` when a deadline has passed. Both implement `error`, so the bubble flows through `errors.Is` / `errors.As` cleanly.

## Where to put checkpoints

Wherever a long-running operation could be safely interrupted — between iterations, between expensive calls, at natural yield points. The call is cheap (a single `ctx.Err()` and a conditional return), so placing them liberally is fine.

```go
func processBatch(ctx context.Context, items []Item) error {
    for _, item := range items {
        q.Bubble(ctx)               // cheap per-iteration cancellation check
        if err := process(item); err != nil {
            return err
        }
    }
    return nil
}
```

For blocking operations (channel receive, future await) reach for `q.RecvCtx` / `q.AwaitCtx` — those fold the ctx check into the blocking operation itself.

## Chain methods on `q.BubbleE`

Identical vocabulary to `q.CheckE` — the captured error is `ctx.Err()`, and every method reshapes it before the bubble. All methods return void.

| Method                                | Bubbled error                                         |
|---------------------------------------|-------------------------------------------------------|
| `.Err(replacement error)`             | `replacement`                                         |
| `.ErrF(fn func(error) error)`         | `fn(ctx.Err())`                                       |
| `.Wrap(msg string)`                   | `fmt.Errorf("<msg>: %w", ctx.Err())`                  |
| `.Wrapf(format string, args ...any)`  | `fmt.Errorf("<format>: %w", args..., ctx.Err())`      |
| `.Catch(fn func(error) error)`        | `fn(ctx.Err())` — **`nil` suppresses**, non-nil bubbles |

```go
q.BubbleE(ctx).Wrap("loading users")
// rewrites to: if err := ctx.Err(); err != nil { return …, fmt.Errorf("loading users: %w", err) }

q.BubbleE(ctx).Catch(func(e error) error {
    if errors.Is(e, context.Canceled) {
        return nil                    // user cancelled — not a bug
    }
    return fmt.Errorf("deadline hit: %w", e)
})
```

## Not supported

- `v := q.Bubble(...)` — Bubble returns `()`; this is a Go type error.
- `return q.Bubble(...), nil` — same reason.
- `q.Bubble` in a return-position or hoisted inside another expression.

## See also

- [q.RecvCtx](recv_ctx.md) — ctx-aware channel receive
- [q.AwaitCtx](await_ctx.md) — ctx-aware future await
- [q.Timeout / q.Deadline](timeout.md) — derive a cancel-deferred child context
