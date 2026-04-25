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

Three families of helpers, each with the same shape contract: the call site reads as ordinary Go, the rewriter folds it to a flat replacement at compile time, and runtime cost is zero where possible.

- **The bubble family.** Failure-shape helpers (`q.Try`, `q.NotNil`, `q.Ok`, `q.Check`, `q.Open`, `q.Trace`, `q.Recv`, `q.As`, `q.Await*`, `q.Recv*`, …). Each turns a failure (`error` / nil / not-ok / channel close / type-assertion miss / ctx cancellation) into an early return. A bare form (`q.Try`) handles the 90% case; an `E`-suffixed chain form (`q.TryE`) exposes `Wrap` / `Wrapf` / `Err` / `ErrF` / `Catch` for shaping the bubble.
- **Compile-time helpers.** Things that fold to a Go literal or AST at preprocess time, with no runtime work: `q.AtCompileTime` (universal escape hatch — run pure Go at preprocess time, splice the result), `q.Enum*` (helpers for the iota-enum pattern), `q.Exhaustive` (compile-checked switch coverage), `q.Match` (value-returning switch), `q.F` / `q.SQL` / string-case transforms, `q.Fields` / `q.Tag` / `q.TypeName` (compile-time reflection), `q.GenStringer` / `q.GenEnumJSON*` (method generators).
- **Runtime helpers.** Things that need real machinery but compose with the rest of q: `q.Async` / `q.Await*` (futures + fan-in), `q.Coro` / `q.Generator` (coroutines + iterators), `q.Lock`, `q.Recover`, `q.Timeout` / `q.Deadline`, `q.Map` / `Filter` / `Fold` / … (functional data ops), `q.ParMap` / `ParFilter` (parallel variants), `q.GoroutineID`.

The left navbar has the full list with one page per feature; that's the authoritative index. This page intentionally does not enumerate everything — it would drift out of sync with the actual surface as q grows.

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
