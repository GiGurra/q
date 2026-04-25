# q.Assemble — phase 2 & 3 plan

This document is the resume-point for the remaining `q.Assemble` work. Phase 1 (auto-derived assembly with diagnostics) has shipped — the public surface, semantics, dispatch rules, and diagnostic shapes are documented in [`docs/api/assemble.md`](../api/assemble.md). Read that first; the phase 1 implementation is the foundation phase 2 and 3 build on.

A cold-state reader can pick up here without prior context. Cross-references:

- [`docs/api/assemble.md`](../api/assemble.md) — shipped surface (phase 1).
- [`internal/preprocessor/assemble.go`](../../internal/preprocessor/assemble.go) — typecheck (`resolveAssemble`) + rewriter (`buildAssembleReplacement` / `renderAssembleE`). Phase 2/3 hook in here.
- [`pkg/q/assemble.go`](../../pkg/q/assemble.go) — stubs. Phase 2 adds `q.AssembleAll`; phase 3 keeps the same surface and only changes the rewrite for `(T, func(), error)`-shaped recipes.
- ZIO `ZLayer` is the conceptual reference. Differences carried over from phase 1: no operator composition (`++`, `>>>`, `>+>`), no monadic env / `service[T]` pattern — recipes are listed at the call site.

## Phase 1.5 — context-aware assembly

Constructors that take `context.Context` already work today via the inline-value path: pass `ctx` as a recipe argument and the resolver matches it against the input slot via interface satisfaction. That covers the read-only "thread the context" case.

Phase 1.5 adds a dedicated `q.AssembleCtx` family that makes the context a first-class arg. Two reasons it's worth a dedicated entry:

1. **Discoverability** — users reading `q.AssembleCtx[T](ctx, recipes...)` immediately see ctx is integral to the construction.
2. **Extensibility hooks** — once ctx is reserved, we can attach assembly-time options through `context.Context` values without changing the surface again.

### Surface

```go
func AssembleCtx[T any](ctx context.Context, recipes ...any) T
func AssembleCtxErr[T any](ctx context.Context, recipes ...any) (T, error)
func AssembleCtxE[T any](ctx context.Context, recipes ...any) ErrResult[T]
```

The ctx is automatically registered as a `context.Context` provider in the recipe graph, so existing recipes that take `context.Context` see it without the user having to pass ctx twice.

### Ctx-attached options

Once ctx is on the entry, options ride on `context.Context` values:

- **`q.WithPar(ctx, n int) context.Context`** — opt into parallel construction. The rewriter walks the topo graph in waves: every recipe whose deps are satisfied at wave N runs concurrently in wave N+1 (bounded by `n`). Useful for slow constructors (DB ping, remote config fetch, secret retrieval) where total assembly time matters more than determinism.
- **`q.Serially(ctx) context.Context`** — opt back into single-threaded sequential construction. The default; useful for unwinding a parent ctx that had `WithPar` and explicitly demanding ordered startup (logs / metrics that depend on observation order).
- **`q.WithAssemblyDebug(ctx) context.Context`** — emit each recipe call's name + resolved deps + output type to a writer (configurable; defaults to `q.DebugWriter`). Trace-of-construction for diagnosing "why did X get the wrong dep" quickly.

The ctx-options pattern is intentionally extensible: future hooks (timeout per recipe, retry policies, recipe-name labelling for slog) can be added without touching the surface.

### Implementation hooks

- New scanner family entries: `familyAssembleCtx`, `familyAssembleCtxErr`, `familyAssembleCtxE`. Same recipe-arg capture path; ctx becomes an implicit context.Context provider in `resolveAssemble`.
- `buildAssembleReplacement` reads ctx-options off the supplied ctx at IIFE entry: a small prelude inspects `q.AssemblyParallelism(ctx)` etc. and either runs sequential (current path) or wave-parallel (new path).
- Wave-parallel emission: per wave, a `sync.WaitGroup` + per-recipe goroutine; first error in any goroutine bubbles, others' completions are still awaited (no leaks).

### Phase 1.5 deliverables

