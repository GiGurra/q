# Parallel data ops: `q.ParMap`, `q.ParFlatMap`, `q.ParFilter`, `q.ParForEach`

Bounded-concurrency variants of the [data ops](data.md). Each fn invocation runs on a worker pool whose size defaults to `runtime.NumCPU()` and is configured via `context.Context` — `q.WithPar(ctx, n)`. Pure runtime helpers, no preprocessor magic.

```go
ctx = q.WithPar(ctx, 8)
results := q.Try(q.ParMapErr(ctx, urls, fetchURL))
```

## Why ctx-carried, not functional options

Two viable shapes for the limit:

1. **Functional options** — `q.ParMap(items, fn, q.ParLimit(8))` (samber/lo PR #858 style)
2. **Ctx-carried** — `q.ParMap(q.WithPar(ctx, 8), items, fn)` (q's choice)

q ships ctx-carried because:

- **Set once, propagates through the call graph.** A handler that sets `ctx = q.WithPar(ctx, 16)` once gets every nested ParMap call honouring that limit without re-threading.
- **Symmetric with the rest of q.** ctx-aware q.* helpers (`q.RecvCtx`, `q.AwaitCtx`, `q.CheckCtx`, …) all take `ctx` as the first arg. Par* fits the same shape.
- **Per-call override is still cheap.** `q.ParMap(q.WithPar(ctx, 16), items, fn)` derives a per-call ctx. Slightly longer than `q.ParLimit(16)`, but no new concept.

The ctx-carried choice is a deliberate departure from samber/lo and party — both ship per-call options. q owns its house style and ctx-as-config-vehicle is more in line with that.

## Surface

```go
// Bare — no error path. ctx read for the limit; cancellation stops dispatch.
func ParMap[T, R any](ctx context.Context, slice []T, fn func(T) R) []R
func ParFlatMap[T, R any](ctx context.Context, slice []T, fn func(T) []R) []R
func ParFilter[T any](ctx context.Context, slice []T, pred func(T) bool) []T
func ParForEach[T any](ctx context.Context, slice []T, fn func(T))

// Predicate searches — short-circuit on first match (Exists) or first
// non-match (ForAll). ctx cancellation honoured (returns false).
func ParExists[T any](ctx context.Context, slice []T, pred func(T) bool) bool
func ParForAll[T any](ctx context.Context, slice []T, pred func(T) bool) bool

// …Err — first error wins; ctx cancellation produces ctx.Err().
//        User fn takes ctx for early-exit on cancellation.
func ParMapErr[T, R any](ctx context.Context, slice []T, fn func(context.Context, T) (R, error)) ([]R, error)
func ParFlatMapErr[T, R any](ctx context.Context, slice []T, fn func(context.Context, T) ([]R, error)) ([]R, error)
func ParFilterErr[T any](ctx context.Context, slice []T, pred func(context.Context, T) (bool, error)) ([]T, error)
func ParForEachErr[T any](ctx context.Context, slice []T, fn func(context.Context, T) error) error
func ParExistsErr[T any](ctx context.Context, slice []T, pred func(context.Context, T) (bool, error)) (bool, error)
func ParForAllErr[T any](ctx context.Context, slice []T, pred func(context.Context, T) (bool, error)) (bool, error)

// Limit configuration on context
func WithPar(ctx context.Context, limit int) context.Context
func WithParUnbounded(ctx context.Context) context.Context  // one goroutine per item
func GetPar(ctx context.Context) int                        // default = runtime.NumCPU()
```

## Bare vs `…Err` semantics

| | Bare | `…Err` |
|---|---|---|
| ctx read for limit | ✅ | ✅ |
| ctx honoured for cancellation | ✅ (stops dispatch) | ✅ (returns ctx.Err()) |
| User fn takes ctx | ❌ | ✅ |
| Returns error | ❌ | ✅ |
| First error semantics | n/a | first error wins, others discarded |

Both flavours honour cancellation, but they signal it differently:

- **Bare:** when ctx fires, dispatch stops immediately. In-flight workers run to completion (Go has no goroutine kill). The returned slice is the partial set — un-dispatched indices hold the zero value of R for `ParMap`, are filtered out for `ParFilter`, and aren't visited at all for `ParForEach`. Callers who need to distinguish cancel from "everything ran but nothing matched" check `ctx.Err()` after the call.
- **`…Err`:** same dispatch-stop behaviour, plus the function returns `(zero, ctx.Err())` so cancellation flows through the bubble path naturally.

Pass ctx into the user fn (the second arg in Err variants) to give workers true early-exit on cancellation — the dispatcher can stop scheduling, but only the user code can stop its own work.

## At a glance

```go
// Default concurrency (runtime.NumCPU())
results := q.ParMap(ctx, items, expensive)

// Set the limit for a request scope
ctx = q.WithPar(ctx, 8)
results := q.ParMap(ctx, items, expensive)

// Unbounded — one goroutine per item (use sparingly)
ctx2 := q.WithParUnbounded(ctx)
results = q.ParMap(ctx2, items, expensive)

// Read the limit (default = NumCPU)
n := q.GetPar(ctx)
```

## Composition with `q.Try` / `q.TryE`

The `…Err` family returns `(result, error)`, so `q.Try` / `q.TryE` work directly:

```go
func loadAll(ctx context.Context, urls []string) ([]Response, error) {
    return q.Try(q.ParMapErr(ctx, urls, fetchURL)), nil
}

func loadAllAnnotated(ctx context.Context, urls []string) ([]Response, error) {
    return q.TryE(q.ParMapErr(ctx, urls, fetchURL)).Wrap("loading all"), nil
}
```

`q.Check` consumes the error-only `ParForEachErr`:

```go
func uploadAll(ctx context.Context, files []File) error {
    q.Check(q.ParForEachErr(ctx, files, upload))
    return nil
}
```

## Cancellation semantics

When ctx cancels mid-flight in an `…Err` op:

1. The dispatcher stops scheduling new work immediately.
2. Already-in-flight workers continue running their current fn — Go has no goroutine kill. Pass ctx into the user fn (it's the second arg) and `select`-check `ctx.Done()` for true early shutdown.
3. The op returns `(zero, ctx.Err())` once the dispatcher exits and pending workers join.
4. Worker results / errors that arrive after cancel are discarded.

```go
ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
defer cancel()

// fetch will see ctx cancelled if it lasts > 5s; the op returns ctx.Err()
results, err := q.ParMapErr(ctx, urls, func(c context.Context, url string) (Response, error) {
    return fetchWithCtx(c, url) // checks c.Done() internally
})
```

## First-error semantics

In `…Err` variants, first error wins:

- The first worker to return an error pushes it to a 1-buffered error channel.
- Subsequent errors hit the default branch in a non-blocking select and are discarded.
- The dispatcher's priority check picks up the buffered error and stops scheduling.

This is the same shape as `golang.org/x/sync/errgroup` and `q.AwaitAllRaw`. If you want to *collect* every error rather than bail on first, write an explicit loop or use a `…Err` with a fn that swallows-and-records.

## Implementation notes

- **Worker pool size:** `min(GetPar(ctx), len(slice))` — never more workers than work to do.
- **Output ordering:** workers write to `out[i]` directly (or write to a `mask[i] bool` for filter). Read after `wg.Wait()`. No per-element copy beyond the result slice.
- **No atomics for first-error:** the 1-buffered errCh + non-blocking-send pattern (from samber/lo PR #858) replaces what `atomic.Pointer[error]` would do; cleaner and slightly faster.
- **Two-phase select in dispatch:** priority check on errCh / ctx.Done first, then a send-or-error select. Catches errors and cancellation immediately without competing with work-channel sends in a flat select.
- **Inspired by [github.com/GiGurra/party](https://github.com/GiGurra/party) and [samber/lo PR #858](https://github.com/samber/lo/pull/858).** q's flavour: ctx-carried limit instead of options/builder, no per-element index, slim signatures matching the rest of the data-ops family.

## When *not* to use

- **Cheap work** (predicates, primitive arithmetic, single-field projections). Goroutine spawn + channel send is ~hundreds of ns; if your fn is faster than that, sequential `q.Map` / `q.Filter` will outperform.
- **Already-async work**. If your fn is itself starting goroutines or async ops, ParMap is layering more concurrency on concurrency. Reach for `q.AwaitAll` over `[]Future[T]` instead — that's the right shape for fan-in over already-running async work.
- **Memory pressure**. Each in-flight worker holds the function's stack and any closures. For very large slices with heavy per-element memory, the bounded form is essential — but so is sizing the limit deliberately.

## See also

- [`q.Map` / `Filter` / `Fold` / …](data.md) — sequential data ops; same surface, no goroutines.
- [`q.AwaitAll` / `q.AwaitAny`](await_multi.md) — fan-in over `[]Future[T]`. Different concern: parallelism over already-running async vs. starting parallel work from a slice.
- [`q.WithPar` / `q.WithParUnbounded`](par.md#surface) — request-scoped concurrency configuration.
