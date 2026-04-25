# q.Assemble — phase 2+ plan (resume point after context clear)

This document is the resume-point for the remaining `q.Assemble` work. Phase 1 (single-entry auto-derived DI with full diagnostics, ctx-as-inline-value, runtime nil detection, debug tracing, and q.Unwrap helpers) has shipped. Read these first; phase 2/3/4 build on top.

A cold-state reader can pick up from this doc plus the references below.

## Where things live

- **API doc:** [`docs/api/assemble.md`](../api/assemble.md) — current public surface, full happy/sad-path coverage, every diagnostic shape with examples.
- **Stubs:** [`pkg/q/assemble.go`](../../pkg/q/assemble.go) — `Assemble[T](recipes ...any) (T, error)`, `WithAssemblyDebug`, `WithAssemblyDebugWriter`, `AssemblyDebugWriter`. `q.Unwrap` and `q.UnwrapE` live in [`pkg/q/q.go`](../../pkg/q/q.go) (plain runtime; not rewritten).
- **Implementation:** [`internal/preprocessor/assemble.go`](../../internal/preprocessor/assemble.go) — `resolveAssemble` (typecheck), `buildAssembleReplacement` + `buildAssembleBody` (rewriter). New phases hook in here.
- **Scanner:** [`internal/preprocessor/scanner.go`](../../internal/preprocessor/scanner.go) — `familyAssemble`, `qSubCall.AssembleRecipes`, `qSubCall.AssembleCtxDepKey` (set at resolve time when a recipe provides `context.Context`).
- **Tests:**
  - **Unit tests:** [`internal/preprocessor/assemble_unit_test.go`](../../internal/preprocessor/assemble_unit_test.go) — sub-millisecond per case, table-driven against the resolver. **Add new diagnostic cases here first; e2e fixture follows.**
  - **E2E fixtures:** `internal/preprocessor/testdata/cases/assemble_*` — full toolexec build cycle, ~0.5s each. The integration guarantee.

## What phase 1 already ships

- Single entry: `q.Assemble[T](recipes ...any) (T, error)` — composes with `q.Try` / `q.TryE` / `q.Unwrap` / `q.UnwrapE` at the call site.
- Function-reference and inline-value recipes; method values; pkg-qualified funcs.
- All return shapes (T / *T / Ifc + their (T, error) variants).
- Interface inputs satisfied by concrete providers via `types.AssignableTo`. Exact-type wins first (q.Tagged keeps precise routing).
- ctx is just an inline value — recipes that take `context.Context` get matched via interface satisfaction. ctx supplied for assembly-config (debug, future hooks) is exempt from the unused-recipe check.
- Runtime nil-check on every nilable output (pointer/interface/slice/map/chan/func) — bubbles `fmt.Errorf("...: %w", q.ErrNil)` so callers can `errors.Is(err, q.ErrNil)`. Catches the typed-nil-interface pitfall before downstream consumers see it.
- Debug tracing via `q.WithAssemblyDebug` (writer defaults to `q.DebugWriter`) or `q.WithAssemblyDebugWriter(w)`. Per-recipe trace lines emitted to the writer.
- Comprehensive diagnostics — every problem in one pass with dep-tree visualisation: missing dep, unsatisfiable target, duplicate provider, interface ambiguity, dependency cycle (with traced edges), unused recipe, recipe-shape errors (no return / too many / non-error second / variadic).
- 21 e2e fixtures + 14 unit tests (table-driven).

ZIO features intentionally NOT carried over:
- **Composition operators (`++`, `>>>`, `>+>`)** — don't fit Go syntax. Recipes are listed at the call site; group via `[]any{...}` + variadic spread when needed.
- **Service pattern (`ZIO.service[DB]`)** — needs ZIO's monadic env. Replaced by named function inputs.
- **Failures vs defects** — Go has one error.

## Phase 2 — selection & multi-output

Two independent additions to the existing graph machinery.

### 2a. `q.AssembleAll[T]` — multiple legitimate providers

Plugin / handler / middleware aggregation. When several recipes legitimately produce the same type, `q.Assemble` rejects with "duplicate provider for T". `q.AssembleAll[T]` opts into the multi-provider shape and returns `([]T, error)`.

```go
type Plugin interface{ Name() string }

func newAuthPlugin()    Plugin { return &authPlugin{} }
func newLoggingPlugin() Plugin { return &loggingPlugin{} }
func newMetricsPlugin() Plugin { return &metricsPlugin{} }

plugins, err := q.AssembleAll[Plugin](newAuthPlugin, newLoggingPlugin, newMetricsPlugin)
```

**Implementation hooks:**

