# Auto-derived dependency injection: `q.Assemble`

You list the recipes; the preprocessor reads each recipe's signature, builds a dependency graph keyed by output type, topo-sorts it, and emits the inlined construction at compile time. No `wire.go` to keep in sync; no runtime container; no `var _ = container.Build()` to forget. The whole graph collapses into one expression at the call site.

```go
server, err := q.Assemble[*Server](newConfig, newDB, newServer).DeferCleanup()
```

## Background — why this exists

Dependency injection in Go usually picks one of three trade-offs:

- **Manual wiring** (`server := newServer(newDB(newConfig()))`). Fine for a handful of types; the order-and-arity bookkeeping rots fast as the graph grows.
- **`google/wire`** — codegen-time DI. The wiring is correct and zero-overhead, but you commit a generated `wire_gen.go` to the repo and re-run `wire` after every signature change.
- **`uber/fx` / `samber/do`** — runtime containers using reflection. Ergonomic at the call site, but errors land at startup and you pay reflection cost on the boot path.

`q.Assemble` keeps the ergonomics of runtime DI and the guarantees of codegen DI without either downside: the dep graph is resolved at preprocess time (build-time errors, no runtime reflection), and there is no separate generated file to keep in sync — the rewrite happens in place.

The model is borrowed from ZIO's [`ZLayer`](https://zio.dev/reference/di/) — list the providers ("layers" in ZIO, "recipes" here), declare the target type, the framework figures out the order. The cooking metaphor maps better onto Go's flat functions-and-types model than ZLayer's monadic composition; **there is no `++` / `>>>` / `>+>` operator**: recipes are listed at the call site, with `slices.Concat` as the grouping primitive when needed.

## Signature

```go
func Assemble[T any](recipes ...any) AssemblyResult[T]

func (AssemblyResult[T]) DeferCleanup() (T, error)
func (AssemblyResult[T]) NoDeferCleanup() (T, func(), error)
func (AssemblyResult[T]) WithScope(*q.Scope) (T, error)
```

`q.Assemble` returns a chain handle. The terminator picks the resource-lifetime policy.

### `.DeferCleanup()` — auto-defer (the fast path)

Returns `(T, error)`. The preprocessor injects a `defer` into the *enclosing function* that fires every collected cleanup in reverse-topo order when the function returns. No bookkeeping at the call site.

```go
func boot() (*Server, error) {
    server, err := q.Assemble[*Server](newConfig, openDB, newServer).DeferCleanup()
    if err != nil { return nil, err }
    return server, nil
}
// db.Close() runs when boot returns, regardless of err path.
```

Compose with q.Try / q.Unwrap to drop the err:

```go
server := q.Try(q.Assemble[*Server](recipes...).DeferCleanup())
server := q.Unwrap(q.Assemble[*Server](recipes...).DeferCleanup())
```

### `.NoDeferCleanup()` — caller-managed shutdown

Returns `(T, func(), error)`. The closure fires the cleanup chain in reverse-topo order on demand. Idempotent (wraps `sync.OnceFunc`); duplicate calls are safe — useful when you want both `defer shutdown()` and `context.AfterFunc(ctx, shutdown)` triggers.

```go
func main() {
    server, shutdown, err := q.Assemble[*Server](recipes...).NoDeferCleanup()
    if err != nil { log.Fatal(err) }
    defer shutdown()
    context.AfterFunc(ctx, shutdown) // optional: ctx cancel also triggers
    server.Run()
}
```

Use `.NoDeferCleanup()` when the assembled value's lifetime spans more than the enclosing function — main loops, signal handlers, background workers.

### `.WithScope(scope)` — share built deps across calls

Returns `(T, error)`. The supplied `*q.Scope` owns the lifetime. Each step consults the scope's cache before invoking its recipe; built deps and their cleanups register with the scope on the assembly's success path. Mutually exclusive with `.DeferCleanup()` / `.NoDeferCleanup()` — the scope is the sole lifetime owner.

```go
scope := q.NewScope().DeferCleanup()
server := q.Try(q.Assemble[*Server](newConfig, newDB, newServer).WithScope(scope))
// Later in the same scope, the same recipes hit the cache:
worker := q.Try(q.Assemble[*Worker](newConfig, newDB, newWorker).WithScope(scope))
// newConfig / newDB are not re-invoked; *Config and *DB are pulled from scope.
```

