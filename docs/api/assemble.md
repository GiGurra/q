# Auto-derived dependency injection: `q.Assemble`, `q.AssembleErr`, `q.AssembleE`

You list the recipes; the preprocessor reads each recipe's signature, builds a dependency graph keyed by output type, topo-sorts it, and emits the inlined construction at compile time. No `wire.go` to keep in sync; no runtime container; no `var _ = container.Build()` to forget. The whole graph collapses into one expression at the call site.

```go
server := q.Try(q.AssembleErr[*Server](newConfig, newDB, newServer))
```

## Background — why this exists

Dependency injection in Go usually picks one of three trade-offs:

- **Manual wiring** (`server := newServer(newDB(newConfig()))`). Fine for a handful of types; the order-and-arity bookkeeping rots fast as the graph grows.
- **`google/wire`** — codegen-time DI. The wiring is correct and zero-overhead, but you commit a generated `wire_gen.go` to the repo and re-run `wire` after every signature change.
- **`uber/fx` / `samber/do`** — runtime containers using reflection. Ergonomic at the call site, but errors land at startup and you pay reflection cost on the boot path.

`q.Assemble` keeps the ergonomics of runtime DI and the guarantees of codegen DI without either downside: the dep graph is resolved at preprocess time (build-time errors, no runtime reflection), and there is no separate generated file to keep in sync — the rewrite happens in place.

The model is borrowed from ZIO's [`ZLayer`](https://zio.dev/reference/di/) — list the providers ("layers" in ZIO, "recipes" here), declare the target type, the framework figures out the order. The cooking metaphor maps better onto Go's flat functions-and-types model than ZLayer's monadic composition; **there is no `++` / `>>>` / `>+>` operator**: recipes are listed at the call site, with `slices.Concat` as the grouping primitive when needed.

## Signatures

```go
// Pure assembly — every recipe is f(deps...) T.
func Assemble[T any](recipes ...any) T

// Errored assembly — at least one recipe returns (T, error).
// Composes naturally with q.Try.
func AssembleErr[T any](recipes ...any) (T, error)

// Chain variant — composes with .Wrap, .Wrapf, .Err, .ErrF, .Catch
// from the standard E-variant vocabulary.
func AssembleE[T any](recipes ...any) ErrResult[T]
```

`recipes` is `...any` because Go's type system can't express "any function with any number of inputs and one output." The preprocessor's typecheck pass takes over validation — same shape as `q.Match`'s `value any`. Misuse surfaces as a build-time diagnostic with file:line:col and a dependency-tree visualisation.

## What counts as a recipe

| Form                       | Example                                      | Behaviour                                                         |
|----------------------------|----------------------------------------------|-------------------------------------------------------------------|
| Pure function reference    | `newDB`                                      | Inputs become deps; first return value is the provided type.     |
| Errored function reference | `newDB` (returns `(*DB, error)`)             | Same; bubbles on failure (only valid in `AssembleErr`/`AssembleE`).|
| Inline value               | `cfg`, `&Config{...}`, `loadConfig()`        | The value's type IS the provided type; no inputs required.        |

Top-level funcs, package-qualified funcs (`pkg.NewDB`), and method values (`srv.NewDB`) all work — anything `go/types` resolves as a `*types.Signature`. Function calls (`getRecipe()`) are accepted as inline values: their type is the call's return type. Function-typed *values* (a `func() *Config` variable) are treated as function references, not inline values.

## Happy path — every form

### `q.Assemble[T]` — pure DI

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
server := q.Assemble[*Server](newServer, newCache, newDB, newConfig)
```

The rewriter emits roughly:

```go
server := (func() *Server {
    _qDep0 := newConfig()
    _qDep1 := newDB(_qDep0)
    _qDep2 := newCache(_qDep1)
    _qDep3 := newServer(_qDep1, _qDep2, _qDep0)
    return _qDep3
}())
```

### `q.AssembleErr[T]` — errored, composes with `q.Try`

```go
func newDB(c *Config) (*DB, error) {
    if c.DB == "" { return nil, errors.New("missing db url") }
    return &DB{cfg: c}, nil
}

func newServer(d *DB, c *Config) (*Server, error) { ... }

server := q.Try(q.AssembleErr[*Server](newConfig, newDB, newServer))
```

The IIFE bubbles inside itself, returning `(T, error)`; `q.Try` then bubbles that to the enclosing function as usual. You get one tight call site with the same error-propagation guarantees as hand-rolled wiring.

### `q.AssembleE[T]` — chain variant for shaped errors

```go
server := q.AssembleE[*Server](newConfig, newDB, newServer).
    Wrap("server initialisation failed")
```

The chain methods are the same as `q.TryE`'s — `.Err`, `.ErrF`, `.Wrap`, `.Wrapf`, `.Catch` — and shape the bubbled error before it leaves the function.

### Inline values as recipes

```go
customCfg := &Config{DB: "override"}
server := q.Assemble[*Server](customCfg, newDB, newCache, newServer)
```

Useful for test harnesses, override points, or "I already have this dep, please use it" patterns. Direct ZIO `ZLayer.succeed` analogue.

### Interface inputs satisfied by concrete providers

A recipe can declare an interface input — the resolver matches it against any provider whose output type satisfies the interface (`types.AssignableTo` under the hood). Exact-type matches always win first; the assignability scan only kicks in when no exact provider exists, so tagged services keep their precise routing.

```go
type Greeter interface{ Greet() string }

