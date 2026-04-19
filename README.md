# q — the question-mark operator for Go

> **Experimental** — APIs and internals may change. Use at your own risk.

`q` gives Go the flat error-handling shape Rust has with `?` and Swift has with `try`. Each `q.Try(...)` / `q.NotNil(...)` / chain call is rewritten at compile time into the conventional `if err != nil { return …, err }` shape. Call sites read flat, generated code is identical to hand-written error forwarding, runtime overhead is zero.

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

See [Setup](#setup--ide-and-cache-configuration) below for the dedicated GOCACHE setup that keeps toolexec and non-toolexec builds from contaminating each other.

<details>
<summary><strong>The full surface</strong> — bare and chain helpers, every method</summary>

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

### Statement positions

Every helper works in three positions:

```go
v := q.Try(call())   // define   — declare a fresh variable
v  = q.Try(call())   // assign   — update an existing one (incl. obj.field, arr[i])
     q.Try(call())   // discard  — bubble on err, drop the value
```

The discard form is useful for "must succeed" calls where the return value isn't needed (e.g. `q.NotNil(somePtr)` as a precondition assertion).

</details>

<details>
<summary><strong>How the link gate works</strong> — why forgetting <code>-toolexec</code> fails the build</summary>

Every helper in `pkg/q` is ordinary Go that the IDE happily type-checks. The package also declares a single bodyless function bound via `//go:linkname` to an external symbol that only the `q` preprocessor's toolexec pass supplies:

```go
//go:linkname _qLink _q_atCompileTime
func _qLink()

func init() { _qLink() }   // forces linker resolution
```

Without `-toolexec=q`, the symbol is unresolved and the link fails:

```text
$ go build ./cmd/yourapp
yourapp: relocation target _q_atCompileTime not defined
```

With `-toolexec=q`, the toolexec pass injects a no-op companion file into `pkg/q`'s compile that supplies `_q_atCompileTime`, and the link succeeds. **Forgetting the preprocessor is a build failure, not a runtime surprise.**

The bodies of `q.Try` / `q.NotNil` / etc. are all `panic("q: <name> call site was not rewritten by the preprocessor")`. Production never reaches them — every call site is rewritten away before the user package compiles. If a call ever survives the rewrite (rewriter bug, unsupported pattern), the panic surfaces it loudly.

</details>

<details>
<summary><strong>How the rewriter works</strong> — what each call site becomes</summary>

For each `q.*` call expression in a user package, the preprocessor pattern-matches the expression's full shape (entry helper × chain method × statement form) and emits a textual replacement at the statement boundary. Source bytes outside the matched span stay byte-identical, so non-rewritten regions preserve gofmt-style formatting.

Example rewrites:

```go
// Source                                          // Generated (sketch)
v := q.Try(call())                                  v, _qErr1 := call()
                                                    if _qErr1 != nil { return *new(T), _qErr1 }

v := q.TryE(call()).Wrapf("ctx %v", x)              v, _qErr1 := call()
                                                    if _qErr1 != nil {
                                                        return *new(T), fmt.Errorf("ctx %v: %w", x, _qErr1)
                                                    }

v := q.TryE(call()).Catch(fn)                       v, _qErr1 := call()
                                                    if _qErr1 != nil {
                                                        var _qRet1 error
                                                        v, _qRet1 = (fn)(_qErr1)
                                                        if _qRet1 != nil {
                                                            return *new(T), _qRet1
                                                        }
                                                    }

p := q.NotNil(expr)                                 p := expr
                                                    if p == nil { return *new(T), q.ErrNil }
```

Zero values use the universal `*new(T)` form — works for any type without per-type knowledge of zero-value spellings, and the Go compiler folds it to a constant. When a rewrite needs `fmt` or `errors` and the file doesn't already import them, the rewriter injects the import.

Full design in [`docs/design.md`](docs/design.md).

</details>

<details>
<summary><strong>Setup</strong> — IDE and cache configuration</summary>

Go's build cache key does not include toolexec state. A `pkg/q.a` cached from a plain `go build` (no stub) and one from a `-toolexec=q` build (with stub) have the same key. Mixing them produces:

- `relocation target _q_atCompileTime not defined` — toolexec build reused a stub-less artifact.

Keep toolexec on a dedicated GOCACHE.

**Terminal:**

```bash
alias gobuild-q='GOFLAGS="-toolexec=q" GOCACHE="$HOME/.cache/q-build" go build'
alias gotest-q='GOFLAGS="-toolexec=q" GOCACHE="$HOME/.cache/q-build"  go test'
```

**GoLand:** Run → Edit Configurations → Templates → Go Test → Environment variables:

```
GOFLAGS=-toolexec=q
GOCACHE=/Users/<you>/.cache/q-build
```

**VS Code (settings.json):**

```json
"go.buildEnvVars": {
    "GOFLAGS": "-toolexec=q",
    "GOCACHE": "${env:HOME}/.cache/q-build"
},
"go.testEnvVars": {
    "GOFLAGS": "-toolexec=q",
    "GOCACHE": "${env:HOME}/.cache/q-build"
}
```

Clean the q cache specifically:

```bash
GOCACHE="$HOME/.cache/q-build" go clean -cache
```

</details>

<details>
<summary><strong>Status and limitations</strong></summary>

**What works.** Bare `q.Try` and `q.NotNil`. Every chain method on `q.TryE` and `q.NotNilE` (`Err`, `ErrF`, `Catch`, `Wrap`, `Wrapf`). Every statement form (define, assign, discard).

**Currently rejected with a diagnostic** (planned, but not yet supported):

- Return-position: `return q.Try(call())`.
- Nested-in-call: `f(q.Try(call()))`.
- Multi-LHS.

When the rewriter encounters an unsupported shape, it emits a `file:line:col: q: …` diagnostic in the same format Go's compiler uses (so editor click-through works) and aborts the build. Half-rewritten code never happens silently.

</details>

## Related work

- [`proven`](https://github.com/GiGurra/proven) — compile-time contracts via `-toolexec`. q reuses proven's link-gate trick.
- [`rewire`](https://github.com/GiGurra/rewire) — compile-time mocking via `-toolexec`. q's preprocessor scaffolding mirrors rewire's shape.

## Acknowledgements

100% vibe coded with [Claude Code](https://claude.ai). AST rewriting and compiler toolchains are well outside my comfort zone.

## License

MIT — see [`LICENSE`](LICENSE).