- New stub `AssembleAll[T any](recipes ...any) ([]T, error)` in `pkg/q/assemble.go`.
- New family entry `familyAssembleAll` in scanner; same recipe-arg capture path as `familyAssemble`.
- `resolveAssemble` branches on the family: for AssembleAll, replace the duplicate-provider check with "collect all providers of T" into a `[]int` of recipe indices. T's own dep tree includes every collected provider transitively.
- `buildAssembleReplacement` emits a `[]T{_qDep<i>, _qDep<j>, ...}` literal as the IIFE's return value. Each producer's other deps are still constructed and topo-ordered normally.
- Errored recipes still bubble; nilable outputs still get the nil-check.

### 2b. Struct-target multi-output

When the user wants several products from one assembly. Detect that T is a struct type and populate each field from a matching recipe.

```go
type App struct {
    Server *Server
    Worker *Worker
    Stats  *Stats
}

app, err := q.Assemble[App](newConfig, newDB, newServer, newWorker, newStats)
```

**Implementation hooks:**

- `resolveAssemble` detects `T`'s underlying is `*types.Struct`; iterate fields, treat each field's type as a required dep target. Missing fields → diagnostic with the same dep-tree visualisation.
- `buildAssembleReplacement` emits a struct literal initialised from the dep temps: `App{Server: _qDep<i>, Worker: _qDep<j>, Stats: _qDep<k>}`.
- Tagged fields (`Server q.Tagged[*Server, _primary]`) work the same way phase 1's tagged services do — the field's type IS the brand.
- Be careful: a struct returned from a recipe (recipe whose output IS the target struct type) takes precedence over field-by-field decomposition. Detection rule: if `providersByKey[targetKey]` has a provider, use it directly (single-recipe path); otherwise decompose into fields.

### Phase 2 deliverables

1. `pkg/q/assemble.go`: `AssembleAll[T any](recipes ...any) ([]T, error)` stub.
2. Scanner: `familyAssembleAll` + dispatch.
3. `resolveAssemble`: branch on family; struct-target detection.
4. `buildAssembleReplacement` / `buildAssembleBody`: `[]T` literal + struct literal emit.
5. **Unit tests** in `assemble_unit_test.go` — add cases for AssembleAll-with-2-plugins, struct-target-happy-path, struct-target-missing-field. Iterate against these (sub-ms each).
6. E2E fixtures: `assemble_all_run_ok`, `assemble_all_typed_run_ok`, `assemble_struct_target_run_ok`, `assemble_struct_target_missing_field_rejected`.
7. Extend `docs/api/assemble.md` with Phase 2 sections.

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

server := q.Try(q.Assemble[*Server](newConfig, openDB, newServer))
```

**Why `(T, func(), error)` and not `q.OpenResult[T]`:** Simpler shape. Recipes don't need to know about `q.Open`'s chain types. A user who wants q.Open-style cleanup writes the boilerplate (or wraps q.Open in their own helper).

### Generated code

```go
(func() (*Server, error) {
    _qDep0 := newConfig()
    if _qDep0 == nil { return nil, fmt.Errorf("...: %w", q.ErrNil) }
    _qDep1, _qCleanup1, _qAErr1 := openDB(_qDep0)
    if _qAErr1 != nil { return nil, _qAErr1 }
    if _qDep1 == nil { return nil, fmt.Errorf("...: %w", q.ErrNil) }
    defer _qCleanup1()                          // registered ONLY after success
    _qDep2, _qAErr2 := newServer(_qDep1, _qDep0)
    if _qAErr2 != nil { return nil, _qAErr2 }
    return _qDep2, nil
}())
```

Failure semantics: if recipe N fails, recipes N+1...end never run, and only the cleanups registered before N's failure fire. Standard Go defer-on-error.

Reverse-topo teardown: topo order places dependencies BEFORE dependents, so `defer` registration in topo order fires dependents first — exactly what's needed. No special scheduling.

### Resource escape detection (free)

The IIFE returns a value whose lifetime ends with the IIFE — the deferred cleanups run before the IIFE returns. If the user assigns the assembled result to a longer-lived variable, the resources are dead by the time the variable is used. Same use-after-close pattern q.Open already detects via `escape.go`. Phase 3 wires q.Assemble's resource-recipe outputs into the same machinery so the escape diagnostic surfaces automatically. Users who genuinely want a longer-lived resource use the `//q:no-escape-check` opt-out.

### Phase 3 deliverables

1. `resolveAssemble`: detect `(T, func(), error)` recipe signature; mark step as resource-recipe (new field `IsResource bool` on `assembleStep`).
2. `buildAssembleBody`: emit `defer _qCleanup<N>()` after each successful resource-recipe call; ordering naturally respects topo via defer LIFO.
3. Wire q.Assemble resource outputs into `escape.go`'s detection.
4. Unit tests + e2e fixtures: resource recipe with cleanup; chained resources where teardown order matters; failure mid-chain triggers partial cleanup; escape detection catches return-of-resource-through-assemble.
5. Extend `docs/api/assemble.md` with the resource-recipe section.

## Phase 4 — parallel construction

ctx-attached opt-in for parallel topo-wave construction. Like `q.WithAssemblyDebug`, the option rides on the ctx; the rewriter detects it via `ctx.Value` at IIFE entry and switches between sequential (default) and wave-parallel emission.

