# q — the question-mark operator for Go

[![CI Status](https://github.com/GiGurra/q/actions/workflows/ci.yml/badge.svg)](https://github.com/GiGurra/q/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/GiGurra/q)](https://goreportcard.com/report/github.com/GiGurra/q)
[![Docs](https://img.shields.io/badge/docs-gigurra.github.io%2Fq-blue)](https://gigurra.github.io/q/)

> **Experimental** — APIs and internals may change. Use at your own risk.

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

## Things you can do with q

A few situations where the flat shape pays off. Each snippet rewrites to ordinary Go at compile time — no runtime overhead, no closures, no panic/recover.

### Wrap an error with context, in one line

```go
user := q.TryE(loadUser(id)).Wrapf("loading user %d", id)
```

`%w` is appended automatically — the original error stays unwrappable via `errors.Is` / `errors.As`. Skip the `Wrapf` and use `q.Try(...)` for a bare bubble.

### Recover from a specific failure mode mid-call

```go
n := q.TryE(strconv.Atoi(s)).Catch(func(e error) (int, error) {
    if errors.Is(e, strconv.ErrSyntax) {
        return 0, nil                 // recover with a default
    }
    return 0, fmt.Errorf("parsing %q: %w", s, e)
})
```

`Catch` is the union of "transform the error" and "substitute a fallback value": `(value, nil)` recovers, `(zero, err)` bubbles.

### Acquire and release a resource in one statement

```go
conn := q.Open(dial(addr)).Release((*Conn).Close)
file := q.Open(os.Open(path)).Release((*os.File).Close)
return process(conn, file)
// On return: file.Close fires first, then conn.Close. LIFO defer order.
```

If `os.Open` fails, `conn` was already opened and `conn.Close` runs. Same semantics as hand-written `defer conn.Close()` chains, half the lines.

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
        q.Bubble(ctx)                 // bubble ctx.Err() if cancelled, no-op otherwise
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
    // bubble: errors.New("q.Require failed codec.go:42: header too short")
    ...
}
```

Validations bubble like every other failure — no `defer recover()` on the caller's side.

### Mid-expression debug print + auto-keyed slog

```go
u := loadUser(q.DebugPrintln(id))
// stderr: "main.go:17 id = 7"  (passes id through unchanged)

slog.Info("loaded", q.DebugSlogAttr(userID))
// → slog.Info("loaded", slog.Any("main.go:42 userID", userID))
```

Both auto-capture the source text and `file:line` at compile time — no retyping the variable name as a key.

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
