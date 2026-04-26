# Go wild with Q, the funkiest -toolexec preprocessor

[![CI Status](https://github.com/GiGurra/q/actions/workflows/ci.yml/badge.svg)](https://github.com/GiGurra/q/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/GiGurra/q)](https://goreportcard.com/report/github.com/GiGurra/q)
[![Docs](https://img.shields.io/badge/docs-gigurra.github.io%2Fq-blue)](https://gigurra.github.io/q/)

> **Experimental** — APIs and internals may change. Use at your own risk.

`q` is a `-toolexec` preprocessor that implements rejected Go language proposals. Most `q.*` calls are rewritten at compile time into ordinary Go — call sites read flat, generated code is identical to hand-written error forwarding, runtime overhead is zero.

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
conn := q.Open(dial(addr)).DeferCleanup((*Conn).Close)
file := q.Open(os.Open(path)).DeferCleanup()       // auto: defer file.Close()
ch   := q.Open(makeChan()).DeferCleanup()          // auto: defer close(ch)
return process(conn, file, ch)
// On return, the defers fire LIFO.
```

`.DeferCleanup(cleanup)` takes the cleanup explicitly. `.DeferCleanup()` (no args) lets the preprocessor infer it from the resource type — channel close, `Close() error` (close-time error discarded), or `Close()` (no return). For "we acquired this and we're *not* closing it":

```go
val := q.Open(loadValue(key)).NoDeferCleanup()
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

### Generators (`iter.Seq` sugar)

```go
// q.Yield(v) inside the body becomes `if !yield(v) { return }`;
// the whole expression is rewritten to iter.Seq[int](...) at compile time.
fibs := q.Generator[int](func() {
    a, b := 0, 1
    for { q.Yield(a); a, b = b, a+b }
})

for v := range fibs {
    if v > 100 { break }
    fmt.Println(v)
}
```

`q.Generator[T]` produces a stdlib `iter.Seq[T]`. The type param is required (Go can't infer a result-only type argument). Free interop with `for ... range` and any other `iter.Seq` consumer.

### Bidirectional coroutines

```go
// Lua / Python-generator-send-style: caller passes in, body sends out.
doubler := q.Coro(func(in <-chan int, out chan<- int) {
    for v := range in {
        out <- v * 2
    }
})
defer doubler.Close()

v, _ := doubler.Resume(21) // 42
v, _  = doubler.Resume(100) // 200
```

`q.Coro` wraps a goroutine + two channels into a Resume / Close / Wait / Done API. Useful for stateful conversations `iter.Seq` (one-way pull) can't express. Pure runtime — no preprocessor work. Reach for `q.Generator` for the simpler emit-only case.

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

### Compile-time string interpolation

```go
name := "world"
age  := 42

q.F("hi {name}, {age+1} next year")           // → fmt.Sprintf("hi %v, %v next year", name, age+1)
q.F("upper: {strings.ToUpper(name)}")         // → fmt.Sprintf("upper: %v", strings.ToUpper(name))
q.Ferr("user {id} not found")                 // → fmt.Errorf("user %v not found", id)  (type error)
q.Fln("processing {len(items)} items")        // → fmt.Fprintln(q.DebugWriter, …)
```

`{{` / `}}` escape literal braces. The format must be a Go string literal — dynamic formats are rejected at scan time. Inside `{…}`, anything that parses as a Go expression goes (selectors, function calls, arithmetic, even nested string literals). Tradeoff: identifiers inside the literal aren't IDE-visible — go-to-definition / rename don't see them.

### Value-returning match expression

```go
type Color int
const (Red Color = iota; Green; Blue)

description := q.Match(c,
    q.Case(Red,   "warm"),
    q.Case(Green, "natural"),
    q.Case(Blue,  "cool"),
    // missing Blue → build fails: "missing case(s) for: Blue"
)
```

Folds to an IIFE-wrapped switch — value-returning switch as an expression, the way Scala / Rust / Swift have it. Coverage-checked when V is an enum (same rules as `q.Exhaustive`); `q.Default(...)` opts out for forward-compat scenarios.

### Conditional expression

```go
display := q.Tern(user != nil, user.Name, "anonymous")
// → "anonymous" when user is nil; user.Name when not — and user.Name
//   is never evaluated when user is nil (no nil-deref panic).

v := q.Tern(cached, fast(), slowLookup(key))
// → slowLookup(key) only runs when `cached` is false; fast() never
//   runs when `cached` is false either.

// Chains naturally for multi-way picks:
tier := q.Tern(score >= 90, "A",
         q.Tern(score >= 80, "B",
          q.Tern(score >= 70, "C", "F")))
```

`q.Tern(cond, ifTrue, ifFalse)` returns `ifTrue` when `cond` is true, otherwise `ifFalse`. The preprocessor splices each branch's source span into its own arm of an IIFE — so only the matching branch is evaluated, despite Go's eager arg-eval semantics. Lazy by source-splicing, not by func-thunks.

### Nested-nil safe traversal

```go
theme := q.At(user.Profile.Settings.Theme).Or("light")
// → "light" if user, Profile, or Settings is nil; user.Profile.Settings.Theme otherwise.

// Multiple fallback paths — try in source order, first non-nil wins:
endpoint := q.At(opts.Endpoint).
    OrElse(env.Endpoint).
    OrElse(globalConfig.DefaultEndpoint).
    Or("https://example.com")

// Zero-value terminal:
name := q.At(user.Profile.DisplayName).OrZero()  // "" if any hop is nil
```

`q.At(<chain>)` opens an optional-chaining-style traversal. The rewriter walks each hop, asks go/types whether it's nil-checkable, and emits per-hop guards inside an IIFE. `.OrElse(<alt>)` chains additional paths / values to try; `.Or(<fallback>)` and `.OrZero()` close the chain. Each path's expression is single-eval and evaluated lazily — only when reached. See [`docs/api/at.md`](docs/api/at.md).

### Deferred evaluation

```go
cfg := q.Lazy(loadConfigFromDisk())  // loadConfigFromDisk() has NOT run.
if userRequested {
    settings := cfg.Value()           // first .Value() runs the thunk
    _ = settings
}
// loadConfigFromDisk() never ran if userRequested was false.
```

`q.Lazy(<expr>)` reads as if the expression were eager but the rewriter wraps it in a thunk closure. The first `.Value()` call evaluates the thunk under `sync.Once`; later calls return the cached result. Concurrency-safe by construction. `q.LazyE(<call>)` is the `(T, error)`-shaped sibling — pair `.Value()` with `q.Try` at the consumer. See [`docs/api/lazy.md`](docs/api/lazy.md).

### Required-by-default parameter structs

```go
type LoadOptions struct {
    _       q.FnParams
    Path    string                              // required
    Format  string                              // required
    Timeout time.Duration `q:"optional"`        // optional
}

Load(LoadOptions{Path: "/etc", Format: "yaml"}) // OK
Load(LoadOptions{Path: "/etc"})                  // build error: Format required
```

Add `_ q.FnParams` as a blank field on a parameter struct to flip the polarity: every field is required by default, opt out per field via the `q:"optional"` tag. The preprocessor checks each marked struct literal at its construction site; missing required fields fail the build with a diagnostic naming the gap. See [`docs/api/fnparams.md`](docs/api/fnparams.md).

### Auto-derived dependency injection

```go
type Config struct{ DB string }
type DB     struct{ cfg *Config }
type Server struct{ db *DB; cfg *Config }

func newConfig() *Config              { return &Config{DB: "..."} }
func newDB(c *Config) (*DB, error)    { return &DB{cfg: c}, nil }
func newServer(d *DB, c *Config) *Server { return &Server{db: d, cfg: c} }

// List the recipes; the preprocessor reads each signature, builds the
// dep graph, topo-sorts, and emits the inlined construction. ZIO ZLayer
// in spirit, plain Go functions in shape. No codegen step. No runtime
// reflection. The chain terminator picks the resource-lifetime policy:
//
//   .DeferCleanup()   — returns (T, error). Cleanups fire automatically via
//                  a `defer` injected into the enclosing function (in
//                  reverse-topo order). The fast path.
//
//   .NoDeferCleanup() — returns (T, func(), error). Caller takes manual
//                  ownership of the (idempotent) shutdown closure —
//                  use when lifetime spans more than the function
//                  scope (main / signal handlers / background workers).
//
// Recipes can be (T), (T, error), (T, func()), (T, func(), error),
// or an inline value. Resource shapes (and types with auto-detected
// Close() / Close() error / writable channel) feed cleanups onto
// the chain; the rest pass through. Wrap a recipe in q.PermitNil
// to opt it out of the runtime nil-check when nil IS a valid output.
server := q.Try(q.Assemble[*Server](newConfig, openDB, newServer).DeferCleanup())

// In main, manage shutdown explicitly:
func main() {
    server, shutdown, err := q.Assemble[*Server](newConfig, openDB, newServer).NoDeferCleanup()
    if err != nil { log.Fatal(err) }
    defer shutdown() // reverse-topo, blocking; idempotent
    server.Run()
}

// Pass ctx as an inline-value recipe — recipes that take context.Context
// receive it via interface satisfaction. q.WithAssemblyDebug enables
// per-step trace output for diagnosing wiring.
ctx := q.WithAssemblyDebug(context.Background())
server := q.Unwrap(q.Assemble[*Server](ctx, newConfig, newDB, newServer).DeferCleanup())

// q.AssembleAll[T] for plugin / handler / middleware aggregation —
// every recipe whose output is assignable to T contributes one slice
// element, in declaration order.
plugins := q.Unwrap(q.AssembleAll[Plugin](newAuth, newLog, newMetrics).DeferCleanup())

// q.AssembleStruct[T] decomposes T's fields into separate dep targets.
// Useful when several distinct products share a common dep set —
// shared transitive deps (here *Config, *DB) build only once.
type App struct {
    Server *Server
    Worker *Worker
    Stats  *Stats
}
app := q.Unwrap(q.AssembleStruct[App](newConfig, newDB, newServer, newWorker, newStats).DeferCleanup())
```

When a recipe is missing or duplicated or the graph cycles, the build fails with a tree visualisation of what the resolver sees. See [`docs/api/assemble.md`](docs/api/assemble.md).

### Functional data ops

```go
// Transform, filter, group — pure runtime, no preprocessor magic.
doubled := q.Map(nums, func(n int) int { return n * 2 })
adults  := q.Filter(users, func(u User) bool { return u.Age >= 18 })
byCat   := q.GroupBy(items, func(it Item) string { return it.Cat })
total   := q.Fold(amounts, 0, func(acc, n int) int { return acc + n })
mx      := q.Reduce(scores, func(a, b int) int { if a > b { return a }; return b })

// Fallible variants compose with q.Try / q.TryE — first error short-circuits.
func loadUsers(rows []Row) ([]User, error) {
    return q.TryE(q.MapErr(rows, parseUser)).Wrap("loading users"), nil
}

// Find pairs with q.Ok / q.OkE for "bubble on missing"
func findAdmin(users []User) (User, error) {
    return q.Ok(q.Find(users, isAdmin)), nil
}
```

Functional data ops over slices: `Map`, `FlatMap`, `Filter`, `GroupBy`, `Exists`, `ForAll`, `Find`, `Fold`, `Reduce`, `Distinct`, `DistinctBy`, `Partition`, `Chunk`, `Count`, `Take`, `Drop`. Each fallible op ships in two flavours — bare and `…Err` returning `(result, error)` — designed to flow through `q.Try` / `q.TryE` for the bubble path. Pure runtime helpers; no `…E` chain flavour because `q.TryE(q.MapErr(…)).Wrap(…)` already produces that shape. Iterator (`iter.Seq`) variants are deferred to a follow-up wave. Inspiration: Scala collections and [samber/lo](https://github.com/samber/lo).

### Parallel data ops

```go
// Bounded concurrency, default = runtime.NumCPU(). Limit travels on ctx.
ctx = q.WithPar(ctx, 8)
results := q.Try(q.ParMapErr(ctx, urls, fetchURL))

// Bare versions — read ctx for the limit but ignore cancellation.
doubled := q.ParMap(ctx, items, expensive)

// One goroutine per item (use sparingly).
ctx2 := q.WithParUnbounded(ctx)
results = q.ParMap(ctx2, items, expensive)

// Side-effect fan-out + first-error-wins.
q.Check(q.ParEachErr(ctx, files, upload))
```

`q.ParMap` / `q.ParMapErr`, `q.ParFlatMap` / `q.ParFlatMapErr`, `q.ParFilter` / `q.ParFilterErr`, `q.ParForEach` / `q.ParForEachErr`, `q.ParGroupBy` / `q.ParGroupByErr`, `q.ParExists` / `q.ParExistsErr`, `q.ParForAll` / `q.ParForAllErr`. The worker count rides on `context.Context` via `q.WithPar(ctx, n)` — set once at the request top, every nested ParMap respects it. ctx cancellation triggers `ctx.Err()` bubble in `…Err` variants; bare variants stop dispatching and return partial results. First error wins. Inspiration: [samber/lo PR #858](https://github.com/samber/lo/pull/858) and [github.com/GiGurra/party](https://github.com/GiGurra/party).

### Compile-time reflection

```go
type User struct {
    ID    int    `json:"id"   db:"user_id"`
    Name  string `json:"name" db:"full_name"`
}

q.Fields[User]()                 // []string{"ID", "Name"}
q.TypeName[User]()               // "User"
q.Tag[User]("Name", "json")      // "name"
q.Tag[User]("Name", "db")        // "full_name"
```

Each call folds to a literal at compile time. Useful for codegen-free JSON / CSV / SQL row mappers, schema-derived helpers, and other small cases where pulling in `reflect` is overkill. `q.Tag`'s field+key args must be string literals; field name validated at compile time.

### Compile-time string-case transforms

```go
q.Snake("HelloWorld")       // "hello_world"
q.Snake("XMLHttpRequest")   // "xml_http_request"
q.Camel("hello_world")      // "helloWorld"
q.Pascal("hello_world")     // "HelloWorld"
q.Kebab("HelloWorld")       // "hello-world"
q.Upper(q.Snake("DBHost"))  // "DB_HOST"
```

Each call site folds to a string literal at compile time. Useful for column names, env vars, URL slugs, JSON field names — the codegen-adjacent stuff Go forces you to spell out by hand. Inputs must be string literals; runtime values use the standard `strings` package.

### Injection-safe SQL

```go
s := q.SQL("SELECT * FROM users WHERE id = {id} AND status = {status}")
// s.Query → "SELECT * FROM users WHERE id = ? AND status = ?"
// s.Args  → []any{id, status}
db.QueryRowContext(ctx, s.Query, s.Args...)

// Or with PostgreSQL-style placeholders:
s := q.PgSQL("SELECT * FROM users WHERE id = {id}")  // → "...$1", []any{id}

// Or named-param style:
s := q.NamedSQL("SELECT * FROM users WHERE id = {id}")  // → "...:name1", []any{id}
```

Same `{expr}` surface as `q.F`, but the rewriter physically can't inline user values into the query — `{name}` always becomes a placeholder + an entry in `Args`. The parameterised guarantee is structural, not advisory.

### Compile-time enum helpers

```go
type Color int
const (Red Color = iota; Green; Blue)

q.EnumValues[Color]()           // []Color{Red, Green, Blue}
q.EnumNames[Color]()            // []string{"Red", "Green", "Blue"}
q.EnumName[Color](Green)        // "Green"
q.EnumParse[Color]("Blue")      // (Blue, nil)
q.EnumOrdinal[Color](Blue)      // 2

func (c Color) String() string { return q.EnumName[Color](c) }
```

Each call site folds to a literal slice or an inline switch — no runtime
reflection, no companion code-generator, no `go generate` step. Works for
both `int`-backed and `string`-backed enums (any const-able comparable
type). Constants are discovered by walking the package's
`*types.Const` set at compile time.

### Auto-generated enum methods

```go
type Color int
const (Red Color = iota; Green; Blue)

var _ = q.GenStringer[Color]()         // synthesizes Color.String()
var _ = q.GenEnumJSONStrict[Color]()   // name-based JSON, errors on unknown

type Status string
const (Pending Status = "pending"; Done Status = "done")
var _ = q.GenEnumJSONLax[Status]()     // pass-through JSON, preserves unknown wire values
```

The directives are package-level; the toolexec pass writes a companion `_q_gen.go` with the methods. **Strict** rejects wire values your code doesn't know about. **Lax** preserves them for forward-compat with newer producers — pair with [`q.Exhaustive`](https://gigurra.github.io/q/api/exhaustive/)'s `default:` arm.

### Exhaustive switches

```go
switch q.Exhaustive(c) {        // build fails if any const of c's type is uncovered
case Red:    return "warm"
case Green:  return "natural"
case Blue:   return "cool"
}
```

`q.Exhaustive` is only legal as a switch tag; anywhere else is a diagnostic. Default clauses opt out of the check. The wrapper is stripped at rewrite time — zero runtime overhead.

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
- **[Why builds fail on `*MyErr` returns](https://gigurra.github.io/q/typed-nil-guard/)** — the preprocessor rejects callees that return concrete error types instead of `error`.

## Status

Experimental. The public surface is implemented end-to-end across every statement position, with closures, generics, and multi-`q.*`-per-statement nesting all supported. The only currently-parked shape is multi-LHS where `q.*` itself produces multiple `T` values (`v, w := q.Try(call())`); see [TODO #16](docs/planning/TODO.md#future--parking-lot).

## Related work

- [`proven`](https://github.com/GiGurra/proven) — compile-time contracts via `-toolexec`. q reuses proven's link-gate trick.
- [`rewire`](https://github.com/GiGurra/rewire) — compile-time mocking via `-toolexec`. q's preprocessor scaffolding mirrors rewire's shape.

## Acknowledgements

100% vibe coded with [Claude Code](https://claude.ai). AST rewriting and compiler toolchains are well outside my comfort zone.

## License

MIT — see [`LICENSE`](LICENSE).