1. Stubs for `AssembleCtx` / `AssembleCtxErr` / `AssembleCtxE` and the ctx-options helpers.
2. Scanner — three new families.
3. Typecheck — auto-register ctx as a `context.Context` provider; otherwise reuse `resolveAssemble`.
4. Rewriter — wave-parallel emission gated on the ctx options; debug-trace prelude.
5. Fixtures — sequential ctx-flow, parallel + concurrent recipes, debug trace output.
6. Docs — add an "AssembleCtx + ctx options" section to `docs/api/assemble.md`.

## Phase 2 — selection & multi-output

Two independent additions to the existing graph machinery.

### `q.AssembleAll[T]` — multiple legitimate providers

When several recipes legitimately produce the same type (plugins, handlers, middlewares), `q.Assemble` rejects with "duplicate provider for T". `q.AssembleAll[T]` opts into the multi-provider shape and returns `[]T` of the collected values.

```go
type Plugin interface{ Name() string }

func newAuthPlugin()    Plugin { return &authPlugin{} }
func newLoggingPlugin() Plugin { return &loggingPlugin{} }
func newMetricsPlugin() Plugin { return &metricsPlugin{} }

plugins := q.AssembleAll[Plugin](newAuthPlugin, newLoggingPlugin, newMetricsPlugin)
```

**Implementation hooks:**

- New stubs in `pkg/q/assemble.go`: `AssembleAll[T]`, `AssembleAllErr[T]`, `AssembleAllE[T]`.
- New scanner family entries; same recipe-args capture path as phase 1.
- `resolveAssemble` branches on the family: for AssembleAll, replace the duplicate-provider check with "collect all providers of T" into a `[]int` of recipe indices; T's own dep tree includes every collected provider.
- `buildAssembleReplacement` emits a `[]T{_qDep<i>, _qDep<j>, ...}` literal as the IIFE's return. Errored / chain variants follow the same bubble-on-failure pattern.

### Struct-target multi-output

When the user wants several products from one assembly. Detect that T is a struct type and populate each field from a matching recipe.

```go
type App struct {
    Server *Server
    Worker *Worker
    Stats  *Stats
}

app := q.Try(q.AssembleErr[App](newConfig, newDB, newServer, newWorker, newStats))
```

**Implementation hooks:**

- `resolveAssemble` detects `*types.Named` whose underlying is `*types.Struct`; iterate fields, treat each field's type as a required dep target. Missing fields → diagnostic with the same dep-tree visualisation as phase 1.
- `buildAssembleReplacement` emits a struct literal initialised from the dep temps: `App{Server: _qDep<i>, Worker: _qDep<j>, Stats: _qDep<k>}`.
- Tagged fields (`Server q.Tagged[*Server, _primary]`) work the same way phase 1's tagged services do — the field's type IS the brand.

### Phase 2 deliverables

1. Stubs for `AssembleAll[T]` / `AssembleAllErr[T]` / `AssembleAllE[T]`.
2. Scanner detection — three new families, mirror existing AssembleE chain dispatch.
3. Typecheck — `resolveAssembleAll` (or branch in `resolveAssemble`); struct-target detection.
4. Rewriter — `[]T` literal emission; struct-literal emission.
5. Fixtures — `assemble_all_run_ok`, `assemble_struct_target_run_ok`, plus rejected variants for missing struct fields.
6. Docs — extend `docs/api/assemble.md` with Phase 2 sections; nav unchanged (same page).

## Phase 3 — resource lifetime

The piece that makes `q.Assemble` compete with ZIO's `ZLayer.scoped`. When a recipe acquires a resource that needs cleanup, it returns `(T, func(), error)`; the assembler emits `defer cleanup()` after each successful resource recipe. Reverse-topo teardown is automatic via Go's defer LIFO order.

### Recipe shape

```go
func openDB(c *Config) (*DB, func(), error) {
    db, err := connectDB(c.URL)
    if err != nil { return nil, nil, err }
    return db, func() { _ = db.Close() }, nil
}

func newServer(d *DB, c *Config) (*Server, error) { ... }

server := q.Try(q.AssembleErr[*Server](newConfig, openDB, newServer))
```

