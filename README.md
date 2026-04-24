# q — the question-mark operator for Go

[![CI Status](https://github.com/GiGurra/q/actions/workflows/ci.yml/badge.svg)](https://github.com/GiGurra/q/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/GiGurra/q)](https://goreportcard.com/report/github.com/GiGurra/q)
[![Docs](https://img.shields.io/badge/docs-gigurra.github.io%2Fq-blue)](https://gigurra.github.io/q/)

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

## The whole surface at a glance

| Family                              | What it does                                                  |
|-------------------------------------|---------------------------------------------------------------|
| [`q.Try` / `q.TryE`](docs/api/try.md)             | Bubble on `(T, error)`. The 90% case.                         |
| [`q.NotNil` / `q.NotNilE`](docs/api/notnil.md)    | Bubble on a nil pointer.                                      |
| [`q.Check` / `q.CheckE`](docs/api/check.md)       | Bubble on `error` alone (for `db.Ping`, `file.Close`, …).    |
| [`q.Open` / `q.OpenE`](docs/api/open.md)          | `(T, error)` + auto `defer cleanup(v)` on success.            |
| [`q.Trace` / `q.TraceE`](docs/api/trace.md)       | Try-shape, bubble prefixed with compile-time `file:line`.     |
| [`q.Ok` / `q.OkE`](docs/api/ok.md)                | Bubble on `(T, bool)` — general comma-ok.                     |
| [`q.Recv` / `q.RecvE`](docs/api/recv.md)          | Comma-ok specialised to channel receive; bubbles on close.    |
| [`q.As` / `q.AsE`](docs/api/as.md)                | Comma-ok specialised to type assertion.                       |
| [`q.Recover` / `q.RecoverE`](docs/api/recover.md) | `defer q.Recover(&err)` — function-wide panic→error.          |
| [`q.Lock`](docs/api/lock.md)                      | `Lock()` + `defer Unlock()` for any `sync.Locker`.            |
| [`q.Async` / `q.Await` / `q.AwaitE`](docs/api/async.md) | JS-flavour promises on top of goroutines + channels.    |
| [`q.Debug`](docs/api/debug.md)                    | Go's missing `dbg!` — prints `file:line src = value`.         |
| [`q.TODO` / `q.Unreachable`](docs/api/todo.md)    | Rust-style panic markers with file:line.                      |
| [`q.Assert`](docs/api/assert.md)                  | Runtime assertion — panic on false.                           |

Full per-entry reference on the [docs site](https://gigurra.github.io/q/).

<details>
<summary><strong>The full surface</strong> — bare and chain helpers, every method</summary>

### Bare bubble — pass through, propagate unchanged

```go
n := q.Try(strconv.Atoi(s))      // (T, error)  → bubble err
u := q.NotNil(table[id])         // (*T)        → bubble q.ErrNil
v := q.Ok(lookup(k))             // (T, bool)   → bubble q.ErrNotOk
     q.Check(db.Ping())          // error alone → bubble err (stmt only)
c := q.Open(dial(a)).Release((*Conn).Close)  // (T, error) + defer cleanup(v) on success
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

For `(T, bool)` via `q.OkE` — map lookups, type assertions, channel receives:

```go
v := q.OkE(users[id]).Err(ErrNotFound)                           // replace with constant error
v := q.OkE(users[id]).ErrF(func() error { ... })                 // computed error (thunk)
v := q.OkE(users[id]).Wrap("user not found")                     // errors.New(msg)
v := q.OkE(users[id]).Wrapf("no user %d", id)                    // fmt.Errorf(format, args...)
v := q.OkE(users[id]).Catch(func() (User, error) { ... })        // computed value OR error
```

For error-only via `q.CheckE` (void — always an expression statement):

```go
q.CheckE(db.Ping()).Wrap("health check")           // fmt.Errorf("health check: %w", err)
q.CheckE(validate(input)).Err(ErrBadInput)         // replace the bubbled error
q.CheckE(file.Close()).Catch(func(e error) error { // nil = suppress, non-nil = bubble
    if errors.Is(e, io.ErrClosedPipe) { return nil }
    return e
})
```

For resource acquisition via `q.OpenE`, where `.Release` is the terminal and every other method is a pass-through modifier on the bubbled error:

```go
conn := q.OpenE(dial(addr)).Wrap("dialing").Release((*Conn).Close)
conn := q.OpenE(dial(addr)).Catch(func(e error) (*Conn, error) {
    return fallbackConn(), nil                     // recover with a fallback resource
}).Release((*Conn).Close)
```

`Catch` is the most powerful method: returning `(value, nil)` recovers and uses that value in place of the bubble; returning `(zero, err)` bubbles the new error. The other methods are conveniences for the common shapes. For `q.OpenE`, a recovered `value` is what the deferred cleanup fires on — not the failed resource.

### Trace — compile-time file:line prefix on every bubble

```go
row := q.Trace(db.Query(id))
// → fmt.Errorf("users.go:42: %w", err) on the bubble path

row := q.TraceE(db.Query(id)).Wrapf("loading user %d", id)
// → fmt.Errorf("users.go:42: loading user 7: %w", err)
```

### Comma-ok variants — `q.Recv`, `q.As`

```go
msg := q.Recv(inbox)                          // bubble q.ErrChanClosed on close
msg := q.RecvE(inbox).Wrap("reading inbox")

n := q.As[int](x)                             // bubble q.ErrBadAssert on type mismatch
admin := q.AsE[Admin](user).Wrapf("%T is not an admin", user)
```

### Panic handling — `q.Recover`

```go
func doWork() error {                         // error-slot auto-named by the preprocessor
    defer q.Recover()                         // &err wired in; any panic → *q.PanicError
    process()
    return nil
}

defer q.RecoverE().Map(func(r any) error {    // same auto-wire for the chain variant
    return &APIError{Detail: fmt.Sprint(r)}
})
```

### Concurrency — `q.Lock`, `q.Async` / `q.Await`

```go
func (s *Store) Set(k, v string) {
    q.Lock(&s.mu)                             // Lock + defer Unlock
    s.data[k] = v
}

f := q.Async(func() (int, error) { return fetchSize(url) })
size := q.Await(f)                            // blocks, bubbles on err
size := q.AwaitE(f).Wrapf("fetching %s", url)
```

### Dev tools — `q.Debug`, `q.TODO`, `q.Unreachable`, `q.Assert`

```go
u := loadUser(q.Debug(id))                    // prints "main.go:17 id = 7" to DebugWriter
q.TODO("schema v2 parser")                    // panic("q.TODO parser.go:42: schema v2 parser")
q.Unreachable()                               // panic("q.Unreachable parser.go:42")
q.Assert(len(buf) >= 16, "header too short")  // panic on false with file:line prefix
```

### Statement positions

Every value-producing helper (Try, NotNil, Open and their `E` variants) works in five positions:

```go
v := q.Try(call())                       // define — declare a fresh variable
v  = q.Try(call())                       // assign — update an existing one (incl. obj.field, arr[i])
     q.Try(call())                       // discard — bubble on err, drop the value
return q.Try(call()), nil                // return — q.* anywhere inside any return result
x := f(q.Try(call()), q.NotNil(p))       // hoist — q.* nested inside another expression
```

Any combination works — `return q.Try(a()) * q.Try(b()) / q.Try(c()), nil` hoists three Trys, `x := q.Try(Foo(q.Try(Bar())))` handles nesting inside another q.*'s argument, `m[q.Try(k())] = v` handles a q.* in the assignment target. `q.Check` / `q.CheckE` return void, so they only appear as expression statements.

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

**What works.** Every helper, every chain method, every statement position:

- Entries: `q.Try`, `q.NotNil`, `q.Check`, `q.Open` and their `E` variants (`q.TryE`, `q.NotNilE`, `q.CheckE`, `q.OpenE`).
- Chain methods on the `E` variants: `Err`, `ErrF`, `Catch`, `Wrap`, `Wrapf`. `Catch` returns the family-specific recovery shape (`func(error) (T, error)` for Try/OpenE; `func() (*T, error)` for NotNilE; `func(error) error` for CheckE; nil = suppress, non-nil = bubble). `q.Open`'s chain is terminated by `.Release(cleanup)`.
- Statement forms: define, assign, discard, return-position, and hoist (nested inside any expression — including another q.*'s argument). Multiple q.*s per statement compose: `return q.Try(a()) * q.Try(b()), nil`, `x := q.Try(Foo(q.Try(Bar())))`. Closures and anonymous functions work too — each FuncLit uses its own result list for the bubble.

**Parked.** Multi-LHS from a single q.* (`v, w := q.Try(call())`) — would require `q.Try2` / `q.Try3` runtime helpers; see `docs/planning/TODO.md` #16.

When the rewriter encounters an unsupported shape, it emits a `file:line:col: q: …` diagnostic in the same format Go's compiler uses (so editor click-through works) and aborts the build. Half-rewritten code never happens silently.

### Typed-nil-interface guard

The preprocessor runs a `go/types` pass over each user package and requires every q.* error slot to be the built-in `error` interface — not a concrete type that merely satisfies it. A callee like `func Foo() (int, *MyErr)` passed to `q.Try(Foo())` compiles fine under plain Go (`*MyErr` is assignable to `error`), but Go's implicit concrete-to-interface conversion makes a nil `*MyErr` appear as a *non-nil* `error` value — so the rewritten `if err != nil` would fire for a notionally-nil error. q rejects this at build time with a diagnostic naming the offending type.

Three ways to fix a rejected build:

1. Change the callee to return `error` (preferred).
2. Use the `q.ToErr` adapter: `v := q.Try(q.ToErr(Foo()))`. Ships in `pkg/q`, also useful standalone.
3. Convert explicitly at the call site via a closure.

See [Typed-nil guard](https://gigurra.github.io/q/typed-nil-guard/) for the full story, including why this matches mistake #45 in *100 Go Mistakes*.

</details>

## Related work

- [`proven`](https://github.com/GiGurra/proven) — compile-time contracts via `-toolexec`. q reuses proven's link-gate trick.
- [`rewire`](https://github.com/GiGurra/rewire) — compile-time mocking via `-toolexec`. q's preprocessor scaffolding mirrors rewire's shape.

## Acknowledgements

100% vibe coded with [Claude Code](https://claude.ai). AST rewriting and compiler toolchains are well outside my comfort zone.

## License

MIT — see [`LICENSE`](LICENSE).