```go
ctx := q.WithAssemblyPar(context.Background(), 4) // up to 4 concurrent recipes per wave
server := q.Unwrap(q.Assemble[*Server](ctx, newConfig, newDB, newCache, newAuth, newServer))
```

Use case: slow constructors (DB ping, remote config fetch, secret retrieval, schema validation) where total assembly time matters. Sequential default keeps deterministic startup order — which matters for logs / metrics / audit trails — and parallel is opt-in.

### Wave detection

Topo sort already produces an ordered slice. Group consecutive recipes whose deps are all already produced into the same "wave". Wave 0 = recipes with no deps (or only ctx). Wave N+1 = recipes whose deps are all produced by waves 0..N.

### Generated code

```go
(func() (*Server, error) {
    _qDbgPar := q.AssemblyParallelism(_qDepCtx) // returns n (0 = serial)
    var wg sync.WaitGroup
    var firstErrMu sync.Mutex
    var firstErr error
    setErr := func(e error) {
        firstErrMu.Lock(); defer firstErrMu.Unlock()
        if firstErr == nil { firstErr = e }
    }
    // wave 0
    _qDep0 := newConfig()  // serial — no deps
    // wave 1: newDB, newAuth (parallel up to _qDbgPar)
    var _qDep1 *DB; var _qDep2 *Auth
    sem := make(chan struct{}, _qDbgPar)
    wg.Add(2)
    sem <- struct{}{}
    go func() { defer wg.Done(); defer func() { <-sem }(); _qDep1 = newDB(_qDep0) }()
    sem <- struct{}{}
    go func() { defer wg.Done(); defer func() { <-sem }(); _qDep2 = newAuth(_qDep0) }()
    wg.Wait()
    if firstErr != nil { return nil, firstErr }
    // ... more waves
}())
```

When `_qDbgPar == 0` (no `WithAssemblyPar` on ctx), skip the goroutine machinery and emit the serial shape — keeping the no-config path unchanged.

### Phase 4 deliverables

1. `pkg/q/assemble.go`: `WithAssemblyPar(ctx, n) context.Context`, `AssemblyParallelism(ctx) int`.
2. `buildAssembleBody`: wave detection; conditional serial-vs-parallel emit.
3. Unit tests for wave detection (table-driven on synthetic step lists).
4. E2E fixtures: parallel happy path with a sleep-injecting recipe to confirm goroutine concurrency; parallel-with-error confirms the wait + first-err semantics.
5. Extend `docs/api/assemble.md` with the WithAssemblyPar section.

## Open questions / future considerations

- **Recipe groups via package-level slices.** `q.Assemble` accepts `recipes ...any`; spreading a `[]any{newConfig, newDB, newServer}` via `recipes...` works today. A tiny helper `q.RecipeSet(...) []any` is YAGNI — `[]any{...}` is fine.
- **Did-you-mean suggestions.** When the user forgets a recipe whose type is close to one supplied (e.g. `Config` vs `*Config`, or a typo'd type alias), the diagnostic could suggest the closest match. Stretch goal; phase 1's tree visualisation already grounds the user enough that typo-mistakes are usually obvious.
- **Per-recipe timeout.** A future ctx option `q.WithAssemblyRecipeTimeout(ctx, dur)` could wrap each recipe call with a `context.WithTimeout`. Only meaningful if recipes take ctx — otherwise the timeout has no enforcement point.
- **slog labels.** Each recipe label could become a slog attr in the trace output instead of a plain Fprintf line. Useful for structured-logs pipelines but adds an `slog` import. Defer until someone asks.
- **Performance.** Topo sort is O(N²) per call site. Real-world recipe sets are small (10s); revisit only if profiles show it.

## Recommended phase order

1. **Phase 2a (`q.AssembleAll`)** first — biggest user-visible win for plugin/handler systems.
2. **Phase 2b (struct-target)** — composes well with 2a.
3. **Phase 3 (resource lifetime)** — biggest impact for long-running services.
4. **Phase 4 (parallel)** — last because it's the largest emit-side change and benefits from the other phases' fixtures shaking out edge cases.

Each phase is independent; the order is a recommendation based on payoff vs implementation cost.

## Resume checklist for a cold-state implementer

1. Read `docs/api/assemble.md` end-to-end (the public surface).
2. Skim `internal/preprocessor/assemble.go` — main entry points are `resolveAssemble`, `buildAssembleReplacement`, `buildAssembleBody`.
3. Run `go test ./internal/preprocessor/ -run 'TestUnit'` — ~1s, all green. Read the table-driven tests to see how the resolver is exercised in-process.
4. Run `go test ./internal/preprocessor/ -run 'TestFixtures/assemble' -v` — ~30s with parallelism, 21 e2e fixtures pass.
5. Pick the next phase from "Recommended phase order"; read its section in this doc; implement against the unit harness first; then add e2e fixtures.