type EnglishGreeter struct{}
func (EnglishGreeter) Greet() string { return "hello" }

type App struct{ g Greeter }

func newGreeter() *EnglishGreeter { return &EnglishGreeter{} } // produces *EnglishGreeter
func newApp(g Greeter) *App       { return &App{g: g} }        // wants Greeter

app := q.Assemble[*App](newGreeter, newApp) // *EnglishGreeter satisfies Greeter
```

Two concrete providers both satisfying the same interface input is rejected with the disambiguation diagnostic — see [interface ambiguity](#interface-ambiguity) below.

### Constructor return shapes

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

### Inline `context.Context`

Constructors that take `context.Context` work today by passing `ctx` as an inline value. The resolver matches the `context.Context` interface input against the supplied value via interface satisfaction, the same way any other interface input is resolved.

```go
func newDB(ctx context.Context, c *Config) *DB { ... }

ctx := context.WithValue(context.Background(), "k", "v")
s := q.Assemble[*Server](ctx, newConfig, newDB, newServer)
```

A dedicated `q.AssembleCtx[T](ctx, recipes...)` may come in a later phase to unlock ctx-driven extensibility (parallel construction, debug tracing, deterministic-order opt-in) — see the [planning doc](https://github.com/GiGurra/q/blob/main/docs/planning/assemble.md). Until then, the inline-value path covers the use case.

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

s := q.Assemble[*Server](newPrimary, newReplica, newServer)
```

## Sad path — diagnostics

Auto-DI fails differently from a typo: with N recipes a single mistake can propagate as ten "missing" symptoms downstream. Every diagnostic from `q.Assemble` lists EVERY problem found in one pass, and includes the dependency tree the resolver believes the graph looks like — so you fix everything once and rerun.

### Missing recipe

```go
type Server struct{ db *DB; cfg *Config }

func newConfig() *Config                 { return &Config{DB: "x"} }
func newServer(d *DB, c *Config) *Server { return &Server{db: d, cfg: c} }

func main() {
    _ = q.Assemble[*Server](newConfig, newServer) // *DB is missing
}
```

```
./main.go:22:6: q: q.Assemble[*Server] cannot resolve the recipe graph:
  - missing recipe for *DB — needed by #2 (newServer)
What the resolver sees:
- *Server <- recipe #2 [fn]
  - input *DB ?? (no recipe provides this)
  - *Config <- recipe #1 [fn]
Providers supplied: #1→*Config, #2→*Server
```

The tree shows you exactly where the gap is in the graph rooted at `T`.

### Unsatisfiable target

```go
_ = q.Assemble[*Server](newConfig, newDB) // no recipe produces *Server
```

```
./main.go:21:6: q: q.Assemble[*Server] cannot resolve the recipe graph:
  - target type *Server is not produced by any recipe
What the resolver sees:
- target ?? (no recipe provides T)
Providers supplied: #1→*Config, #2→*DB
```

### Duplicate provider

```go
func newConfig()      *Config { ... }
func newOtherConfig() *Config { ... }

_ = q.Assemble[*Config](newConfig, newOtherConfig)
```

```
./main.go:16:6: q: q.Assemble[*Config] cannot resolve the recipe graph:
  - duplicate provider for *Config — recipes #1 (newConfig), #2 (newOtherConfig) all
    produce it; pick one or use q.Tagged to brand the variants
```

The fix is either dropping one or using `q.Tagged` to brand them as distinct dep slots (see the "two databases" example above).

### Dependency cycle

```go
type A struct{ b *B }
type B struct{ a *A }
type Root struct{ a *A }

func newA(b *B) *A       { return &A{b: b} }
func newB(a *A) *B       { return &B{a: a} }
func newRoot(a *A) *Root { return &Root{a: a} }

_ = q.Assemble[*Root](newA, newB, newRoot)
```

```
./main.go:18:6: q: q.Assemble[*Root] cannot resolve the recipe graph:
  - dependency cycle: *A (#1) -> *B (#2) -> *A (#1)
```

The cycle path is traced — not just "you have a cycle" but the actual edges that close it.

### Unused recipe

```go
func unrelated() string { return "stray" }

_ = q.Assemble[*Cache](newConfig, newDB, newCache, unrelated)
```

```
./main.go:21:6: q: q.Assemble[*Cache] cannot resolve the recipe graph:
  - unused recipe(s): #4 (unrelated) — provides string
The target type *Cache requires:
- *Cache <- recipe #3 [fn]
  - *DB <- recipe #2 [fn]
    - *Config <- recipe #1 [fn]
```

