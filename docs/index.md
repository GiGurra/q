# Go wild with Q, the funkiest -toolexec preprocessor

[![CI Status](https://github.com/GiGurra/q/actions/workflows/ci.yml/badge.svg)](https://github.com/GiGurra/q/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/GiGurra/q)](https://goreportcard.com/report/github.com/GiGurra/q)

`q` is a `-toolexec` preprocessor that implements rejected Go language proposals (the `?` / `try` operator) plus a playground of helpers Go didn't ship: context cancellation checkpoints, futures and fan-in, panic→error recovery, mutex sugar, runtime preconditions, dev-time prints and `slog.Attr` builders. Every `q.*` call is rewritten at compile time into ordinary Go — call sites read flat, generated code is identical to hand-written error forwarding, runtime overhead is zero.

```go
// Without q
func loadUser(id int) (User, error) {
    row, err := db.Query(id)
    if err != nil {
        return User{}, fmt.Errorf("loading user %d: %w", id, err)
    }
    user, err := parse(row)
    if err != nil {
        return User{}, err
    }
    return user, nil
}

// With q
func loadUser(id int) (User, error) {
    row  := q.TryE(db.Query(id)).Wrapf("loading user %d", id)
    user := q.Try(parse(row))
    return user, nil
}
```

Signatures stay plain Go. There are no special types you have to learn, no closures, no panic/recover. `gopls` and `go vet` see ordinary code, so IDE checking stays green — but **building without the preprocessor fails loudly at link time**, so you cannot silently ship a binary that bypasses the rewrite.

The withdrawn Go [`try` proposal](https://github.com/golang/go/issues/32437) is the same idea, delivered as a preprocessor instead of a language change. You opt in per-module via `-toolexec`.

## What's in q

The core is the **bubble family** — entries that turn a failure (error / nil / not-ok / channel close / type-assertion miss) into an early return at the call site. Each entry has a **bare** form (`q.Try`) for the 90% case and an **`E`-suffixed chain** form (`q.TryE`) with `Wrap` / `Wrapf` / `Err` / `ErrF` / `Catch` methods for shaping the bubble.

Around that core sit a handful of orthogonal helpers — context cancellation, futures, channels, panic recovery, locking, and dev-time markers — that all use the same bubble shape so call sites read uniformly. The full list, alphabetical:

| Helper | What it does |
|--------|--------------|
| [`q.As` / `q.AsE`](api/as.md) | Comma-ok specialised to type assertion; bubbles `q.ErrBadTypeAssert` on miss. |
| [`q.Async`, `q.Await` / `q.AwaitE`](api/async.md) | JS-flavour promises on top of goroutines + channels. |
| [`q.AwaitAll` / `q.AwaitAny`](api/await_multi.md) | Fan-in over many futures: gather all (`[]T`) or first-success-wins (`T`). |
| [`q.AwaitCtx` / `q.AwaitCtxE`](api/await_ctx.md) | ctx-aware future await — bubble on cancel. |
| [`q.CheckCtx` / `q.CheckCtxE`](api/bubble.md) | `ctx.Err()` cancellation checkpoint as a statement. |
| [`q.Check` / `q.CheckE`](api/check.md) | Bubble on `error` alone — for `db.Ping`, `file.Close`, `validate(x)`. Statement-only. |
| [`q.DebugPrintln` / `q.DebugSlogAttr`](api/debug.md) | Go's missing `dbg!` — prints `file:line src = value` mid-expression, or produces an auto-keyed `slog.Attr`. |
| [`q.Lock`](api/lock.md) | `Lock()` + `defer Unlock()` for any `sync.Locker`. |
| [`q.NotNil` / `q.NotNilE`](api/notnil.md) | Bubble on a nil pointer (sentinel `q.ErrNil`). |
| [`q.Ok` / `q.OkE`](api/ok.md) | Bubble on `(T, bool)` — the general comma-ok pattern. |
| [`q.Open` / `q.OpenE`](api/open.md) | `(T, error)` plus `defer cleanup(v)` on success. |
| [`q.Recover` / `q.RecoverE`](api/recover.md) | `defer q.Recover()` — function-wide panic→error. |
| [`q.Recv` / `q.RecvE`](api/recv.md) | Comma-ok specialised to channel receive; bubbles `q.ErrChanClosed` on close. |
| [`q.RecvAny` / `q.Drain` / `q.DrainAll`](api/channel_multi.md) | Multi-channel select / drain-until-close / per-channel drain-all. |
| [`q.RecvCtx` / `q.RecvCtxE`](api/recv_ctx.md) | ctx-aware channel receive — bubble on close or cancel. |
| [`q.Require`](api/require.md) | Runtime precondition — bubble an error when `cond` is false. |
| [`q.SlogAttr` / `q.SlogFile` / `q.SlogLine`](api/slog.md) | Production-grade `slog.Attr` builders with auto-derived keys (source text, file, line). |
| [`q.Timeout` / `q.Deadline`](api/timeout.md) | Derive a child ctx with an auto-`defer cancel()`. |
| [`q.TODO` / `q.Unreachable`](api/todo.md) | Rust-style panic markers with file:line. |
| [`q.Trace` / `q.TraceE`](api/trace.md) | Try-shape with a compile-time `file:line:` prefix on the bubble. |
| [`q.Try` / `q.TryE`](api/try.md) | Bubble on `(T, error)`. The 90% case. |

## Statement positions

Every value-producing helper works in five positions. The preprocessor rewrites all of them; closures, generics, and multiple `q.*` per statement compose:

```go
v := q.Try(call())                       // define
v  = q.Try(call())                       // assign (incl. m[k] = …, obj.field = …)
     q.Try(call())                       // discard — bubble fires, value dropped
return q.Try(call()), nil                // return-position
x := f(q.Try(call()), q.NotNil(p))       // hoist — q.* nested inside any expression
```

`q.Check`, `q.CheckE`, `q.Lock`, `q.CheckCtx`, `q.TODO`, `q.Unreachable`, `q.Require`, and `q.Timeout` / `q.Deadline` are statement-only by design.

## Where to go next

- [Getting Started](getting-started.md) — install, first build, IDE setup, GOCACHE discipline.
- [Examples → Basic bubbling](examples/basic.md) — smallest runnable programs for `q.Try` / `q.NotNil` / `q.Check`.
- [Examples → Error shaping](examples/error-shaping.md) — `Wrap` / `Wrapf` / `Err` / `ErrF` / `Catch` patterns.
- [Examples → Resources](examples/resources.md) — acquire/release with `q.Open`, LIFO cleanup, recovery to a fallback.
- [Typed-nil guard](typed-nil-guard.md) — why the preprocessor rejects `(T, *MyErr)` callees.
- [Design](design.md) — the link gate, the rewriter contract, what's recognised, what isn't.

## Status

Experimental — APIs and internals may change. The public surface listed above is implemented end-to-end across every statement position, with closures, generics, and multi-`q.*`-per-statement nesting all supported. The only currently-parked shape is multi-LHS where `q.*` itself produces multiple `T` values (`v, w := q.Try(call())`) — that needs new runtime helpers; see [TODO #16](https://github.com/GiGurra/q/blob/main/docs/planning/TODO.md#future--parking-lot).