**Cache hits skip both the recipe call AND the cleanup registration.** The dep was already built and registered to a prior assembly's internal scope; reusing it doesn't double-register the cleanup.

**Cache misses build fresh** and stage cleanups onto a per-call internal scope. On the assembly's success path, the rewriter atomically commits the fresh cache entries plus the internal scope as a child of the supplied scope. Closing the supplied scope cascades through the child so per-call deps close together, after later-registered scope entries.

If the scope is closed before or during the assembly, the rewriter returns `(zero, q.ErrScopeClosed)`. Fresh deps built in this call before the close fire their staged cleanups locally; pre-cached deps are not affected (their cleanups were registered earlier and fire when the scope itself closes).

**Inline-value recipes are NOT cached or fetched.** They're per-call user inputs, not shared across assemblies — passing a different `&Config{...}` to two calls in the same scope correctly uses each call's value for that call.

**Concurrent assemblies caveat.** Two assemblies in the same scope may both build the same fresh type before either commits — last-writer-wins on the cache, both cleanups end up registered. Document the orphaning risk; for strict singleflight semantics, layer a `singleflight` wrapper in the recipe.

See [`q.Scope`](scope.md) for the full lifetime container surface — construction terminators, manual `Attach`/`Detach`, subscope nesting.

### Mandatory chain terminator

