# TODO

The persistent backlog for `q`. A cold-state reader can pick up here without re-deriving priorities. For what's already shipped, read the code under `pkg/q/` + `internal/preprocessor/` and the API docs under `docs/api/`. For *why* something shipped or got reverted, read `git log`.

**Standing rule (design).** q only accepts syntax Go itself accepts — see `docs/design.md` §7. Reject any feature that would light up as a type/syntax error in gopls. This kills some nice-reading shapes (auto-inferred `, nil` tails, auto-injected trailing `return nil`, omitted return values) but it's non-negotiable: the IDE experience is the whole reason we're a toolexec rewriter instead of a custom parser.

**E-variant convention.** Every bubble-shaped feature gets both a bare form and an `…E` chain form that exposes the standard vocabulary — `.Err(error)`, `.ErrF(fn)`, `.Wrap(msg)`, `.Wrapf(format, args…)`, `.Catch(…)` — matching what Try/NotNil/Check/Open already expose. Features that do not bubble (compile-time prints, panics, defer sugar) explicitly have no E-variant.

## Open

### Rejected-Go-proposal expansion

- **#74 — Sum types via `q.OneOf` / `q.Switch`.** Discriminated unions, the most-rejected of all rejected proposals.

  **Surface:**
  - `type Result q.OneOf2[Success, Failure]` — alias to a real generic struct that holds an `any` value + a small tag int
  - `q.MakeOneOf[Success, Result]("Success", success)` constructor (or per-arm sugar like `q.As1[Success, Result](v)`)
  - `q.Switch[R, U any](u U, arm1, arm2…) R` — exhaustive type-tagged dispatch; rewriter checks the union has exactly N arms and N cases passed

  **Go-validity:** all generic calls / generic type aliases. `q.OneOf2` is a real type with stub methods; the value lives at runtime as `any` + tag.

  **Tradeoff:** runtime cost is one `any` box (interface conversion). To avoid it for primitive variants, the rewriter could specialise `q.OneOf2[int, string]` to a struct-with-discriminator at compile time. Big lift; defer the optimisation.

  **Why now-ish vs much later:** sum types are the headline rejected proposal. Even a runtime-boxed version with exhaustiveness checking would be high-impact for users.

