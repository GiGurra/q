# q — the question-mark operator for Go

`q` is a `-toolexec` preprocessor that gives Go the flat-error-handling shape Rust has with `?` and Swift has with `try`. Each `q.Try(...)` / `q.NotNil(...)` (and their chain-style siblings `q.TryE(...).Method(...)` / `q.NotNilE(...).Method(...)`) is rewritten at compile time into the conventional `if err != nil { return …, err }` shape — so call sites read flat, generated code is identical to what you would write by hand, and there is zero runtime overhead.

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

## Status

Every entry helper in the public surface is rewritten end-to-end: bare `q.Try` / `q.NotNil` plus all five `q.TryE(...).Method(...)` and all five `q.NotNilE(...).Method(...)` chain methods. Less-common shapes (plain `=` assignment to an existing var, discard form `_ = q.Try(call())`) emit a diagnostic and abort the build so half-rewritten code never happens silently. Roadmap and full architecture: see [`CLAUDE.md`](CLAUDE.md) and [`docs/design.md`](docs/design.md).

## Install

```bash
go install github.com/GiGurra/q/cmd/q@latest
```

The `q` binary is the toolexec shim. Use it via `-toolexec=q`:

```bash
go build -toolexec=q ./...
go test  -toolexec=q ./...
```

If the binary is not on `$PATH`, pass an absolute path: `go build -toolexec=$(go env GOPATH)/bin/q ./...`.

## The full surface

### Bare bubble — pass through, propagate unchanged

```go
n := q.Try(strconv.Atoi(s))      // (T, error) → bubble err
u := q.NotNil(table[id])         // (*T)       → bubble q.ErrNil
```

### Chain — custom error handling at the call site

The chain entries `q.TryE(...)` / `q.NotNilE(...)` carry the captured `(T, error)` or `*T` into a `Result` that exposes a small set of methods. The full `q.TryE(call).Method(args)` expression is rewritten as one unit by the preprocessor.

For `(T, error)` via `q.TryE`:

```go
n := q.TryE(strconv.Atoi(s)).Err(ErrBadInput)                   // replace with constant error
n := q.TryE(strconv.Atoi(s)).ErrF(toDBError)                    // transform: fn(err) error
n := q.TryE(strconv.Atoi(s)).Wrap("parsing")                    // fmt.Errorf("parsing: %w", err)
n := q.TryE(strconv.Atoi(s)).Wrapf("parsing %q", s)             // fmt.Errorf("parsing %q: %w", s, err)
n := q.TryE(strconv.Atoi(s)).Catch(func(e error) (int, error) { // transform OR recover
    if errors.Is(e, strconv.ErrSyntax) {
        return 0, nil                                           //   recover with default
    }
    return 0, fmt.Errorf("parsing %q: %w", s, e)                //   bubble new error
})
```

For `*T` via `q.NotNilE`:

```go
u := q.NotNilE(table[id]).Err(ErrNotFound)                       // replace with constant error
u := q.NotNilE(table[id]).ErrF(func() error { ... })             // computed error (thunk)
u := q.NotNilE(table[id]).Wrap("user not found")                 // errors.New(msg)
u := q.NotNilE(table[id]).Wrapf("no user %d", id)                // fmt.Errorf(format, args...)
u := q.NotNilE(table[id]).Catch(func() (*User, error) { ... })   // computed value OR error
```

`Catch` is the most powerful method: returning `(value, nil)` recovers and uses that value in place of the bubble; returning `(zero, err)` bubbles the new error. The other methods are conveniences for the common shapes.

## How the link gate works

Every helper in `pkg/q` is ordinary Go that the IDE happily type-checks. The package also declares a single bodyless function `_qLink` via `//go:linkname _qLink _q_atCompileTime`, with a package-level `var _ = _qLink` that forces the linker to resolve it. Without `-toolexec=q`, the symbol is unresolved and the link step fails:

```text
$ go build ./cmd/yourapp
yourapp/internal: relocation target _q_atCompileTime not defined
```

With `-toolexec=q`, the toolexec pass injects a no-op companion file into `pkg/q`'s compile that supplies `_q_atCompileTime`, and the link succeeds. **Forgetting the preprocessor is a build failure, not a runtime surprise.**

## How the rewriter (will) work

For each `q.*` call expression encountered in a user package, the preprocessor:

1. Recognises the expression's shape (`q.Try(call)`, `q.TryE(call).Method(args)`, etc.).
2. Looks at the enclosing `FuncDecl`'s return-type list to synthesize the right zero-value tuple.
3. Replaces the expression with the inlined `if err != nil { return zero…, <wrapped err> }` block, threading the value through to the assignment LHS.

The shape is mechanical — no type-level algebra, no SMT. See [`docs/design.md`](docs/design.md) for the full rewriter contract once Phase 2 lands.

## Why? When? Compared to what?

- **vs hand-written `if err != nil { return … }`:** identical generated code, fewer source lines, the value flow stays visible inline.
- **vs `panic`/`recover`-based "try" libraries:** zero runtime overhead, no surprising stack unwinds, idiomatic Go in production.
- **vs the withdrawn Go [`try` proposal](https://github.com/golang/go/issues/32437):** same idea, delivered as a preprocessor instead of a language change. You control the rollout per-module via `-toolexec`.

## Companion projects

- [`proven`](https://github.com/GiGurra/proven) — compile-time contracts (preconditions / postconditions discharged at call sites).
- [`rewire`](https://github.com/GiGurra/rewire) — test-time function & interface mocking, also via `-toolexec`.

`q` reuses the link-gate trick from `proven` and the toolexec scaffolding shape from both.

## License

MIT — see [`LICENSE`](LICENSE).
