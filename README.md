# q — the question-mark operator for Go

[![CI Status](https://github.com/GiGurra/q/actions/workflows/ci.yml/badge.svg)](https://github.com/GiGurra/q/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/GiGurra/q)](https://goreportcard.com/report/github.com/GiGurra/q)
[![Docs](https://img.shields.io/badge/docs-gigurra.github.io%2Fq-blue)](https://gigurra.github.io/q/)

> **Experimental** — APIs and internals may change. Use at your own risk.

Go's `if err != nil { return …, err }` boilerplate accumulates fast: a function with three fallible calls becomes a fifteen-line return-laundering exercise, the actual logic gets pushed off-screen, and copy-paste mistakes drop or shadow errors silently.

`q` brings the flat shape Rust has with `?` and Swift has with `try` to Go. Each `q.Try(...)` / `q.NotNil(...)` / chain call is rewritten at compile time into the conventional `if err != nil { return …, err }` form. Delivered as a `-toolexec` preprocessor — you opt in per-module.

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

The withdrawn Go [`try` proposal](https://github.com/golang/go/issues/32437) is the same idea, delivered as a preprocessor instead of a language change.

## What's in it

A small bubble core — entries that turn a kind of failure into an early return — plus helpers for the surrounding patterns where Go boilerplate accumulates.

- **Bubble on every common Go failure shape.** `(T, error)`, nil pointers, error-only calls (`db.Ping`), `(T, bool)`, channel close, type assertions. One bare entry per shape (`q.Try`, `q.NotNil`, `q.Check`, `q.Ok`, `q.Recv`, `q.As`).
- **Shape the bubble at the call site.** Each entry has an `E`-suffixed sibling (`q.TryE`, …) with `.Wrap("loading")` / `.Wrapf("...", id)` / `.Err(constErr)` / `.ErrF(fn)` / `.Catch(fn)` — the last one transforms or recovers.
- **Acquire/release in one line.** `q.Open(dial(addr)).Release((*Conn).Close)` bubbles on error, registers `defer cleanup(v)` on success.
- **Context cancellation without ceremony.** `q.Bubble(ctx)` is a one-statement checkpoint. `q.Timeout(ctx, dur)` derives a child ctx with auto-`defer cancel()`. `q.RecvCtx`, `q.AwaitCtx` are ctx-aware versions of channel receive and future await.
- **JS-flavour futures over goroutines.** `q.Async(fn)` returns a `Future[T]`; `q.Await(f)` blocks and bubbles. `q.AwaitAll` gathers, `q.AwaitAny` returns first-success.
- **Multi-channel fan-in.** `q.RecvAny` for first-value-wins select, `q.Drain` / `q.DrainAll` to collect until close.
- **Panic → error.** `defer q.Recover()` auto-wires the function's `error` return so any panic becomes a typed `*q.PanicError` (or whatever shape `RecoverE().Map(fn)` produces).
- **Mutex sugar.** `q.Lock(&mu)` emits `Lock + defer Unlock` for any `sync.Locker`.
- **Runtime preconditions.** `q.Require(cond, "msg")` bubbles an error when `cond` is false — same flat shape as the rest of the bubble family, no panics.
- **Compile-time call-site annotations.** `q.Trace(call)` prefixes the bubbled error with `file:line`; `q.Debug(x)` is Go's missing `dbg!`; `q.TODO` / `q.Unreachable` are panic markers for branches that genuinely shouldn't execute.

Every value-producing helper works in five statement positions — define, assign, discard, return-position, and hoisted inside any expression — so call sites stay where they are most readable.

## Why a preprocessor

Three properties fall out of the design:

- **Zero runtime overhead.** Each `q.*` is rewritten at compile time into the same `if err != nil { return …, err }` shape you would write by hand. No closures, no panic/recover, no reflection.
- **IDE-native.** `gopls`, `go vet`, and editor analyzers see ordinary Go — completion, refactors, type errors all point at the right places.
- **Loud failure on misuse.** Forgetting `-toolexec=q` doesn't silently produce a binary that drops errors — it fails the link with `relocation target _q_atCompileTime not defined`. Same for any rewriter bug that leaves a `q.*` call site untransformed: the helper's body panics with a diagnostic naming itself.

The link gate, the rewrite contract, and the typed-nil-interface guard are documented in [`docs/design.md`](docs/design.md).

## Quick start

```bash
# Install the preprocessor binary
go install github.com/GiGurra/q/cmd/q@latest

# Add the runtime package to your module
go get github.com/GiGurra/q

# Build or test with the preprocessor active
GOFLAGS="-toolexec=q" go build ./...
GOFLAGS="-toolexec=q" go test  ./...
```

[Getting Started](https://gigurra.github.io/q/getting-started/) covers GOCACHE discipline (toolexec and non-toolexec builds shouldn't share a cache), IDE setup for GoLand and VS Code, and a sample CI workflow.

## Read more

- **[Documentation site](https://gigurra.github.io/q/)** — per-helper reference, examples, and design notes.
- **[Design doc](docs/design.md)** — link gate, rewriter contract, what's recognised and what isn't.
- **[Typed-nil guard](https://gigurra.github.io/q/typed-nil-guard/)** — why the preprocessor rejects callees that return `*MyErr` instead of `error`.

## Status

Experimental. The public surface is implemented end-to-end across every statement position, with closures, generics, and multi-`q.*`-per-statement nesting all supported. The only currently-parked shape is multi-LHS where `q.*` itself produces multiple `T` values (`v, w := q.Try(call())`); see [TODO #16](docs/planning/TODO.md#future--parking-lot).

## Related work

- [`proven`](https://github.com/GiGurra/proven) — compile-time contracts via `-toolexec`. q reuses proven's link-gate trick.
- [`rewire`](https://github.com/GiGurra/rewire) — compile-time mocking via `-toolexec`. q's preprocessor scaffolding mirrors rewire's shape.

## Acknowledgements

100% vibe coded with [Claude Code](https://claude.ai). AST rewriting and compiler toolchains are well outside my comfort zone.

## License

MIT — see [`LICENSE`](LICENSE).
