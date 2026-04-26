# q.Assemble — Phase 4 plan (resume point)

This document is the resume-point for the parked Phase 4 work on `q.Assemble`. The current public surface (single-call DI, `q.AssembleAll`, `q.AssembleStruct`, full resource-lifetime management with `.Release()` / `.NoRelease()` chain, debug tracing, `q.PermitNil`) is documented in [`docs/api/assemble.md`](../api/assemble.md). The list below is what is *not* shipped.

A cold-state reader can pick up Phase 4 from this doc plus the references below.

## Where things live

- **API doc:** [`docs/api/assemble.md`](../api/assemble.md) — current public surface, full happy/sad-path coverage, every diagnostic shape with examples.
- **Stubs:** [`pkg/q/assemble.go`](../../pkg/q/assemble.go) — `Assemble[T]`, `AssembleAll[T]`, `AssembleStruct[T]`, `AssemblyResult[T]` chain (`Release`, `NoRelease`), `PermitNil`, `WithAssemblyDebug`, `WithAssemblyDebugWriter`, `AssemblyDebugWriter`, `LogCloseErr`. `q.Unwrap` and `q.UnwrapE` live in [`pkg/q/q.go`](../../pkg/q/q.go) (plain runtime; not rewritten).
- **Implementation:** [`internal/preprocessor/assemble.go`](../../internal/preprocessor/assemble.go) — `resolveAssemble` (typecheck), `buildAssembleReplacement` + `buildAssembleBody` (rewriter). Phase 4 hooks here.
- **Scanner:** [`internal/preprocessor/scanner.go`](../../internal/preprocessor/scanner.go) — `familyAssemble*`, `qSubCall.AssembleRecipes`, `qSubCall.AssemblePermitNil`, `qSubCall.AssembleCtxDepKey`.
- **Tests:**
  - **Unit tests:** [`internal/preprocessor/assemble_unit_test.go`](../../internal/preprocessor/assemble_unit_test.go) — sub-millisecond per case, table-driven against the resolver. **Add new diagnostic cases here first; e2e fixture follows.**
  - **E2E fixtures:** `internal/preprocessor/testdata/cases/assemble_*` — full toolexec build cycle, ~0.5s each.

## Phase 4 — parallel construction (parked)

ctx-attached opt-in for parallel topo-wave construction. Like `q.WithAssemblyDebug`, the option rides on the ctx; the rewriter detects it via `ctx.Value` at IIFE entry and switches between sequential (default) and wave-parallel emission.

```go
ctx := q.WithAssemblyPar(context.Background(), 4) // up to 4 concurrent recipes per wave
server := q.Unwrap(q.Assemble[*Server](ctx, newConfig, newDB, newCache, newAuth, newServer).Release())
```

Use case: slow constructors (DB ping, remote config fetch, secret retrieval, schema validation) where total assembly time matters. Sequential default keeps deterministic startup order — which matters for logs / metrics / audit trails — and parallel is opt-in.

**Why parked:** the sequential path is fast enough for current workloads and nobody has hit total-assembly-time as a measurable cost. Revisit if profiles show otherwise.

### Wave detection

Topo sort already produces an ordered slice. Group consecutive recipes whose deps are all already produced into the same "wave". Wave 0 = recipes with no deps (or only ctx). Wave N+1 = recipes whose deps are all produced by waves 0..N.

### Generated code (sketch)

```go
(func() (*Server, func(), error) {
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
    if firstErr != nil { return nil, func(){}, firstErr }
    // ... more waves
}())
```

When `_qDbgPar == 0` (no `WithAssemblyPar` on ctx), skip the goroutine machinery and emit the serial shape — keeping the no-config path unchanged. The cleanup-chain wiring already in `buildAssembleBody` needs to interleave with the wave emission so per-wave failures still trigger partial-cleanup rollback in reverse-topo order.

### Phase 4 deliverables

1. `pkg/q/assemble.go`: `WithAssemblyPar(ctx, n) context.Context`, `AssemblyParallelism(ctx) int`.
2. `buildAssembleBody`: wave detection; conditional serial-vs-parallel emit; ensure cleanup-chain rollback semantics still hold per-wave.
3. Unit tests for wave detection (table-driven on synthetic step lists).
4. E2E fixtures: parallel happy path with a sleep-injecting recipe to confirm goroutine concurrency; parallel-with-error confirms the wait + first-err semantics; parallel-with-resource confirms cleanup ordering across waves.
5. Extend `docs/api/assemble.md` with the WithAssemblyPar section.

## Open questions / future considerations

- **Recipe groups via package-level slices.** `q.Assemble` accepts `recipes ...any`; spreading a `[]any{newConfig, newDB, newServer}` via `recipes...` works today. A tiny helper `q.RecipeSet(...) []any` is YAGNI — `[]any{...}` is fine.
- **Did-you-mean suggestions.** When the user forgets a recipe whose type is close to one supplied (e.g. `Config` vs `*Config`, or a typo'd type alias), the diagnostic could suggest the closest match. Stretch goal; the existing tree visualisation already grounds the user enough that typo-mistakes are usually obvious.
- **Per-recipe timeout.** A future ctx option `q.WithAssemblyRecipeTimeout(ctx, dur)` could wrap each recipe call with a `context.WithTimeout`. Only meaningful if recipes take ctx — otherwise the timeout has no enforcement point.
- **slog labels.** Each recipe label could become a slog attr in the trace output instead of a plain Fprintf line. Useful for structured-logs pipelines but adds an `slog` import. Defer until someone asks.
- **Performance.** Topo sort is O(N²) per call site. Real-world recipe sets are small (10s); revisit only if profiles show it.
- **Ctx-attached assembly cache.** TODO #87 — separate from Phase 4 but in the same vicinity. Cache built deps across multiple `q.Assemble` calls in the same ctx scope. Design notes in [`docs/planning/TODO.md`](TODO.md).

## Resume checklist for a cold-state implementer

1. Read `docs/api/assemble.md` end-to-end (the current public surface).
2. Skim `internal/preprocessor/assemble.go` — main entry points are `resolveAssemble`, `buildAssembleReplacement`, `buildAssembleBody`.
3. Run `go test ./internal/preprocessor/ -run 'TestUnit'` — all green in ~1-2s. Read the table-driven tests to see how the resolver is exercised in-process.
4. Run `go test ./internal/preprocessor/ -run 'TestFixtures/assemble' -v` — full toolexec build cycle for every e2e fixture.
5. Implement Phase 4 against the unit harness first; then add e2e fixtures.
