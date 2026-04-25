# Go wild with Q, the funkiest -toolexec preprocessor

[![CI Status](https://github.com/GiGurra/q/actions/workflows/ci.yml/badge.svg)](https://github.com/GiGurra/q/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/GiGurra/q)](https://goreportcard.com/report/github.com/GiGurra/q)
[![Docs](https://img.shields.io/badge/docs-gigurra.github.io%2Fq-blue)](https://gigurra.github.io/q/)

> **Experimental** — APIs and internals may change. Use at your own risk.

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

The withdrawn Go [`try` proposal](https://github.com/golang/go/issues/32437) is the seed; q ships that idea (and a handful of others) as a preprocessor instead of waiting on a language change.

## Things you can do with q

A few situations where the flat shape pays off. Each snippet rewrites to ordinary Go at compile time — no runtime overhead, no closures, no panic/recover.

### Wrap an error with context, in one line

```go
user := q.TryE(loadUser(id)).Wrapf("loading user %d", id)
```

`%w` is appended automatically — the original error stays unwrappable via `errors.Is` / `errors.As`. Skip the `Wrapf` and use `q.Try(...)` for a bare bubble.

### Recover from a specific failure mode mid-call

```go
n := q.TryE(strconv.Atoi(s)).
    RecoverIs(strconv.ErrSyntax, 0).
    Wrapf("parsing %q", s)
```

`.RecoverIs(sentinel, value)` recovers when the captured err matches the sentinel via `errors.Is`. `.RecoverAs((*MyErr)(nil), value)` does the same via `errors.As` for typed errors. Both continue the chain — pair them with a terminal (`Wrap`, `Wrapf`, `Err`, `ErrF`, `Catch`) for the non-matching path. Multiple `Recover*` steps may be chained in source order.

For full control, `.Catch(fn)` takes a `func(error) (T, error)` — return `(v, nil)` to recover, `(zero, err)` to bubble. `q.Const(v)` is a shortcut: `q.TryE(call).Catch(q.Const(0))` always recovers to 0.

### Acquire and release a resource in one statement

```go
conn := q.Open(dial(addr)).Release((*Conn).Close)
file := q.Open(os.Open(path)).Release()       // auto: defer file.Close()
ch   := q.Open(makeChan()).Release()          // auto: defer close(ch)
return process(conn, file, ch)
// On return, the defers fire LIFO.
```

`.Release(cleanup)` takes the cleanup explicitly. `.Release()` (no args) lets the preprocessor infer it from the resource type — channel close, `Close() error` (close-time error discarded), or `Close()` (no return). For "we acquired this and we're *not* closing it":

```go
val := q.Open(loadValue(key)).NoRelease()
```

If `os.Open` fails, `conn` was already opened and `conn.Close` runs. Same semantics as hand-written `defer file.Close()` chains, half the lines.

### Bubble nil pointers, channel closes, type-assertion misses

```go
user := q.NotNil(table[id])           // bubble q.ErrNil if id isn't in the map
msg  := q.Recv(inbox)                 // bubble q.ErrChanClosed when inbox closes
admin := q.AsE[Admin](user).Wrapf("%T is not an admin", user)
```

Each helper picks a different failure shape; the rewrite is the same `if X { return zero, err }` pattern.

### Cancellation as a one-statement checkpoint

```go
func sync(ctx context.Context, items []Item) error {
    for _, it := range items {
        q.CheckCtx(ctx)                 // bubble ctx.Err() if cancelled OR timed out, no-op otherwise
        q.Try(process(it))
    }
    return nil
}
```

For ctx-aware blocking ops, `q.RecvCtx(ctx, ch)` and `q.AwaitCtx(ctx, future)` bubble whichever fires first — cancel or value.

### Auto-cancelled child contexts

```go
ctx = q.Timeout(ctx, 2*time.Second)   // ctx, _qCancel := WithTimeout(...); defer cancel()
ctx = q.Deadline(ctx, deadline)       // same with WithDeadline
```

The required `defer cancel()` is wired in by the rewriter — there's no `cancel` variable to forget about.

### JS-flavour futures, with select-style fan-in

```go
fa := q.Async(func() (Sales, error) { return fetchSales(ctx) })
fb := q.Async(func() (Inventory, error) { return fetchInventory(ctx) })

sales := q.AwaitCtxE(ctx, fa).Wrap("sales")
inv   := q.AwaitCtx(ctx, fb)

results := q.AwaitAll(fa, fb, fc)     // []T in input order; bubble first error
fastest := q.AwaitAny(fa, fb, fc)     // first success wins, errors.Join on all-fail
```

### Multi-channel select and drain

```go
v   := q.RecvAny(chA, chB, chC)       // first value across N channels
all := q.DrainAll(chA, chB, chC)      // [][]T — collected until each closes
```

### Panic → error, function-wide

```go
func handle(req Request) (resp Response, err error) {
    defer q.Recover()                 // any panic becomes a *q.PanicError on err
    return work(req)
}

defer q.RecoverE().Map(func(r any) error {
    return &APIError{Detail: fmt.Sprint(r)}
})
```

The `&err` is wired in from the enclosing signature — no need to type it out.

### Mutex sugar

```go
func (s *Store) Set(k, v string) {
    q.Lock(&s.mu)                     // Lock + defer Unlock
    s.data[k] = v
}
```

### Runtime preconditions, no panic

```go
func encode(buf []byte) (Frame, error) {
    q.Require(len(buf) >= 16, "header too short")
    // bubble: fmt.Errorf("codec.go:42: %s: %w", "header too short", q.ErrRequireFailed)
    // reads as: "codec.go:42: header too short: q.Require failed"
    ...
}

// callers can identify the failure mode:
if errors.Is(err, q.ErrRequireFailed) { ... }
```

Validations bubble like every other failure — no `defer recover()` on the caller's side.

### Production-grade slog attrs

```go
slog.Info("request handled",
    q.SlogAttr(reqID),     // → slog.Any("reqID", reqID)
    q.SlogAttr(elapsed),   // → slog.Any("elapsed", elapsed)
    q.SlogFile(),          // → slog.Any("file", "main.go")
    q.SlogLine())          // → slog.Any("line", 42)
```

Auto-derives keys from the source text / file / line at compile time. No runtime stack walk; everything's a constant the compiler folds in.

### Context-attached attrs (correlation IDs, etc.)

Install once at startup, then attach attrs anywhere in request flow:

```go
q.InstallSlogJSON(nil, nil)   // JSON to os.Stderr (or pass your own base handler)

// in request middleware / handler:
ctx = q.SlogCtx(ctx, q.SlogAttr(reqID), q.SlogAttr(userID))
slog.InfoContext(ctx, "processing")  // record auto-carries reqID + userID
```

Standard Go pattern (a wrapping `slog.Handler` that pulls attrs out of the ctx) — q just gives you the ctx key, the wrapper, and the install one-liner. Accumulating via repeated `q.SlogCtx` calls works: deeper attrs add to whatever the parent already had.

### Dev-time `dbg!` and stderr-flavored slog

```go
u := loadUser(q.DebugPrintln(id))
// stderr: "main.go:17 id = 7"  (passes id through unchanged)

slog.Info("loaded", q.DebugSlogAttr(userID))
// → slog.Info("loaded", slog.Any("main.go:42 userID", userID))
```

The `Debug*` family carries `file:line` *inside the key text* — easy to spot in scrolling stderr, but noisy in shipping logs. Pull these out before merging; reach for `q.SlogAttr` / `q.SlogFile` / `q.SlogLine` for permanent logging.

### Get the goroutine ID Go won't give you

```go
id := q.GoroutineID()        // uint64, the goid in panic traces
slog.Info("processing", q.SlogAttr(id))
```

The `runtime` package deliberately hides goroutine IDs. q's preprocessor
injects a one-line accessor (`getg().goid`) into the stdlib runtime
compile and `//go:linkname`-pulls it from `pkg/q`. Cost: ~1 ns,
just an inlined struct-field read. No stack-walk, no assembly, no
pprof-labels dependency. Loses to a future Go release if Go closes the
linkname loophole; works on Go 1.26.

### Trace a bubble back to its call site

```go
row := q.TraceE(db.Query(id)).Wrapf("loading user %d", id)
// → fmt.Errorf("users.go:42: loading user 7: %w", err) on the bubble
```

Compile-time `file:line` prefix; the wrap and underlying error remain unwrappable.

### Statement positions

Every value-producing helper works in five positions:

```go
v := q.Try(call())                       // define
v  = q.Try(call())                       // assign (incl. m[k] = …, obj.field = …)
     q.Try(call())                       // discard — bubble fires, value dropped
return q.Try(call()), nil                // return-position
x := f(q.Try(call()), q.NotNil(p))       // hoist — q.* nested inside any expression
```

Multiple `q.*` per statement compose:

```go
return q.Try(a()) * q.Try(b()) / q.Try(c()), nil
x := q.Try(Foo(q.Try(Bar())))           // nested q.* inside another q.*'s arg
```

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
