# Multi-channel helpers — `q.RecvAny`, `q.Drain`, `q.DrainAll`

Fan-in operations on channels. Three shapes:

- **`q.RecvAny`** — select over N channels, return the first value received. Like `q.AwaitAny` but for channels instead of futures.
- **`q.Drain`** — receive from one channel until it closes, return `[]T`.
- **`q.DrainAll`** — drain every channel in parallel until all close, return `[][]T` in input order.

## Signatures

```go
// RecvAny — first-value-wins select.
func RecvAny[T any](chans ...<-chan T) T
func RecvAnyE[T any](chans ...<-chan T) ErrResult[T]
func RecvAnyCtx[T any](ctx context.Context, chans ...<-chan T) T
func RecvAnyCtxE[T any](ctx context.Context, chans ...<-chan T) ErrResult[T]

// Drain — pure runtime (no bubble, no error path).
func Drain[T any](ch <-chan T) []T
func DrainAll[T any](chans ...<-chan T) [][]T

// DrainCtx / DrainAllCtx — bubble on ctx cancellation.
func DrainCtx[T any](ctx context.Context, ch <-chan T) []T
func DrainCtxE[T any](ctx context.Context, ch <-chan T) ErrResult[[]T]
func DrainAllCtx[T any](ctx context.Context, chans ...<-chan T) [][]T
func DrainAllCtxE[T any](ctx context.Context, chans ...<-chan T) ErrResult[[]T]

// Runtime helpers (NOT rewritten — callable directly):
func RecvAnyRaw[T any](chans ...<-chan T) (T, error)
func RecvAnyRawCtx[T any](ctx context.Context, chans ...<-chan T) (T, error)
func DrainRawCtx[T any](ctx context.Context, ch <-chan T) ([]T, error)
func DrainAllRawCtx[T any](ctx context.Context, chans ...<-chan T) ([][]T, error)
```

## Why no `DrainE` / `DrainAllE` non-ctx form

Without `ctx`, `Drain` and `DrainAll` can't fail — if every channel eventually closes they return their collected slices; if one never closes they block forever. There's no error to shape, so the chain vocabulary (`Wrap`, `Wrapf`, `Err`, `Catch`, `ErrF`) has nothing to act on. The E variant only exists for the `Ctx` forms where `ctx.Err()` is a real error source.

## What `q.RecvAny` does

```go
v := q.RecvAny(ch1, ch2, ch3)
```

Uses `reflect.Select` to perform a dynamic N-way select over the supplied channels. Returns the first value received. On any channel close, bubbles `q.ErrChanClosed`.

The chain variants let you shape or suppress the close bubble:

```go
// Ignore close — keep waiting for others is not possible here since
// RecvAny is single-shot, but you can recover to a sentinel value.
v := q.RecvAnyE(ch1, ch2).Catch(func(e error) (int, error) {
    if errors.Is(e, q.ErrChanClosed) {
        return -1, nil
    }
    return 0, e
})
```

For "keep listening until a real value arrives across multiple channels, skipping closes as they happen", write the loop by hand — `RecvAny` is single-shot by design.

## What `q.Drain` does

```go
vs := q.Drain(ch)       // blocks until ch closes, returns all collected values
```

`q.DrainCtx` adds `ctx` cancellation:

```go
vs := q.DrainCtx(ctx, ch)  // bubbles ctx.Err() if ctx fires first
```

On cancel, partial results are discarded and `ctx.Err()` bubbles. Use the raw helper `q.DrainRawCtx` directly if you want to inspect partial results on cancel — but the typical bubble-and-bail path matches the rest of q.

## What `q.DrainAll` does

```go
results := q.DrainAll(chA, chB, chC)
// results[0] == values from chA, results[1] == values from chB, …
```

One goroutine per channel drains until its channel closes. Blocks until every channel has closed. Results are indexed by input position.

`q.DrainAllCtx` adds `ctx` cancellation; on cancel, returns `ctx.Err()` and discards all partial per-channel results. Background goroutines continue draining until each source closes naturally — same goroutine-leak caveat as `q.AwaitAllCtx`. If you need the producer side to bail too, thread the same `ctx` through to whatever code writes to these channels.

## Result-type design

Why `[][]T` for `DrainAll`, not `map[<-chan T][]T`:

- Channels are comparable (pointer identity), so the map is syntactically valid — but map keys don't render usefully in logs or prints.
- `[][]T` preserves input-order correlation with the user's arguments; callers can index by the same position they passed.
- Mirrors `AwaitAll`'s `[]T` convention: one consistent pattern across the library.

## Why no `DrainAny`

Ambiguous semantics (first-channel-to-close-wins? merge-until-first-close? just "gather whatever"?) — none clearly better than the rest. Compose from primitives when needed.

## See also

- [q.Recv](recv.md) — single-channel receive with close bubble.
- [q.RecvCtx](recv_ctx.md) — single-channel receive with ctx cancellation.
- [q.AwaitAll / AwaitAny](await_multi.md) — the Future analogues.
