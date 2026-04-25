# Go wild with Q, the funkiest -toolexec preprocessor

[![CI Status](https://github.com/GiGurra/q/actions/workflows/ci.yml/badge.svg)](https://github.com/GiGurra/q/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/GiGurra/q)](https://goreportcard.com/report/github.com/GiGurra/q)

`q` is a `-toolexec` preprocessor that implements rejected Go language proposals. Every `q.*` call is rewritten at compile time into ordinary Go — call sites read flat, generated code is identical to hand-written error forwarding, runtime overhead is zero.

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

## What's in q

The core is the **bubble family** — entries that turn a failure (error / nil / not-ok / channel close / type-assertion miss) into an early return at the call site. Each entry has a **bare** form (`q.Try`) for the 90% case and an **`E`-suffixed chain** form (`q.TryE`) with `Wrap` / `Wrapf` / `Err` / `ErrF` / `Catch` methods for shaping the bubble.

Around that core sit a handful of orthogonal helpers — context cancellation, futures, channels, panic recovery, locking, and dev-time markers — that all use the same bubble shape so call sites read uniformly. The full list, alphabetical:

| Helper | What it does |
|--------|--------------|
| [`q.As` / `q.AsE`](api/as.md) | Comma-ok specialised to type assertion; bubbles `q.ErrBadTypeAssert` on miss. |
| [`q.Async`, `q.Await` / `q.AwaitE`](api/async.md) | JS-flavour promises on top of goroutines + channels. |
| [`q.AwaitAll` / `q.AwaitAny`](api/await_multi.md) | Fan-in over many futures: gather all (`[]T`) or first-success-wins (`T`). |
| [`q.AwaitCtx` / `q.AwaitCtxE`](api/await_ctx.md) | ctx-aware future await — bubble on cancel. |
| [`q.Map` / `Filter` / `Fold` / `Reduce` / `GroupBy` / …](api/data.md) | Functional data ops over slices. Bare + `…Err` flavours; pure runtime, compose with `q.Try` / `q.Ok`. |
| [`q.ParMap` / `ParFilter` / `ParEach` / `WithPar`](api/par.md) | Parallel variants — bounded worker pool, concurrency limit carried on `ctx` via `q.WithPar(ctx, n)`. Default `runtime.NumCPU()`. |
| [`q.CheckCtx` / `q.CheckCtxE`](api/bubble.md) | `ctx.Err()` cancellation checkpoint as a statement. |
| [`q.Check` / `q.CheckE`](api/check.md) | Bubble on `error` alone — for `db.Ping`, `file.Close`, `validate(x)`. Statement-only. |
| [`q.DebugPrintln` / `q.DebugSlogAttr`](api/debug.md) | Go's missing `dbg!` — prints `file:line src = value` mid-expression, or produces an auto-keyed `slog.Attr`. |
| [`q.EnumValues` / `EnumName` / `EnumParse` / `EnumValid` / `EnumOrdinal`](api/enums.md) | Compile-time helpers for Go's `const X = iota` enum pattern — list all values, name lookup, parse from name, membership, ordinal. Int- and string-backed. |
| [`q.Exhaustive`](api/exhaustive.md) | `switch q.Exhaustive(v) { … }` — build fails if any constant of `v`'s type is missing from the case clauses (unless a `default:` opts out). Wrapper stripped at rewrite time, zero runtime cost. |
| [`q.GenStringer` / `q.GenEnumJSONStrict` / `q.GenEnumJSONLax`](api/gen.md) | Package-level directives that synthesize `String()` / `MarshalJSON` / `UnmarshalJSON` methods on enum types. Strict errors on unknown wire values; Lax preserves them for forward-compat. |
| [`q.Fields` / `q.AllFields` / `q.TypeName` / `q.Tag`](api/reflection.md) | Compile-time reflection — fold a struct's field names / type name / struct-tag value to a literal at the call site. Codegen-free JSON / CSV / SQL row mappers without runtime `reflect`. |
| [`q.Match` / `q.Case` / `q.Default`](api/match.md) | Value-returning switch as an expression. Coverage-checked when matching on an enum type. |
| [`q.F` / `q.Ferr` / `q.Fln`](api/format.md) | Compile-time `{expr}` string interpolation. `q.F("hi {name}")` → `fmt.Sprintf(...)`. Format must be a string literal. |
| [`q.SQL` / `q.PgSQL` / `q.NamedSQL`](api/sql.md) | Injection-safe parameterised SQL. `{expr}` placeholders lift out as `?` / `$N` / `:nameN` driver binds. |
| [`q.Upper` / `Lower` / `Snake` / `Kebab` / `Camel` / `Pascal` / `Title`](api/string_case.md) | Compile-time string-case transforms — fold a string literal to a string literal at compile time. Tokenisation handles camelCase, PascalCase, kebab-case, snake_case, and acronym runs. |
| [`q.GoroutineID`](api/goroutine_id.md) | Returns the runtime goid Go deliberately hides — via runtime-package injection. ~1ns. |
| [`q.Lock`](api/lock.md) | `Lock()` + `defer Unlock()` for any `sync.Locker`. |
| [`q.NotNil` / `q.NotNilE`](api/notnil.md) | Bubble on a nil pointer (sentinel `q.ErrNil`). |
| [`q.Ok` / `q.OkE`](api/ok.md) | Bubble on `(T, bool)` — the general comma-ok pattern. |
| [`q.Open` / `q.OpenE`](api/open.md) | `(T, error)` plus `defer cleanup(v)` on success. |
| [`q.Recover` / `q.RecoverE`](api/recover.md) | `defer q.Recover()` — function-wide panic→error. |
| [`q.Recv` / `q.RecvE`](api/recv.md) | Comma-ok specialised to channel receive; bubbles `q.ErrChanClosed` on close. |
| [`q.RecvAny` / `q.Drain` / `q.DrainAll`](api/channel_multi.md) | Multi-channel select / drain-until-close / per-channel drain-all. |
| [`q.RecvCtx` / `q.RecvCtxE`](api/recv_ctx.md) | ctx-aware channel receive — bubble on close or cancel. |
| [`q.Require`](api/require.md) | Runtime precondition — bubble an error when `cond` is false. |
| [`q.SlogAttr` / `q.SlogCtx` / `q.File` / `q.Line` / `q.Expr` ...](api/slog.md) | Compile-time info builders + plain-string/int counterparts + context-attached attrs (correlation IDs across a request). |
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
- [Typed-nil guard](typed-nil-guard.md) — why the preprocessor rejects `(T, *MyErr)` callees.
- [Design](design.md) — the link gate, the rewriter contract, what's recognised, what isn't.

## Status

Experimental — APIs and internals may change. The public surface listed above is implemented end-to-end across every statement position, with closures, generics, and multi-`q.*`-per-statement nesting all supported. The only currently-parked shape is multi-LHS where `q.*` itself produces multiple `T` values (`v, w := q.Try(call())`) — that needs new runtime helpers; see [TODO #16](https://github.com/GiGurra/q/blob/main/docs/planning/TODO.md#future--parking-lot).