**Why `(T, func(), error)` and not `q.OpenResult[T]`:** Simpler shape. Recipes don't need to know about q.Open's chain types. A user who wants q.Open-style cleanup writes the boilerplate (or wraps q.Open in their own helper). Lower coupling, easier to compose with non-q resource APIs.

### Generated code

```go
(func() (*Server, error) {
    _qDep0 := newConfig()
    _qDep1, _qCleanup1, _qAErr1 := openDB(_qDep0)
    if _qAErr1 != nil { return *new(*Server), _qAErr1 }
    defer _qCleanup1()                           // registered ONLY after success
    _qDep2, _qAErr2 := newServer(_qDep1, _qDep0)
    if _qAErr2 != nil { return *new(*Server), _qAErr2 }
    return _qDep2, nil
}())
```

Failure semantics: if recipe N fails, recipes N+1...end never run, and only the cleanups registered before N's failure fire. Standard Go defer-on-error.

Reverse-topo teardown: topo order places dependencies BEFORE dependents, so `defer` registration in topo order fires dependents first — exactly what's needed. No special scheduling.

### Resource escape detection (free)

The IIFE returns a value whose lifetime ends with the IIFE — the deferred cleanups run before the IIFE returns. If the user assigns the assembled result to a longer-lived variable, the resources are dead by the time the variable is used. This is the same use-after-close pattern q.Open already detects via `escape.go`. The escape detector consults the scanner's classified shapes; phase 3 wires q.Assemble's resource-recipe outputs into the same machinery, so the escape diagnostic surfaces automatically. Users who genuinely want a longer-lived resource use the `//q:no-escape-check` opt-out.

### Phase 3 deliverables

1. `resolveAssemble`: detect `(T, func(), error)` recipe signature; mark step as resource-recipe.
2. `buildAssembleReplacement` / `buildAssembleBody`: emit `defer _qCleanup<N>()` after each successful resource recipe call.
3. Wire q.Assemble resource outputs into `escape.go`'s detection.
4. Fixtures: resource recipe with cleanup; chained resources where teardown order matters; failure mid-chain triggers partial cleanup; escape detection catches return-of-resource-through-assemble.
5. Docs — extend `docs/api/assemble.md` with the resource-recipe section.

## Open questions / future considerations

- **Recipe groups via package-level slices.** ZIO has `ZLayer ++ ZLayer` for combining layer sets. In Go, the equivalent is `slices.Concat` over slices of recipe values. q.Assemble accepts `recipes ...any` already; spreading a slice via `recipes...` works. We could ship a tiny helper like `func RecipeSet(recipes ...any) []any` for grouping, but YAGNI — `[]any{newConfig, newDB, newServer}` works as-is.

- **Subset assembly / "request these N types".** Sometimes the user wants several products from one recipe set. Phase 2's struct-target covers this idiomatically. Multi-return form `db, cache := q.Assemble2[*DB, *Cache](...)` would be more direct but adds N families (Assemble2, Assemble3, ...). Skip in favour of struct-target.

- **Provider override.** ZIO has `ZLayer.fresh` / `ZLayer.passthrough` for re-providing a layer in a sub-scope. With q's per-call recipe scope (recipes listed at each q.Assemble call), this falls out — just list the override recipe instead of the original.

- **Did-you-mean suggestions.** When the user forgets a recipe whose type is close to one supplied (e.g. `Config` vs `*Config`, or a typo'd type alias), the diagnostic could suggest the closest match. Stretch goal; phase 1's tree visualisation already grounds the user enough that typo-mistakes are usually obvious.

- **Cross-package recipe types.** Phase 1's qualifier already handles this — type names from external packages are spelled with the package short name. If a recipe lives in a package that's not directly imported by the call site, the type's package needs to be in scope; otherwise the IIFE references an unimportable type. No known production failure mode, but worth a fixture if a real case emerges.

- **Performance.** Topo-sort is O(N²) per call site (each pass checks every recipe's inputs). Real-world recipe sets are small (10s); revisit only if profiles show it.