Bare `q.Assemble[T](...)` — without `.DeferCleanup()`, `.NoDeferCleanup()`, or `.WithScope(scope)` — does not compile (`AssemblyResult[T]` isn't `(T, error)`). The preprocessor surfaces a friendly diagnostic too. Pick one terminator at every call site.

`recipes` is `...any` because Go's type system can't express "any function with any number of inputs and one output". The preprocessor's typecheck pass takes over validation. Errors in the recipe set surface as build-time diagnostics with file:line:col plus a dependency-tree visualisation.

## What counts as a recipe

Every reasonable constructor shape works:

```go
NewX() X                    // value type
NewX() *X                   // pointer
NewX() Ifc                  // interface
NewX() (X, error)           // value type with error path
NewX() (*X, error)          // pointer with error path
NewX() (Ifc, error)         // interface with error path
NewX() (*X, func())         // non-erroring resource — explicit cleanup, no error
NewX() (Ifc, func())        // interface non-erroring resource
NewX() (*X, func(), error)  // resource recipe — explicit cleanup with error path
NewX() (Ifc, func(), error) // interface resource recipe — also valid
```

Any of these can also gain auto-detected cleanup if T has a `Close()` / `Close() error` method or is a writable channel — see [Resource lifetime](#resource-lifetime).

Inline values (`cfg`, `&Config{...}`, `loadConfig()`) are also valid recipes — the value's type IS the provided type, no inputs required. See [Inline values as recipes](#inline-values-as-recipes).

Pointer / interface / slice / map / chan / func outputs are checked for nil at runtime (see [Nil-recipe detection](#nil-recipe-detection)). Use [`q.PermitNil`](#permitting-nil--qpermitnil) to opt a single recipe out when nil IS a valid output. Value-typed outputs (struct, basic types, arrays) skip the check — they can't be nil.

## Resource lifetime

Recipes can opt into lifetime management three ways:

### 1. Auto-detected from T's type (zero ceremony)

When a function recipe returns a type with a recognisable close shape — `Close()`, `Close() error`, or a channel — the resolver auto-attaches a synthesised cleanup. **No wrapper needed for external libraries:**

```go
import "database/sql"

// *sql.DB has Close() error → auto-detected.
func newDB() (*sql.DB, error) {
    return sql.Open("postgres", "...")
}

// chan struct{} → auto-detected (rewrites to close(c)).
func newDoneCh() chan struct{} {
    return make(chan struct{})
}

server, err := q.Assemble[*Server](newDB, newDoneCh, newServer).DeferCleanup()
// db.Close() and close(doneCh) both fire on enclosing-function exit,
// in reverse-topo order. No q.Open boilerplate, no manual wrapping.
```

The auto-detected shapes:

| T shape                | Synthesised cleanup                                     |
|------------------------|---------------------------------------------------------|
| `chan U` / `chan<- U`  | `func() { close(t) }`                                   |
| `<-chan U`             | *(none — never auto-closed; see below)*                 |
| Has `Close()`          | `func() { t.Close() }`                                  |
| Has `Close() error`    | `func() { q.LogCloseErr(t.Close(), "<recipe-label>") }` |

**Receive-only channels (`<-chan U`) are never auto-closed.** Closing a channel is the sender's responsibility, and Go itself rejects `close(c)` on a recv-only channel. A recipe whose declared output type is `<-chan U` is signalling that the channel is being *consumed*, not owned, by the assembly — the producer side lives elsewhere (a goroutine, a network source) and is responsible for its own teardown. Use a bidirectional `chan U` (or `chan<- U` if you want to expose only the send side) when the recipe owns the channel and the assembly should close it.

The `Close() error` path routes through `q.LogCloseErr`, which `slog.Error`s the failure with the recipe label as a structured attr — failed teardown is loud, not silent.

**Inline values are never auto-closed.** A precomputed value passed as a recipe (e.g. `q.Assemble[*App](existingDB, ...)`) belongs to the caller — the assembler doesn't claim ownership. The gate in the resolver is `!step.IsValue && hasCloseShape(T)`.

### 2. Explicit `(T, func(), error)` — full control

When you need custom cleanup (graceful HTTP shutdown with deadline, multi-step tear-down, drain-then-close patterns, etc.) declare the cleanup explicitly:

```go
func openDB(c *Config) (*DB, func(), error) {
    db, err := connectDB(c.URL)
    if err != nil { return nil, nil, err }
    return db, func() { _ = db.Close() }, nil
}

func openHTTPServer(addr string) (*http.Server, func(), error) {
    s := &http.Server{Addr: addr}
    cleanup := func() {
        ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        _ = s.Shutdown(ctx)
    }
    return s, cleanup, nil
}
```

If the explicit cleanup is `nil` on the success path, it's silently skipped — useful for "sometimes-cleanup" recipes.

### 3. Non-erroring `(T, func())` — always-succeeds resources

For ctors that genuinely can't fail (test fixtures, in-memory stores, recipes that spawn a goroutine and return a stop signal), the error slot is noise. Use the 2-return resource shape:

```go
func newWorker() (*Worker, func()) {
    w := &Worker{}
    stop := make(chan struct{})
    go w.run(stop)
    return w, func() { close(stop); w.wait() }
}

func newInMemStore() (*Store, func()) {
    s := newStore()
    return s, func() { s.clear() }
}
```

### Chain terminator semantics

Whether the cleanup is auto-detected or explicit, the chain terminator decides what happens with it:

- **`.DeferCleanup()`** — defer-injected; fires on enclosing function return in reverse-topo order.
- **`.NoDeferCleanup()`** — handed to the caller as an idempotent closure for explicit control.
- **`.WithScope(scope)`** — registered with the supplied `*q.Scope`; fires when the scope closes (defer-style, ctx-bound, or manual — see [`q.Scope`](scope.md)).

In all three cases, **partial-failure cleanup is automatic.** If recipe N fails, the cleanups for recipes 0..N-1 fire in reverse-topo before the error bubbles. The chain emerges intact: nothing leaks even on failure paths.

## Happy path examples

### Basic — function-reference recipes

```go
type Config struct{ DB string }
type DB     struct{ cfg *Config }
type Cache  struct{ db  *DB }
type Server struct{ db *DB; cache *Cache; cfg *Config }

func newConfig() *Config                                  { return &Config{DB: "primary"} }
func newDB(c *Config) *DB                                 { return &DB{cfg: c} }
func newCache(d *DB) *Cache                               { return &Cache{db: d} }
func newServer(d *DB, c *Cache, cfg *Config) *Server      { return &Server{db: d, cache: c, cfg: cfg} }

// Recipes can appear in any order — the preprocessor topo-sorts.
server := q.Unwrap(q.Assemble[*Server](newServer, newCache, newDB, newConfig))
```

The rewriter emits roughly:

```go
server, err := (func() (*Server, error) {
    _qDep0 := newConfig()
    if _qDep0 == nil { return nil, fmt.Errorf("...: %w", q.ErrNil) }
    _qDep1 := newDB(_qDep0)
    if _qDep1 == nil { return nil, fmt.Errorf("...: %w", q.ErrNil) }
    _qDep2 := newCache(_qDep1)
    if _qDep2 == nil { return nil, fmt.Errorf("...: %w", q.ErrNil) }
    _qDep3 := newServer(_qDep1, _qDep2, _qDep0)
    if _qDep3 == nil { return nil, fmt.Errorf("...: %w", q.ErrNil) }
    return _qDep3, nil
}())
// q.Unwrap then panics if err != nil; otherwise returns _qDep3.
```

### Errored recipes

```go
func newDB(c *Config) (*DB, error) {
    if c.DB == "" { return nil, errors.New("missing db url") }
    return &DB{cfg: c}, nil
}

func newServer(d *DB, c *Config) (*Server, error) { ... }

// Inside an (T, error)-returning function:
func boot() (*Server, error) {
    return q.Assemble[*Server](newConfig, newDB, newServer)
}
```

The IIFE bubbles inside itself, returning `(T, error)`. Each errored recipe's error short-circuits with that error; the runtime nil-check fires on each successful step's nilable output.

### Inline values as recipes

```go
customCfg := &Config{DB: "override"}
server := q.Unwrap(q.Assemble[*Server](customCfg, newDB, newCache, newServer))
```

Useful for test harnesses, override points, or "I already have this dep, please use it" patterns. Direct ZIO `ZLayer.succeed` analogue.

### context.Context — just another dependency

ctx isn't special — it's an inline-value recipe like any other. If a recipe takes `context.Context` as input, the resolver matches it to the supplied ctx via interface satisfaction.

```go
func newDB(ctx context.Context, c *Config) *DB { ... }

ctx := context.Background()
server := q.Unwrap(q.Assemble[*Server](ctx, newConfig, newDB, newServer))
```

ctx supplied without any consumer is also fine — `context.Context` is exempt from the unused-recipe check, so passing ctx purely for assembly-config (debug, future hooks) doesn't fail the build:

```go
ctx := q.WithAssemblyDebug(context.Background())
// No recipe takes ctx, but the assembly accepts it and uses it for trace output.
server := q.Unwrap(q.Assemble[*Server](ctx, newConfig, newDB, newServer))
```

### Branded variants — two databases, no special code

When you need two providers of the same underlying type — the classic primary / replica DB pattern — define a distinct named type per variant. The provider map keys on the full Go-named type, so each variant gets its own dep slot:

```go
type DB struct{ name string }
func (d *DB) Query() string { return "Q@" + d.name }

// One line per branded variant. *DB methods promote via embedding,
// so the consumer can call p.Query() / r.Query() naturally — no
// .Value() / unwrap step.
type PrimaryDB struct{ *DB }
type ReplicaDB struct{ *DB }

func newPrimary() PrimaryDB { return PrimaryDB{&DB{name: "primary"}} }
func newReplica() ReplicaDB { return ReplicaDB{&DB{name: "replica"}} }

func newServer(p PrimaryDB, r ReplicaDB) *Server {
    // p.Query() / r.Query() work directly — methods of *DB promote.
    return &Server{primary: p.Query(), replica: r.Query()}
}

s := q.Unwrap(q.Assemble[*Server](newPrimary, newReplica, newServer))
```

This is plain Go — no q-specific type machinery involved. The pattern works because `PrimaryDB` and `ReplicaDB` are *separately-declared* named types (even though both wrap `*DB`), which gives each its own canonical type-key in the resolver's provider map. The variant *names* carry the routing information; method/field access flows through naturally via struct embedding.

### Interface inputs satisfied by concrete providers

A recipe can declare an interface input — the resolver matches it against any provider whose output type satisfies the interface (`types.AssignableTo` under the hood). Exact-type matches always win first; the assignability scan only kicks in when no exact provider exists, so branded variants keep their precise routing.

```go
type Greeter interface{ Greet() string }
type EnglishGreeter struct{}
func (EnglishGreeter) Greet() string { return "hello" }

func newGreeter() *EnglishGreeter { return &EnglishGreeter{} } // produces *EnglishGreeter
func newApp(g Greeter) *App       { return &App{g: g} }        // wants Greeter

app := q.Unwrap(q.Assemble[*App](newGreeter, newApp)) // *EnglishGreeter satisfies Greeter
```

Two concrete providers both satisfying the same interface input is rejected with the disambiguation diagnostic — see [interface ambiguity](#interface-ambiguity) below.

## Composition helpers

### `q.Try` — bubble in (T, error)-returning functions

The standard q bubble — only works in functions whose last return is `error`. Inside `func boot() (*Server, error) { ... }`:

```go
server := q.Try(q.Assemble[*Server](...))
```

Rewrites to `if err != nil { return zero, err }` and binds the success value.

### `q.Unwrap` — panic in main / init / tests

```go
func Unwrap[T any](v T, err error) T  // panics on err; returns v on success
```

Plain runtime function, NOT rewritten. Use when there's no error return path:

```go
func main() {
    server := q.Unwrap(q.Assemble[*Server](newConfig, newDB, newServer))
    server.Run()
}
```

### `q.UnwrapE` — chain variant of Unwrap

```go
func UnwrapE[T any](v T, err error) UnwrapResult[T]
```

Same shape as `q.TryE` but the chain methods panic instead of bubbling. Use when you want a wrapped error or a recovery path in non-bubble contexts:

```go
func main() {
    server := q.UnwrapE(q.Assemble[*Server](...)).Wrap("server init failed")
    server.Run()
}

// .Catch lets the caller recover instead of panicking.
cfg := q.UnwrapE(loadConfig()).Catch(func(error) (*Config, error) {
    return defaultConfig(), nil
})
```

Methods: `.Err(replacement)`, `.ErrF(fn)`, `.Wrap(msg)`, `.Wrapf(format, args...)`, `.Catch(fn)`. All plain runtime; no rewriter pass.

### `q.TryE` — bubble + error shaping

For chain-style error shaping in (T, error) functions:

```go
return q.TryE(q.Assemble[*Server](...)).Wrap("server init"), nil
```

## Sad path — diagnostics

Auto-DI fails differently from a typo: with N recipes a single mistake can propagate as ten "missing" symptoms downstream. Every diagnostic from `q.Assemble` lists EVERY problem found in one pass, and includes the dependency tree the resolver believes the graph looks like — so you fix everything once and rerun.

### Missing recipe

```go
type Server struct{ db *DB; cfg *Config }

func newConfig() *Config                 { return &Config{DB: "x"} }
func newServer(d *DB, c *Config) *Server { return &Server{db: d, cfg: c} }

func main() {
    _, _ = q.Assemble[*Server](newConfig, newServer) // *DB is missing
}
```

```
./main.go:22:9: q: q.Assemble[*Server] cannot resolve the recipe graph:
  - missing recipe for *DB — needed by #2 (newServer)
What the resolver sees:
- *Server <- #2 (newServer) [fn]
  - input *DB ?? (no recipe provides this)
  - *Config <- #1 (newConfig) [fn]
Providers supplied: #1→*Config, #2→*Server
```

The tree shows you exactly where the gap is in the graph rooted at `T`.

### Unsatisfiable target

```go
_, _ = q.Assemble[*Server](newConfig, newDB) // no recipe produces *Server
```

```
./main.go:21:9: q: q.Assemble[*Server] cannot resolve the recipe graph:
  - target type *Server is not produced by any recipe
What the resolver sees:
- target ?? (no recipe provides T)
Providers supplied: #1→*Config, #2→*DB
```

### Duplicate provider

```go
func newConfig()      *Config { ... }
func newOtherConfig() *Config { ... }

_, _ = q.Assemble[*Config](newConfig, newOtherConfig)
```

```
./main.go:16:9: q: q.Assemble[*Config] cannot resolve the recipe graph:
  - duplicate provider for *Config — recipes #1 (newConfig), #2 (newOtherConfig) all
    produce it; pick one or define distinct named types per variant
```

The fix is either dropping one or defining distinct named-type wrappers (e.g. `type PrimaryConfig struct{ *Config }`) so each variant lives in its own dep slot. See [Branded variants](#branded-variants-two-databases-no-special-code).

### Interface ambiguity

```go
type Greeter interface{ Greet() string }
func newEN() *EnglishGreeter { ... }
func newES() *SpanishGreeter { ... }
func newApp(g Greeter) *App  { ... }

_, _ = q.Assemble[*App](newEN, newES, newApp)
```

```
./main.go:N:M: q: q.Assemble[*App] cannot resolve the recipe graph:
  - interface input Greeter (needed by #3 (newApp)) is satisfied by multiple
    providers: #1 (newEN) → *EnglishGreeter, #2 (newES) → *SpanishGreeter —
    narrow the recipe set or define distinct named types per variant
```

### Dependency cycle

```go
type A struct{ b *B }
type B struct{ a *A }
type Root struct{ a *A }

func newA(b *B) *A       { return &A{b: b} }
func newB(a *A) *B       { return &B{a: a} }
func newRoot(a *A) *Root { return &Root{a: a} }

_, _ = q.Assemble[*Root](newA, newB, newRoot)
```

```
./main.go:18:9: q: q.Assemble[*Root] cannot resolve the recipe graph:
  - dependency cycle: *A (#1 (newA)) -> *B (#2 (newB)) -> *A (#1 (newA))
```

The cycle path is traced — not just "you have a cycle" but the actual edges that close it.

### Unused recipe

```go
func unrelated() string { return "stray" }

_, _ = q.Assemble[*Cache](newConfig, newDB, newCache, unrelated)
```

```
./main.go:21:9: q: q.Assemble[*Cache] cannot resolve the recipe graph:
  - unused recipe(s): #4 (unrelated) — provides string
The target type *Cache requires:
- *Cache <- #3 (newCache) [fn]
  - *DB <- #2 (newDB) [fn]
    - *Config <- #1 (newConfig) [fn]
```

`q.Assemble` is strict by default — every recipe must be transitively required by `T`. The dep tree shows what *was* required so you know which recipes to keep.

**Exception:** `context.Context` recipes are exempt from this check. Supplying ctx purely for assembly-config (debug, future hooks) doesn't fail the build even when no recipe consumes it.

## Nil-recipe detection

Constructors that return `nil` are a real bug: a recipe of type `func(*Config) *DB` returning `nil` for a configuration error means downstream consumers receive a nil pointer that, when assigned into an interface slot, becomes a non-nil interface holding a nil concrete (Go's classic typed-nil-interface pitfall). The rewriter catches this immediately after the bind, before any implicit interface conversion.

Each step whose output type is *nilable* (pointer, interface, slice, map, chan, func) gets a runtime nil-check on its `_qDep<N>` immediately after the bind:

```go
func newNilDB(c *Config) *DB { return nil } // bug
_, err := q.Assemble[*Server](newConfig, newNilDB, newServer)
// err: q.Assemble: recipe #2 (newNilDB) returned nil: q: nil value
errors.Is(err, q.ErrNil) // true
```

Value-typed outputs (struct, basic types, arrays) skip the check — they can't be nil.

The check runs on the *bound* `_qDep<N>` value (not on the result of any subsequent interface conversion), so a typed-nil from a buggy concrete constructor can't masquerade as a non-nil interface at the consumer's call site.

## Permitting nil — `q.PermitNil`

Some recipes legitimately return `nil` — an "optional dependency" pattern where the absence of a value is itself meaningful, and downstream consumers are written to handle a nil input. Wrap such a recipe in `q.PermitNil(...)` at the call site to opt out of the runtime nil-check on its bound dep:

```go
// newOptionalCache may return nil ("no cache configured") — and the
// downstream Server is written to skip caching when its cache is nil.
server, err := q.Assemble[*Server](
    newConfig,
    q.PermitNil(newOptionalCache),
    newServer,
).DeferCleanup()
```

`q.PermitNil` is a typed identity (`func PermitNil[T any](recipe T) T { return recipe }`). At runtime it returns the recipe unchanged; the preprocessor detects the wrapper at scan time, unwraps it, and marks the resulting Assemble step so the rewriter skips that step's nil-check.

The wrapper works uniformly across **every** recipe shape — Go's type inference resolves `T` to the recipe's exact type (function reference of any signature, or inline value of any type), and the rest of the resolver sees the unwrapped expression:

```go
q.PermitNil(newPure)        // func() *Pure
q.PermitNil(newErrd)        // func() (*Errd, error)
q.PermitNil(newRes)         // func() (*Res, func(), error)
q.PermitNil(newResNoErr)    // func() (*Res, func())
q.PermitNil(inlineNil)      // *Inline (inline value)
```

`q.PermitNil` only suppresses the recipe's own output check; it has no effect on the consumer's behaviour. Recipes whose output type can't hold nil (struct values, basic types, arrays) pass through unchanged — there's no nil-check to skip.

## Debug tracing

`q.WithAssemblyDebug` enables per-step trace output for any q.Assemble call that consumes the supplied ctx. Each recipe call prints its label to the writer registered on the ctx; useful for diagnosing "why did X get the wrong dep" without re-running with a debugger.

```go
ctx := q.WithAssemblyDebug(context.Background())
server := q.Unwrap(q.Assemble[*Server](ctx, newConfig, newDB, newServer))

// Stderr output (defaults to q.DebugWriter):
// [q.Assemble] ctx provided
// [q.Assemble] step #2 (newConfig)
// [q.Assemble] step #3 (newDB)
// [q.Assemble] step #4 (newServer)
```

For tests where stdout/stderr shouldn't be mutated, use `q.WithAssemblyDebugWriter`:

```go
var buf bytes.Buffer
ctx := q.WithAssemblyDebugWriter(context.Background(), &buf)
_ = q.Unwrap(q.Assemble[*Server](ctx, newConfig, newDB, newServer))
// buf.String() now contains the trace.
```

`q.AssemblyDebugWriter(ctx)` is the corresponding getter — returns the writer registered via `q.WithAssemblyDebug` / `q.WithAssemblyDebugWriter`, or `nil` when neither is on the ctx. Useful inside custom recipes that want to feed the same debug stream.

The ctx is passed as an inline-value recipe — same as any other context.Context. The rewriter detects when a recipe provides `context.Context` and binds the debug writer from it; the conditional is one ctx.Value lookup per step (microseconds when debug is off).

## Caveats

- **Strict by default.** Unused recipes fail the build (except `context.Context` — exempt because it's expected to ride into the assembly for assembly-config). The discipline is intentional — recipe sets that drift over time stay correct only if every member is needed.
- **No variadic recipes.** A recipe like `func newServer(plugins ...Plugin) *Server` can't be auto-resolved (the dep set isn't fixed). Wrap it in a fixed-arity adapter instead.
- **Type identity is `go/types` identity.** Two named types with the same underlying type are still distinct providers — that's how the branded-variants pattern (`type PrimaryDB struct{ *DB }`) works. If you have unintentional collisions (e.g. two packages with `type Config struct{...}`), use named wrappers to disambiguate.

## Multi-provider aggregation: `q.AssembleAll`

When several recipes legitimately produce values of the same type — plugins, handlers, middlewares, anywhere you'd reach for a list-of-implementers pattern — `q.Assemble` is too strict: it rejects with "duplicate provider for T". `q.AssembleAll[T]` opts into the multi-provider shape and returns `([]T, error)`. Every recipe whose output is assignable to `T` contributes one element, in recipe declaration order.

```go
type Plugin interface{ Name() string }

type AuthPlugin struct{}
type LogPlugin struct{}
type MetricsPlugin struct{}

func (AuthPlugin) Name() string    { return "auth" }
func (LogPlugin) Name() string     { return "log" }
func (MetricsPlugin) Name() string { return "metrics" }

func newAuth() Plugin    { return AuthPlugin{} }
func newLog() Plugin     { return LogPlugin{} }
func newMetrics() Plugin { return MetricsPlugin{} }

plugins := q.Unwrap(q.AssembleAll[Plugin](newAuth, newLog, newMetrics))
// plugins: []Plugin{AuthPlugin{}, LogPlugin{}, MetricsPlugin{}}
```

Recipes can also produce different concrete types that all satisfy `T` via interface assignability — common when each plugin is its own struct:

```go
func newAuthImpl()    *AuthPlugin    { return &AuthPlugin{} }
func newLogImpl()     *LogPlugin     { return &LogPlugin{} }
func newMetricsImpl() *MetricsPlugin { return &MetricsPlugin{} }

plugins := q.Unwrap(q.AssembleAll[Plugin](newAuthImpl, newLogImpl, newMetricsImpl))
```

Transitive deps still flow through the same auto-derived graph. Two providers each taking a `*Config`:

```go
func newConfig() *Config       { return &Config{Region: "eu-west-1"} }
func newAuth(c *Config) Plugin { return AuthPlugin{cfg: c} }
func newLog(c *Config) Plugin  { return LogPlugin{cfg: c} }

plugins := q.Unwrap(q.AssembleAll[Plugin](newConfig, newAuth, newLog))
```

The topo sort produces `*Config` exactly once; both plugin recipes share that single instance.

**Differences from `q.Assemble`:**

- Multiple recipes producing `T` is the success case. `q.Assemble` rejects this; `q.AssembleAll` collects them.
- Zero recipes assignable to `T` is still an error (would silently return an empty slice, almost certainly a mistake).
- Duplicate-provider detection still applies for *non-target* types. If two recipes both produce a `*Config` that another recipe consumes as a dep, that's still ambiguous and rejected.
- The IIFE returns `([]T, error)` instead of `(T, error)`. Compose with `q.Try` / `q.Unwrap` / `q.TryE` / `q.UnwrapE` exactly as with `q.Assemble`.

## Struct-target multi-output: `q.AssembleStruct`

When several distinct products share a common dep set, packing them into one struct in a single assembly call avoids the boilerplate of three separate `q.Assemble` calls (each repeating the shared recipes). `q.AssembleStruct[T]` decomposes T (which must be a struct) into its fields and finds a recipe for each field's type. Shared transitive deps build only once.

```go
type App struct {
    Server *Server
    Worker *Worker
    Stats  *Stats
}

app, err := q.AssembleStruct[App](newConfig, newDB, newServer, newWorker, newStats)
```

The rewriter emits roughly:

```go
app, err := (func() (App, error) {
    _qDep0 := newConfig()
    _qDep1 := newDB(_qDep0)        // *DB built once, fed to all 3 fields
    _qDep2 := newServer(_qDep1)
    _qDep3 := newWorker(_qDep1)
    _qDep4 := newStats(_qDep0)
    return App{Server: _qDep2, Worker: _qDep3, Stats: _qDep4}, nil
}())
```

**Field requirements:**

- Every exported field of T needs a recipe whose output type matches (exact-type or interface assignability). Missing a field → per-field diagnostic naming the field.
- Unexported fields work in the same package (the rewritten code lives in T's package, so it can set them). Cross-package unexported fields are rejected.
- Branded fields (`Server PrimaryDB` where `type PrimaryDB struct{ *DB }`) work the same way the branded-variants pattern does — the field type IS the brand.

**Why a separate entry point from `q.Assemble`?**

- `q.Assemble[App]` looks for a recipe that produces `App`. If the user has a `func newApp(...) App` recipe, that runs.
- `q.AssembleStruct[App]` always decomposes into fields. A recipe that produces `App` directly is unused.

The split avoids a precedence rule. You pick the entry, you pick the semantics.

**Slice fields are NOT auto-aggregated.** A field `Plugins []Plugin` requires a recipe whose output is `[]Plugin` — either a hand-written one or `q.AssembleAll[Plugin]` called separately and passed as an inline-value recipe:

```go
plugins := q.Try(q.AssembleAll[Plugin](newAuth, newLog, newMetrics))
app, _ := q.AssembleStruct[App](newConfig, plugins)  // plugins is the []Plugin recipe
```

The auto-aggregation behavior may be added later as an opt-in.

## What's coming

Active surface covers single-call DI, `q.AssembleAll` for multi-provider aggregation, `q.AssembleStruct` for struct-target multi-output, full resource-lifetime management (auto-detected and explicit cleanups, partial-failure rollback, `.DeferCleanup()` / `.NoDeferCleanup()` / `.WithScope(scope)` chain), debug tracing, and `q.PermitNil` for opt-in nilable recipes. No phases are currently in active development.

Parked, may revisit:

- **Parallel construction.** `q.WithAssemblyPar(ctx, n)` riding on the ctx like `q.WithAssemblyDebug` — the rewriter would emit topo waves with a `sync.WaitGroup` per wave. Sequential construction is fast enough for current workloads; revisit if profiles show construction time as a measurable cost.

See [`docs/planning/TODO.md`](https://github.com/GiGurra/q/blob/main/docs/planning/TODO.md) for the persistent backlog.

## See also

- [`q.Try` / `q.TryE`](try.md) — the bubble vocabulary `q.Assemble` composes with.
- [`q.Open`](open.md) — single-resource lifetime helper; same auto-detected cleanup shapes as `q.Assemble` recipes.
- [`q.Scope`](scope.md) — lifetime container used by `.WithScope(scope)`; supports manual `Attach`/`Detach`, subscope nesting, and ctx-bound close.
- [ZIO `ZLayer`](https://zio.dev/reference/di/) — the inspiration. The conceptual model maps closely; the operator-heavy composition (`++`, `>>>`, `>+>`) does not.
