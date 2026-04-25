# Auto-derived dependency injection: `q.Assemble`

You list the recipes; the preprocessor reads each recipe's signature, builds a dependency graph keyed by output type, topo-sorts it, and emits the inlined construction at compile time. No `wire.go` to keep in sync; no runtime container; no `var _ = container.Build()` to forget. The whole graph collapses into one expression at the call site.

```go
server, err := q.Assemble[*Server](newConfig, newDB, newServer)
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
func Assemble[T any](recipes ...any) (T, error)
```

Just one entry. Always returns `(T, error)`; compose at the call site:

```go
// Inside (T, error)-returning function — q.Try bubbles via the error slot.
server := q.Try(q.Assemble[*Server](newConfig, newDB, newServer))

// In main / init / tests — q.Unwrap panics on err.
server := q.Unwrap(q.Assemble[*Server](newConfig, newDB, newServer))

// Custom error shaping — q.TryE chain.
server := q.TryE(q.Assemble[*Server](...)).Wrap("server init")

// Custom panic shaping in main / tests — q.UnwrapE chain.
server := q.UnwrapE(q.Assemble[*Server](...)).Wrap("server init")

// Tuple form — explicit error handling.
server, err := q.Assemble[*Server](...)
if err != nil { ... }
```

`recipes` is `...any` because Go's type system can't express "any function with any number of inputs and one output". The preprocessor's typecheck pass takes over validation — same shape as `q.Match`'s `value any`. Errors in the recipe set surface as build-time diagnostics with file:line:col plus a dependency-tree visualisation.

## What counts as a recipe

| Form                       | Example                                      | Behaviour                                                         |
|----------------------------|----------------------------------------------|-------------------------------------------------------------------|
| Pure function reference    | `newDB`                                      | Inputs become deps; first return value is the provided type.     |
| Errored function reference | `newDB` (returns `(*DB, error)`)             | Same; bubbles on failure into the assembly's error path.          |
| Inline value               | `cfg`, `&Config{...}`, `loadConfig()`        | The value's type IS the provided type; no inputs required.        |

Top-level funcs, package-qualified funcs (`pkg.NewDB`), and method values (`srv.NewDB`) all work — anything `go/types` resolves as a `*types.Signature`. Function calls (`getRecipe()`) are accepted as inline values: their type is the call's return type. Function-typed *values* (a `func() *Config` variable) are treated as function references, not inline values.

## Constructor return shapes

Every reasonable shape works:

```go
NewX() X            // value type
NewX() *X           // pointer
NewX() Ifc          // interface
NewX() (X, error)   // value type with error path
NewX() (*X, error)  // pointer with error path
NewX() (Ifc, error) // interface with error path
```

Pointer / interface / slice / map / chan / func outputs are checked for nil at runtime (see [Nil-recipe detection](#nil-recipe-detection)). Value-typed outputs (struct, basic types, arrays) skip the check — they can't be nil.

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

### Tagged services — two databases, no special code

`q.Tagged[U, T]` brands two values of the same underlying type as distinct types. Since the provider map keys on the full branded type, two providers of `*DB` tagged differently are treated as separate dep slots — the "two databases" pattern with zero assembler-side support.

```go
type _primary struct{}
type _replica struct{}
type PrimaryDB = q.Tagged[*DB, _primary]
type ReplicaDB = q.Tagged[*DB, _replica]

func newPrimary() PrimaryDB { return q.MkTag[_primary](&DB{name: "primary"}) }
func newReplica() ReplicaDB { return q.MkTag[_replica](&DB{name: "replica"}) }

func newServer(p PrimaryDB, r ReplicaDB) *Server { ... }

s := q.Unwrap(q.Assemble[*Server](newPrimary, newReplica, newServer))
```

### Interface inputs satisfied by concrete providers

A recipe can declare an interface input — the resolver matches it against any provider whose output type satisfies the interface (`types.AssignableTo` under the hood). Exact-type matches always win first; the assignability scan only kicks in when no exact provider exists, so tagged services keep their precise routing.

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
    produce it; pick one or use q.Tagged to brand the variants
```

The fix is either dropping one or using `q.Tagged` to brand them as distinct dep slots.

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
    narrow the recipe set or use q.Tagged to disambiguate
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

### Recipe shape rejections

- **No return values** — `recipe #N (fn) returns no values — recipes must return T or (T, error)`
- **Three-or-more returns** — `recipe #N (fn) returns 3 values; recipes must return T or (T, error)`
- **Non-error second return** — `recipe #N (fn) second return is *MyErr; recipes must return T or (T, error) where the second value is the built-in 'error'` (catches the typed-nil-interface pitfall the same way `q.Try` does)
- **Variadic recipe** — `recipe #N (fn) is variadic; q.Assemble can't infer a fixed dep set for variadic inputs — wrap it in a fixed-arity adapter`

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

The ctx is passed as an inline-value recipe — same as any other context.Context. The rewriter detects when a recipe provides `context.Context` and binds the debug writer from it; the conditional is one ctx.Value lookup per step (microseconds when debug is off).

## Caveats

- **Strict by default.** Unused recipes fail the build (except `context.Context` — exempt because it's expected to ride into the assembly for assembly-config). The discipline is intentional — recipe sets that drift over time stay correct only if every member is needed.
- **No variadic recipes.** A recipe like `func newServer(plugins ...Plugin) *Server` can't be auto-resolved (the dep set isn't fixed). Wrap it in a fixed-arity adapter instead.
- **Type identity is `go/types` identity.** Two named types with the same underlying type are still distinct providers — that's how `q.Tagged` works. If you have unintentional collisions (e.g. two packages with `type Config struct{...}`), use type aliases or named wrappers to disambiguate.
- **Recipes can't be `q.*` calls** in the function-reference position. Inline-value recipes can wrap one (`q.Try(loadCfg())`) since the value-position rewrite triggers the standard q.* hoist.

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

## What's coming

Phase 1 ships the full single-call DI surface plus `q.AssembleAll` for multi-provider aggregation. Future phases:

- **Phase 2b — struct-target multi-output.** `q.Assemble[App]` populates each field of `App` from a matching recipe.
- **Phase 3 — resource lifetime.** `(T, func(), error)`-returning recipes for resources that need cleanup; defer-LIFO teardown integrates with `q.Open`'s escape detection.
- **Phase 4 — parallel construction.** `q.WithAssemblyPar(ctx, n)` rides on the ctx like `q.WithAssemblyDebug`; the rewriter emits topo waves with a `sync.WaitGroup` per wave.

See [`docs/planning/assemble.md`](https://github.com/GiGurra/q/blob/main/docs/planning/assemble.md) for the full plan.

## See also

- [`q.Try` / `q.TryE`](try.md) — the bubble vocabulary `q.Assemble` composes with.
- [`q.Tagged`](tagged.md) — phantom-type branding used for the "two databases" pattern.
- [`q.Open`](open.md) — resource lifetime; phase 3 will integrate.
- [ZIO `ZLayer`](https://zio.dev/reference/di/) — the inspiration. The conceptual model maps closely; the operator-heavy composition (`++`, `>>>`, `>+>`) does not.