`q.Assemble` is strict by default — every recipe must be transitively required by `T`. The dep tree shows what *was* required so you know which recipes to keep. The intent is to catch "I added a recipe but forgot to wire its consumer" early; if you genuinely want to supply optional providers, leave them out and pass them explicitly.

### Interface ambiguity

```go
type Greeter interface{ Greet() string }
func newEN() *EnglishGreeter { ... }
func newES() *SpanishGreeter { ... }
func newApp(g Greeter) *App  { ... }

_ = q.Assemble[*App](newEN, newES, newApp)
```

```
./main.go:N:M: q: q.Assemble[*App] cannot resolve the recipe graph:
  - interface input Greeter (needed by #3 (newApp)) is satisfied by multiple
    providers: #1 (newEN) → *EnglishGreeter, #2 (newES) → *SpanishGreeter —
    narrow the recipe set or use q.Tagged to disambiguate
```

### Errored recipe in pure `q.Assemble`

```go
func newDB(c *Config) (*DB, error) { ... }

_ = q.Assemble[*Server](newConfig, newDB, newServer) // newDB has an error path
```

```
./main.go:N:M: q: q.Assemble[*Server] cannot resolve the recipe graph:
  - recipe #2 (newDB) returns (*DB, error); q.Assemble has no error path —
    use q.AssembleErr or q.AssembleE
```

## Nil-recipe detection

Constructors that return `nil` are a real bug: a recipe of type `func(*Config) *DB` returning `nil` for a configuration error means downstream consumers receive a nil pointer that, when assigned into an interface slot, becomes a non-nil interface holding a nil concrete (Go's classic typed-nil-interface pitfall). The rewriter catches this immediately after the bind, before any implicit interface conversion.

Each step whose output type is *nilable* (pointer, interface, slice, map, chan, func) gets a runtime nil-check on its `_qDep<N>` immediately after the bind:

- **`q.AssembleErr` / `q.AssembleE`** — bubble `fmt.Errorf("...: %w", q.ErrNil)` so callers can `errors.Is(err, q.ErrNil)` to detect the failure mode.
- **`q.Assemble` (pure)** — panic with a message naming the offending recipe. Pure assembly has no error path; a buggy nil constructor surfaces loudly at the recipe site rather than propagating silently.

```go
func newNilDB(c *Config) *DB { return nil } // bug
_, err := q.AssembleErr[*Server](newConfig, newNilDB, newServer)
// err: q.AssembleErr: recipe #2 (newNilDB) returned nil: q: nil value
errors.Is(err, q.ErrNil) // true
```

Value-typed outputs (struct, basic types, arrays) skip the check — they can't be nil.

The check runs on the *bound* `_qDep<N>` value (not on the result of any subsequent interface conversion), so a typed-nil from a buggy concrete constructor can't masquerade as a non-nil interface at the consumer's call site.

## Output shape

The rewriter emits an IIFE so the result is a single expression that drops into any expression position — `:=`, `=`, return, function arg, struct field, anywhere. Errored recipes get a per-step bubble that returns the IIFE's zero plus the captured error; pure recipes get a single bind line.

For `q.AssembleErr` composing with `q.Try`, the IIFE returns `(T, error)` and `q.Try` unpacks it as usual — exactly the same shape as `q.Try(call())` for any other `(T, error)`-returning call.

## Caveats

- **Strict by default.** Unused recipes fail the build. The discipline is intentional — recipe sets that drift over time stay correct only if every member is needed.
- **No variadic recipes.** A recipe like `func newServer(plugins ...Plugin) *Server` can't be auto-resolved (the dep set isn't fixed). Wrap it in a fixed-arity adapter instead.
- **Type identity is `go/types` identity.** Two named types with the same underlying type are still distinct providers — that's how `q.Tagged` works. If you have unintentional collisions (e.g. two packages with `type Config struct{...}`), use type aliases or named wrappers to disambiguate.
- **Recipes can't be `q.*` calls** (in the function-reference position). Inline-value recipes can wrap one (`q.Try(loadCfg())`) since the value-position rewrite triggers the standard q.* hoist and the inline value sees the resolved `_qTmp<N>`.

## Phasing — what's coming

Phase 1 (this page) ships pure / errored / chain recipes with full diagnostics. Phase 2 will add `q.AssembleAll[T]` (multiple legitimate providers of `T`, returns `[]T`) and struct-target multi-output (`q.Assemble[App]` populating each field of `App` from a matching recipe). Phase 3 will integrate with `q.Open`'s deferred-cleanup machinery via `(T, func(), error)`-returning resource recipes. See [`docs/planning/assemble.md`](https://github.com/GiGurra/q/blob/main/docs/planning/assemble.md) for the full plan.

## See also

- [`q.Try` / `q.TryE`](try.md) — the bubble vocabulary `q.AssembleErr` composes with.
- [`q.Tagged`](tagged.md) — phantom-type branding used for the "two databases" pattern.
- [`q.Open`](open.md) — resource lifetime; phase 3 will integrate.
- [ZIO `ZLayer`](https://zio.dev/reference/di/) — the inspiration. The conceptual model maps closely; the operator-heavy composition (`++`, `>>>`, `>+>`) does not.