- **#78 — Embed `rewire` and `proven` into q's toolexec dispatcher.** Right now a project that wants q + proven + rewire has to run them as a chain or pick one. q's `cmd/q` could become an umbrella dispatcher that detects which patterns are present in each compiled package and routes to the appropriate rewriter pass.

  **Surface:**
  - Single `-toolexec=q` enables Try/NotNil/etc. (q), `proven.True(…)` assertions (proven), and `rewire.Mock(…)` / `rewire.Stub(…)` mocks (rewire).
  - Each child preprocessor's pass runs on per-package compiles where its patterns are detected.
  - q's existing pkg/q stub injection + runtime injection stays. Proven and rewire likely have similar package-dispatch patterns we can compose with.

  **Mechanism options:**
  1. **Vendor** the rewire / proven repos into `internal/preprocessors/{rewire,proven}/`. Pin to a specific commit, copy on update. Pro: hermetic, no cross-repo coordination at build time. Con: drift; we miss upstream fixes until we re-vendor.
  2. **Import as Go modules.** Easiest if their internals are exported in a stable API. Likely they're not — `internal/preprocessor/...` packages aren't reachable from outside their modules. Would need upstream cooperation (move the pass entry point to a non-internal package, or add a thin API).
  3. **Shell out to their binaries.** q's process dispatches to `proven` and `rewire` binaries on PATH. Minimal coupling but requires them installed. Worst option.

  **Lean:** option 1 (vendor) for rewire and proven both. We own all three repos, so vendoring is a coordination cost we can absorb cheaply. Pin via go.mod replace directives during dev, switch to copies on stable release.

  **Order of work:**
  1. Audit rewire and proven for the entry-point shape (pass `(toolArgs, sources) → (newArgs, diagnostics)`).
  2. If rewire/proven's pass is roughly the `Plan/Diagnostic` shape q already uses, write an adapter; otherwise propose API changes upstream first.
  3. Vendor + add a dispatcher in `compile.go` that runs all three passes per user-package compile (q first, then proven assertions, then rewire mocks — order matters since later passes see earlier passes' rewrites).
  4. New e2e fixture combining all three: a function that uses `q.Try`, `proven.True`, and `rewire.Mock` in the same body.

  **Open question:** does running multiple toolexec passes on the same compile produce correct results, or does pass N+1 trip on temp paths from pass N? Likely fine since each pass writes its own tempdir and the final argv just lists them all, but worth a smoke test before going deeper.

### Resource lifetime + dependency injection

- **#83 ARC for non-RAM resources (long-term).** "Last-usage-site closes" is what Rust gets through linear types and what Swift / Objective-C get through reference counting. q's seed is already half-built: `q.Open` is the resource constructor, `.Release` is the destructor, the rewriter knows where every resource binding flows, and the resource-escape detection pass identifies when a binding outlives its function.

  The ambitious version: track every reference site of an Opened resource (including escapes) and, when escape is OK (e.g. handed to a goroutine that joins), insert refcount inc/dec at each ownership-transfer point so the resource closes at the *actual* last usage rather than the function boundary. Concretely:

  - Wrap each Open value in a generated `qRC[T]{value T; rc *atomic.Int32; cleanup func(T)}` shim.
  - At each transfer site (return, channel send, field store, goroutine spawn), emit `rc.Add(1)`. At each scope exit, emit `if rc.Add(-1) == 0 { cleanup(value) }`.
  - The shim is invisible to user code — q.Open's existing surface (`Release`, `NoRelease`) stays — but the deferred cleanup becomes "decrement, free if last" instead of unconditional close.

  Big lift: needs flow analysis (or a heavy hand: rewrite every reference into shim-method calls), needs to interop with raw resource access (sometimes you do want the underlying `*Conn`), and adds a real per-call atomic. Probably not worth it for the 90% case (functions that own their own resources) — defer until profiles or user reports show resource ownership crosses function boundaries often enough that the existing diagnostics become annoying.

- **#84 — `q.Assemble` follow-on phases.** Phase 1 (single-entry auto-derived DI), Phase 2a (`q.AssembleAll[T]` for multi-provider aggregation), and Phase 2b (`q.AssembleStruct[T]` for field-decomposition multi-output) have shipped. See [`docs/api/assemble.md`](../api/assemble.md). Remaining work, full plan in [`docs/planning/assemble.md`](assemble.md):
    - **Phase 3 — resource lifetime.** AssemblyResult[T] chain with `.Release()` (defer-injected cleanup) and `.NoRelease()` (manual shutdown closure). Explicit `(T, func(), error)` recipes shipped. Still TODO: **auto-detect Close()-able recipes from T's type** — recipes whose T has `Close()` / `Close() error` / is a channel get a synthesised cleanup. **Important: only function recipes get auto-cleanup; inline values are user-owned and pass through unchanged.** The `step.IsValue` flag already distinguishes them, so the gate is `!step.IsValue && hasCloseShape(step.Output)`.
    - **Phase 4 — parallel construction.** `q.WithAssemblyPar(ctx, n)` rides on the ctx like `q.WithAssemblyDebug`; rewriter emits topo waves with `sync.WaitGroup` per wave.

- **#90 — Recipe-with-cleanup adapter for external types.** When a recipe constructor comes from an external library (no Close() method, no channel) and the user can't change its signature, the natural pattern is to write a 3-line inline wrapper:

  ```go
  openForeign := func(c *Config) (*foreignlib.Resource, func(), error) {
      r, err := foreignlib.Open(c)
      if err != nil { return nil, nil, err }
      return r, func() { foreignlib.Cleanup(r) }, nil
  }
  ```

  A q-provided helper could shorten this. Surface options:

  - **Generic adapter.** `q.Cleanup[T any](ctor func() (T, error), cleanup func(T)) func() (T, func(), error)` — only works for zero-arg ctors. Useful for the common case but doesn't generalise to ctors with input deps.
  - **Per-arity adapters.** `q.Cleanup1[A, T](ctor func(A) (T, error), cleanup func(T)) func(A) (T, func(), error)`, `q.Cleanup2`, etc. Generalises but bloats the surface.
  - **Ctx-attached registry.** `q.WithCleanup[T](ctx, cleanup func(T))` — at q.Assemble time, the rewriter checks ctx for a registered cleanup for T's type and synthesises one. Cleaner but adds ctx complexity and global-ish state.

  Park until users actually hit the inline-wrapper pattern often enough that one of these earns its keep.

- **#87 — Ctx-attached assembly cache.** `q.WithAssemblyCache(ctx)` (name TBD) attaches a `*sync.Map` keyed by typeKey; `q.Assemble` / `q.AssembleAll` consult it before building each dep. Two consecutive calls in the same ctx scope reuse `*Config`, `*DB`, etc. — useful in long-running services where the same dep set is rebuilt for each request handler.

  **Open design questions.**

  - **Lookup key.** typeKey alone (simplest, but two recipes producing the same type silently share) is fine for most cases since the branded-variant pattern (`type PrimaryDB struct{ *DB }`) already gives each variant its own typeKey. If finer-grained scoping is needed later, the cache could key on `(typeKey, callSite)` to localise sharing.
  - **Phase 3 collision.** A cached `*DB` carries a `func()` cleanup. Cleanup must fire ONCE, tied to ctx cancellation — not per-Assemble-call. Either the cache owns the cleanup (registered via `context.AfterFunc(ctx, cleanup)`) or resource recipes are excluded from caching and rebuilt every time.
  - **Errors.** Re-attempt on each call, or cache the error? Default: no cache on error so transient failures don't get pinned.
  - **Recipe-identity divergence.** If two `q.Assemble` call sites use different recipe functions but request the same `*Config` type, the second call gets the first's `*Config`. Spec it as "ctx is the cache scope; you own its membership" rather than trying to be clever.

  **Mechanism sketch.** Rewriter detects `q.AssemblyCache(ctx) != nil` at IIFE entry (one ctx.Value lookup, like the debug-trace prelude). When non-nil, each step emits `if v, ok := _qCache.Load(typeKey); ok { _qDep<N> = v.(T) } else { _qDep<N> = recipe(...); _qCache.Store(typeKey, _qDep<N>) }`. When nil, sequential serial emit unchanged.

  **Partial-failure semantics — write-at-end pattern.** The cache is only written AT THE END of a successful assembly, never mid-flight. Two consequences fall out:

  1. **No failure-path cache invalidation.** If assembly fails mid-chain, the cache is unchanged — there's nothing to roll back. Local cleanups still fire (in reverse-topo order, unconditional, built into the chain rewriter). Pre-existing cache entries from earlier successful assemblies remain valid.

  2. **No mid-flight race window.** A failed assembly can't pollute the cache with half-built deps that another concurrent assembly could grab.

  Mechanism: rewriter emits per-step `if cached, ok := _qCache.Load(key); ok { use cached } else { build fresh }`. The "build fresh" path does NOT Store. After the IIFE's final success path, ONE pass `_qCache.Store(...)` for each freshly-built dep. Failure paths skip the Store pass entirely.

  **Concurrency caveat (for the docs, not a hard problem).** Two assemblies running concurrently in the same cache scope may both build the same dep type before either reaches the success-path Store; whichever Stores first wins, the other's instance is orphaned (not cleaned up — it was never registered in any local cleanup chain that survived to fire). For the strict singleflight semantics, layer a sync.Mutex per typeKey or use golang.org/x/sync/singleflight; defer that until users actually hit the orphaning.

### Coroutines

- **#85 tier 3 — Stackless coroutines (preprocessor-rewritten state machines).** The preprocessor analyses a function containing `q.Yield(v)` calls, identifies yield points as state-machine transitions, and rewrites the entire function into a state-machine struct with a `Resume(input) (output, done)` method. No goroutine. No channel. Just a struct holding the saved local variables and a `state int` field.

  Pros: zero goroutine overhead; faster than tier 2 (`q.Coro`) for tight loops (no channel send/receive on each yield).

  Cons: this is THE hard problem. Closures over local variables need to lift to struct fields. Defer / recover semantics get weird (where does a deferred call go in a state machine?). Loops that span yield points need careful state tracking. C#'s async-rewriter took years to get right, and Go's syntax is more permissive (defer, goroutine spawning, panic/recover all interact with control flow).

  Realistic scope: probably too big for q. Tier 1 (`q.Generator`) and tier 2 (`q.Coro`) cover most of the ergonomic win. Park unless a specific tight-loop workload justifies the lift.

### Eventing

- **#89 — Qt-style signals and slots.** Decoupled callback propagation: a `Signal[T]` holds a list of subscribers (slots), `signal.Connect(slot)` registers, `signal.Emit(v)` fans out. Plain runtime helper, no preprocessor magic — just generic types over `func(T)` callbacks with concurrency-safe Connect/Disconnect/Emit.

  **Surface (sketch):**

  ```go
  type Signal[T any] struct { /* internal */ }
  func NewSignal[T any]() *Signal[T]

  func (s *Signal[T]) Connect(slot func(T)) (disconnect func())
  func (s *Signal[T]) Emit(v T)              // synchronous fan-out
  func (s *Signal[T]) EmitAsync(ctx, v T)    // each slot in its own goroutine; ctx for cancellation

  // Fan-in: one slot listens to multiple signals.
  func ConnectAll[T any](slot func(T), signals ...*Signal[T]) (disconnect func())
  ```

  **Why over plain channels:** channels couple producers and consumers via a known buffer + close discipline. Signals/slots decouple — each subscriber is a callback function; connecting/disconnecting is dynamic; no consumer-discovery boilerplate. Useful for UI-style event propagation, cross-module notifications inside a single binary, observability hooks.

  **Open design questions.**
  - **Sync vs async Emit.** Default to sync (each slot called inline on Emit's goroutine) — predictable latency, no goroutine sprawl. EmitAsync as opt-in for slow slots.
  - **Slot panic policy.** A panicking slot shouldn't kill the emitter or stop other slots. Wrap each slot call in `defer recover()`; expose a `WithPanicHandler` hook on the signal for user-controlled handling.
  - **Disconnect during Emit.** A slot that disconnects itself (or another) mid-Emit must not corrupt the iteration. Snapshot the slot list at Emit start.
  - **Once-style slots.** `signal.ConnectOnce(slot)` for fire-and-disconnect; useful for "wait for first event" idioms.
  - **Fan-in shape.** `ConnectAll` is one option; alternatively a `Merge[T](sigs...)` that returns a single `*Signal[T]` re-emitting any input. The latter composes better with downstream slots.
  - **Typed payload constraints.** Generic on T; no runtime type assertion. For heterogeneous payloads users wrap in a struct or use `any`.

  Inspiration: Qt's signal/slot, observable patterns from RxJava / RxJS, but simpler — no operator zoo, just connect/emit/disconnect.

### Future / parking lot

- **#11 — `q.<X>` for is-nil-as-failure / comma-ok / etc.** Catch-all for any additional bubble triggers that surface later (e.g. `q.IfNil(x)` for error-less nil checks that don't want to spell `q.NotNilE(…).Err(ErrSomething)`). Existing bubble triggers cover the obvious cases; this is the umbrella for whatever turns up next.

- **#16 — Multi-LHS from a single q.\***. `v, w := q.Try(call())` where we'd want q.Try to split a multi-result producer. Requires new runtime helpers `q.Try2[T1, T2]` / `q.Try3` and matching rewrite templates. The hoist infrastructure already handles *incidental* multi-LHS (where the RHS call itself returns multi, and a q.* is nested in its args). This entry covers the shape where q.* IS the multi-result producer.

- **#67 — Goroutine-local storage with auto-cleanup on goroutine death.** `q.GoroutineLocalStorage() map[any]any` keyed by `q.GoroutineID()`, with entries removed automatically when the owning goroutine exits. Motivating use case: Java-`ThreadLocal`-style "set once, fire-and-forget" storage that doesn't leak across long-running programs that have spawned and reaped many transient goroutines.

  **Mechanism.** Two pieces injected into the stdlib `runtime` compile by q's preprocessor:

  1. A companion file declaring `qGLSMap map[uint64]map[any]any` plus `qGetOrCreateGLS()` (linkname-exposed for pkg/q to pull) and `qDeleteGLS(id uint64)` (package-internal call site).
  2. A one-line AST patch at the top of `runtime.goexit0`: `qDeleteGLS(uint64(gp.goid))`. The patcher walks `proc.go` with `go/parser`, finds `goexit0` by name, reads the parameter name from the AST (so we don't hardcode `gp`), prepends an `ast.ExprStmt`, and prints back through `go/printer`. No textual line manipulation.

  pkg/q exposes `q.GoroutineLocalStorage() map[any]any` (lazy on first call, returns the live map so the caller can mutate directly) and a diagnostic `q.GLSEntryCount() int`.

  **Open design questions.**

  - **Concurrency primitive.** `runtime` cannot import `sync` (sync depends on runtime). Two paths:
    - *Use `runtime.mutex`* (the runtime-internal mutex used everywhere in the scheduler). Simple, all state lives in runtime, briefly locks on every Get/Delete. Each goroutine reads/writes its own key so there's no per-key contention; the lock is purely for map structure integrity. Microseconds we won't measure.
    - *Move storage to pkg/q, link from runtime.* Real `sync.Map` in pkg/q; runtime's injected file has bodyless declarations linkname-pulling pkg/q's bodies. The patched `goexit0` calls `qDeleteGLS` which the linker resolves to pkg/q's body. Adds three linkname directives, an extra file boundary, and exercises runtime → third-party linkname pulls (less tested than the third-party → runtime path we already use for `GoroutineID`). For the access pattern (each goroutine reads/writes its own key, infrequently), sync.Map's lock-free-read advantage doesn't materialise.
    - *Lean:* `runtime.mutex`. The complexity of routing through pkg/q buys nothing observable.

  - **Inheritance to child goroutines.** Currently each goroutine gets its own map (no inheritance from the parent that spawned it). Java's ThreadLocal works similarly. Alternative: copy parent's map into child's on first child access, like rewire's pprof-labels-pointer trick where labels propagate automatically. Adds complexity (where to hook the copy?) and surprise (mutations from child don't reflect to parent). Lean: no inheritance — explicit propagation via `context.Context` is what we already have.

  - **Type safety.** `map[any]any` is type-untyped by design — q doesn't introspect what users want to store. A future `q.GoroutineLocal[T]` typed wrapper layered on top is cheap if needed.

  - **Patching runtime is a real escalation.** Currently we're *appending* one file to runtime's compile. With GLS we'd also be *modifying* an existing runtime file (`proc.go`). That moves us from "runtime sees one extra file" to "runtime sees one of its own files rewritten". The AST-based patch is robust to comment / whitespace changes but breaks if Go renames `goexit0` (rare; it's load-bearing for the scheduler) or restructures it enough that "prepend at body top" is no longer a safe insertion point.

  - **Cross-Go-version stability.** Goexit0's signature has been `func goexit0(gp *g)` for years, but a bigger rework (e.g., merge with `gdestroy`) would invalidate the parameter-extraction path. Mitigation: when the patcher fails to find `goexit0`, fall back to omitting the cleanup hook — the GLS still works, just leaks. Behaviour-degraded rather than build-broken.

  - **Test for the cleanup invariant.** A fixture that spawns N goroutines that each touch GLS, joins them, then polls `GLSEntryCount()` until it returns to baseline. Relies on `runtime.Gosched()` + brief sleep to give `goexit0` a chance to run for every dead goroutine.

- **#86 — Zig-style native binary embedding for `q.AtCompileTime` results.** Today's q.AtCompileTime ships values from preprocessor-time to runtime via Codec encode/decode round-trips (default JSON). It works for arbitrary types but pays a runtime decode cost on every program startup, plus a one-time encode at preprocessor time. Zig sidesteps this entirely: comptime values become target-native bytes embedded directly in `.rodata` / `.data` sections, with linker relocations patching pointers. No serialization, no decode — the bytes are simply the type's natural in-memory representation.

  **Zig pipeline (reference for the Go translation):**

  1. **Sema / InternPool** — comptime values live in the compiler's interned tagged-union value representation. Identity preserved (same comptime allocation = same symbol).
  2. **Lowering** — when a comptime value escapes to runtime, codegen walks it transitively and emits raw bytes per the target ABI memory layout: integer endianness, struct field offsets, slice = `{ptr, len}` pair, etc.
  3. **Pointers become relocations** — pointer fields emit zero bytes plus a relocation entry; the linker patches addresses. Cycles handled because revisited values already have an assigned symbol.
  4. **`@embedFile` is the trivial case** — file bytes stuffed verbatim into a `[N]u8` symbol in `.rodata`.

  **Limitations Zig enforces:** no comptime pointers into runtime memory; no real heap allocator at comptime; `@ptrFromInt` of arbitrary integers can survive to runtime as opaque values but not be dereferenced at comptime; pointers to comptime-only types (`type`, `comptime_int`) can't escape; mutable comptime-pointer chains land in `.data` instead of `.rodata`.

  **What this would look like in q:** instead of a runtime `init()` that decodes via Codec, the rewriter emits a `[N]byte` literal whose contents are the target-native struct bytes, plus an `unsafe.Slice` / `unsafe.Pointer` cast at the use site. For pointer-bearing types, separate symbols + Go's symbol-relocation machinery (which the gc compiler does support, used by `//go:linkname` and embedded data) chain them together.

  **Why parked, not in active development:**

  1. **Go has no public ABI guarantees.** Struct field offsets, padding, and even alignment depend on compiler version. JSON / Gob round-tripping is layout-agnostic; bytes-are-bytes embedding requires us to track gc's layout for the target arch + version. A Go upgrade could silently corrupt embedded values.
  2. **Cross-arch builds get awkward.** A linux/amd64 build host preprocessing for a darwin/arm64 target would need to know the *target's* layout rules at preprocess time — not the host's. Doable (encoding/binary plus careful padding emulation) but non-trivial.
  3. **The 90% case is satisfied by the codec route.** Decode cost on startup for a few kilobytes of static config is microseconds. The bytes-native path matters mostly for very large embedded tables (CRC tables, sin tables, parser state machines) where every kilobyte of binary size + every microsecond of init() time count.
  4. **Pointer-relocation machinery is invasive.** Emitting a `[N]byte` literal is one thing; emitting cross-symbol pointer relocations from the preprocessor would need either a custom object-file pass (huge lift) or an `init()` that fixes pointers at startup using `unsafe.Add` / `unsafe.Pointer` arithmetic on a known base — at which point we're back to runtime work.

  **Realistic scope if pursued:** start with fixed-layout primitive arrays (`[N]int`, `[N]float64`, `[N]byte`) where Go's layout is well-defined. Emit a `var _qCt0_value = [...]int{1, 2, 3, ...}` Go literal at file scope. The Go compiler's existing constant-folding optimisations turn this into `.rodata` placement automatically — no preprocessor relocation work needed. That covers the "embed a 64KB CRC table" use case at zero runtime cost. Defer the pointer-relocation tier indefinitely; the codec route is good enough for everything else.

  **When this matters:** if a real workload shows q.AtCompileTime decode time as a measurable startup-cost line item OR if the encoded JSON/Gob blob bloats the binary noticeably. Until then, the codec route earns its keep.

### Considered and dropped

Don't re-propose these without new information — each was ruled out for a specific reason that's still live.

- **q.Default / q.DefaultE** — the 2-arg form `q.Default(call(), -1)` isn't valid Go (the `f(g())` multi-return spread rule requires `g()` to be the *sole* arg). The 3-arg pre-destructured form is redundant with `q.TryE(call).Catch(func(error) (T, error) { return fallback, nil })`.
- **q.Go** — too opinionated about panic logging + recovery policy. Plain `go fn()` plus a 4-line wrapper in the caller's own module gives full control.
- **q.TryCatch** (block-scoped try/catch) — `.Catch(handler func(any))` has no return path, so caught panics can't flow into the enclosing function's error return. `q.Recover` / `q.RecoverE` already cover the useful function-boundary case.
- **q.Must / q.MustE** — original rationale was "panicking is the opposite of what q exists to enable." Reconsidered when q.Assemble (always (T, error)) needed an escape hatch in main / init / tests where q.Try can't bubble; shipped as **q.Unwrap** / **q.UnwrapE** instead, with the explicit framing that they're for non-bubble call sites only and not a general-purpose error policy.
- **q.Call / q.Named (named arguments)** — verbose at the call site (noisier than positional + comment), rewriter requires `*types.Signature` to expose param names which fails for any callee with unnamed params, and either "missing names → zero value" semantics turn signature additions into silent bugs OR strict-coverage variants make the surface even longer than struct-options-pattern Go already has. Doesn't earn its keep.

## How this list is maintained

- New tasks: added here in the same commit that creates the in-session task.
- Closed tasks: removed from the open list in the same commit that ships the work. The shipped behaviour belongs in code, tests, and `docs/api/`; what shipped + when + why lives in `git log`.
- Renames / reshapes of an open task: edit the entry in place and note the change in the commit message.
