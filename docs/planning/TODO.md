# TODO

The persistent backlog for `q`. A cold-state reader can pick up here without re-deriving priorities. For what's already shipped, read the code under `pkg/q/` + `internal/preprocessor/` and the API docs under `docs/api/`. For *why* something shipped or got reverted, read `git log`.

**Standing rule (design).** q only accepts syntax Go itself accepts — see `docs/design.md` §7. Reject any feature that would light up as a type/syntax error in gopls. This kills some nice-reading shapes (auto-inferred `, nil` tails, auto-injected trailing `return nil`, omitted return values) but it's non-negotiable: the IDE experience is the whole reason we're a toolexec rewriter instead of a custom parser.

**E-variant convention.** Every bubble-shaped feature gets both a bare form and an `…E` chain form that exposes the standard vocabulary — `.Err(error)`, `.ErrF(fn)`, `.Wrap(msg)`, `.Wrapf(format, args…)`, `.Catch(…)` — matching what Try/NotNil/Check/Open already expose. Features that do not bubble (compile-time prints, panics, defer sugar) explicitly have no E-variant.

## Open

### Errors / observability

- **#98 — Application-wide stack traces on errors.** Go errors don't
  carry a stack by default. Libraries like `pkg/errors`
  (`errors.WithStack`) and `cockroachdb/errors` plug the gap, but
  they require per-call-site opt-in (`errors.WithStack(err)`) which
  drifts: every new `errors.New` / `fmt.Errorf` site is a chance to
  forget. q's preprocessor already sees every error-creation site
  in the user package; it can attach a stack at compile time, with
  zero ergonomic cost to the call site, so structured logs (slog
  attrs, observability sinks) get a real stack for free.

  **Surface options to compare:**

  - **Auto-wrap**: the preprocessor finds every `errors.New(...)`,
    `fmt.Errorf(...)`, and bare `return nil, err` (where the err
    didn't come from a wrapped call) and wraps the produced error
    in a stack-carrying shim. Zero call-site change. Off by default;
    enabled per-package via `var _ = q.WithStackTraces()` or a
    build-tag.
  - **q.* bubble interception**: stack capture happens inside the
    rewriter's existing `if err != nil { return zero, err }` block
    that q.Try / q.Check / q.NotNilE / etc. already emit. Zero new
    surface — every q-using codebase gets stack traces for free,
    non-q errors stay untouched. Smaller blast radius than auto-
    wrapping every `errors.New`.
  - **Explicit q.Err helper**: `q.Err("msg")`, `q.Errf("...", args)`
    — q-flavoured `errors.New` / `fmt.Errorf` that capture stack at
    construction. Opt-in per call site; loud about the choice; same
    drift problem as `errors.WithStack`.

  **Lean:** start with q.* bubble interception (smallest surface,
  highest hit rate for code already using q), then evaluate auto-
  wrap on top if real codebases want it. The explicit helper can
  layer alongside as a non-q option.

  **Hard invariant: capture-once.** A stack must be attached to an
  error AT MOST ONE TIME — at the original creation / first-bubble
  site. Subsequent wrap layers (a deeper q.Try bubbling an already-
  stacked err, an `fmt.Errorf("ctx: %w", err)`, an
  `errors.Join(...)`) MUST detect the existing stack via
  `errors.As(err, *qStackErr{})` and NOT attach a fresh one. Two
  motivations: (a) repeated capture across N wrap layers makes the
  attached trace useless — only the innermost is the real call site,
  the outer ones are just where the bubble passed through; (b)
  stack capture is the dominant per-error cost, so doing it N times
  per bubble chain regresses error-loop hot paths badly. The shim's
  constructor must be the single point that decides "attach or
  pass-through".

  **slog integration:** a custom `slog.Handler` (or a thin wrapper
  on top of `q.SlogContextHandler`) inspects every error-typed attr,
  pulls the stack via `errors.As(err, *qStackErr{})`, and emits it
  as a structured `stack` group (frames as `{file, line, fn}`
  records, JSON-friendly). Pair with `q.InstallSlogJSON` so a
  vanilla `slog.Error("processing", "err", err)` call ships a
  full stack to the log sink with no per-site work.

  **Open design questions before picking up.**

  - **Stack capture cost.** `runtime.Callers` is fast but allocates
    a `[]uintptr`. Most error paths are rare, but for
    high-throughput error-loops (parser fast-fail, validator hot
    paths) the alloc adds up. Consider a `runtime.Callers`
    fixed-size buffer (typical depth is < 32) and lazy
    symbolication.
  - **Compatibility with existing wrapping.** If the user already
    wraps via `fmt.Errorf("ctx: %w", err)`, the q-injected stack
    needs to flow through `Unwrap` so `errors.Is` / `errors.As`
    keep working. The shim must implement `Unwrap() error`.
  - **Foreign stack-carrying errors.** Capture-once handles q's own
    shim trivially via `errors.As(*qStackErr)`, but third-party
    stack-carriers (pkg/errors, cockroachdb/errors, go-errors) are a
    grey zone. Decide whether to (a) detect them via interface probe
    (`interface{ StackTrace() []runtime.Frame }` etc., enumerate the
    common shapes), (b) always defer to upstream when ANY foreign
    stack-carrier is present, or (c) attach our own anyway and let
    the slog handler de-dup at log time. (a) is most accurate, (c)
    is simplest.
  - **Goroutine boundaries.** Stack captured at error creation,
    naturally — but a stack at the *return* site (where q.Try fires)
    might be more useful for tracing the bubble chain. Decide
    whether to capture once-at-source or per-bubble (cheap per-
    bubble append: just one `runtime.Caller(0)` frame).
  - **Off-by-default vs on-by-default.** Stack on every error is
    invasive — even a small program produces megabytes of stack
    text in tight error loops. Off by default; opt-in via a
    package-level directive that the rewriter detects.

### Rejected-Go-proposal expansion

- **#74 follow-up — `q.SealedN` alias-style spelling on top of
  `q.Sealed`.** The variadic-value-args form
  `var _ = q.Sealed[Message](Ping{}, Pong{}, Disconnect{})` shipped;
  see [`docs/api/sealed.md`](../api/sealed.md). The alias-style
  spelling `type Message q.Sealed3[Ping, Pong, Disconnect]` would let
  users avoid the separate marker-interface declaration entirely —
  the preprocessor would synthesise BOTH the marker interface
  (`type Message interface { _q_marker_Message() }`) AND the per-
  variant marker impls. Smaller surface at the call site; more invasive
  preprocessor work (synthesising the interface itself, not just
  methods on existing types). Park until users ask for it — the
  variadic form already eliminates the per-variant boilerplate, so
  the gap is one declaration line.

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

- **#91 follow-up — additional `q.At` bubble terminals.** v1 ships `.OrError(err)` and `.OrE(call())` for the bubble shapes. Sibling vocabularies from the q.NotNilE chain are still pending: `.OrErrF(fn func() error)`, `.OrWrap(msg)`, `.OrWrapf(format, args…)`, `.OrCatch(fn func() (T, error))`. Same machinery as `.OrError` — just different ways of shaping the bubbled error. Park until users actually want them.

### Cleanup-shape uniformity across q

- **#99 — Cleanup-shape uniformity across q.** `q.Open(...).DeferCleanup(cleanup)` accepts `func(T)` and `func(T) error`, with the err-returning form rewritten to a `slog.Error`-wrapped defer. Other cleanup-accepting sites in q should accept the same two-shape vocabulary so users don't have to remember which API takes which shape:

  - `q.Assemble[T](...).DeferCleanup(cleanup)` — currently zero-arg only (auto-cleanup); add explicit-cleanup support with the same two shapes.
  - `q.NewScope().DeferCleanup(cleanup)` — same.
  - Recipe-cleanup hooks in q.Assemble's recipe shape (`(T, func(), error)`) — the `func()` slot could be either `func()` or `func() error` from the recipe author's perspective; the rewriter would normalise.

  Mechanism: extract `validateExplicitCleanup` + the `func(T) error` slog-wrap defer-line emission (landed for q.Open) into a reusable helper and call it from each cleanup-accepting site. Each site needs a parameter-shape switch on `...any` plus the corresponding `slog`-import wiring; the log message can be parameterised on the source ("q.Open / q.Assemble / q.Scope") so it names the offending site.

  Out of scope: no-arg cleanups (`func()` / `func() error`) and arbitrary call expressions. q.Open's DeferCleanup is intentionally scoped to the resource it wraps — write `defer myCleanup()` at the call site if the cleanup doesn't need the resource.

## Doc-coverage progress

Progress through `docs/api/<page>.md` ↔ `example/<page>/` 1:1 coverage. Each page ships in its own commit with the example mate + `expected_run.txt` + (when relevant) impl fixes that the doc-mirroring exposes. Tracked here so a cold-state reader can resume.

### Done

- try.md
- check.md
- notnil.md
- open.md
- as.md
- assemble.md
- async.md
- at.md
- atcompiletime.md
- atom.md
- await_ctx.md
- await_multi.md
- channel_multi.md
- checkctx.md
- convert.md
- coro.md
- data.md
- debug.md
- either.md
- enums.md
- exhaustive.md
- fnparams.md
- format.md
- gen.md
- generator.md
- goroutine_id.md
- lazy.md
- lock.md
- match.md
- ok.md
- oneof.md
- par.md
- recover.md
- recv.md
- recv_ctx.md
- reflection.md
- require.md
- scope.md
- sealed.md (marked experimental — IDE squiggles on pre-rewrite source; build-tag gated)
- slog.md
- sql.md
- string_case.md
- tern.md
- timeout.md
- todo.md
- trace.md

### Todo (api pages)
(none — all docs/api/*.md pages have an example/<page>/ mate)

### Todo (top-level docs)

(none — getting-started.md's Quick Start has an example/getting_started/ mate; design.md / index.md / typed-nil-guard.md are reference prose whose code snippets are covered by the per-API examples they point to)

### Resource lifetime + dependency injection

- **#83 ARC for non-RAM resources (long-term).** "Last-usage-site closes" is what Rust gets through linear types and what Swift / Objective-C get through reference counting. q's seed is already half-built: `q.Open` is the resource constructor, `.DeferCleanup` is the destructor, the rewriter knows where every resource binding flows, and the resource-escape detection pass identifies when a binding outlives its function.

  The ambitious version: track every reference site of an Opened resource (including escapes) and, when escape is OK (e.g. handed to a goroutine that joins), insert refcount inc/dec at each ownership-transfer point so the resource closes at the *actual* last usage rather than the function boundary. Concretely:

  - Wrap each Open value in a generated `qRC[T]{value T; rc *atomic.Int32; cleanup func(T)}` shim.
  - At each transfer site (return, channel send, field store, goroutine spawn), emit `rc.Add(1)`. At each scope exit, emit `if rc.Add(-1) == 0 { cleanup(value) }`.
  - The shim is invisible to user code — q.Open's existing surface (`DeferCleanup`, `NoDeferCleanup`) stays — but the deferred cleanup becomes "decrement, free if last" instead of unconditional close.

  Big lift: needs flow analysis (or a heavy hand: rewrite every reference into shim-method calls), needs to interop with raw resource access (sometimes you do want the underlying `*Conn`), and adds a real per-call atomic. Probably not worth it for the 90% case (functions that own their own resources) — defer until profiles or user reports show resource ownership crosses function boundaries often enough that the existing diagnostics become annoying.

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

### Type conversion / structural typing

- **#94 follow-up — `q.ConvertTo` extensions.** The struct-to-struct core (exact-name auto-derivation, recursive nested derivation, `q.Set` / `q.SetFn` overrides, target-driven field gaps) shipped — see [`docs/api/convert.md`](../api/convert.md). Open extensions:

  - **Implicit lifting.** `int → int64`, `*T → T` (non-nil-asserted deref), `T → Option[T]` / `T → sql.NullX` wrapping. Keep the bar conservative: only lifts where there's no information loss and no panic risk under any input. Anything else stays an explicit `q.SetFn`.
  - **Slice / map / iter recursion.** `[]Foo → []Bar` when `Foo → Bar` is auto-derivable; same for `map[K]Foo → map[K]Bar`. Emit a `for` loop or `iter.Seq` call inside the IIFE. Decide whether iterators count.
  - **Field renames.** `q.Rename("FooID", "ID")` for shape-only mismatches. Currently expressible via `q.SetFn("ID", func(s S) int { return s.FooID })` — the rename helper would be ergonomic sugar but is not load-bearing.
  - **Duck-typed interfaces.** `Target` is an interface whose method set is a subset of Source's; emit a method-shim wrapper. Probably out of scope — Go's structural interfaces already cover most of this without a helper.
  - **Cross-package targets with unexported fields.** Auto-derivation considers exported fields only, so unexported targets in another package fail. No clean path without breaking encapsulation.

  Pick up when the implicit-lifting cases or slice/map recursion cases show up in real code; the current strict surface forces explicit overrides for everything that isn't a 1:1 same-type field copy, and that's a feature, not a bug, until evidence says otherwise.

- **#95 — Variance tags / casting tricks for covariance & contravariance.** Go's type system is strictly invariant: `[]Animal` and `[]Dog` are unrelated even when `Dog` satisfies `Animal`; `func(Animal)` and `func(Dog)` likewise. Common Go idiom is to copy element-by-element or to introduce an interface, both of which leak into call sites. The question this entry parks: can the preprocessor offer a Scala-like variance annotation (`q.Covariant[T]` / `q.Contravariant[T]` markers, or a `q.Variant[+A]` shape declaration) that gets erased at rewrite time, with the rewriter emitting the per-element copy / interface-conversion / unsafe-cast that Go won't generate itself?

  **Investigation directions before coding:**
  - **Type-system pressure points.** Where does invariance bite hardest in real Go code? Slice element widening (`[]*Concrete` → `[]Iface`) is the canonical case; `chan<-` / `<-chan` on directional channels is another; function-argument variance (parameters contravariant, returns covariant) shows up when assigning method values. Pick one or two concrete pain points, design the annotation around those.
  - **Erasure mechanism.** A pure rewriter trick — the annotation is a compile-time marker, the rewriter emits `runtime: copy slice / wrap iter / unsafe.Slice cast` based on inferred direction. Avoid `unsafe` if a typed shape suffices. Slice covariance: emit `dst := make([]Iface, len(src)); for i := range src { dst[i] = src[i] }`. Channel directionality: rewrite to a goroutine-bridged copy. Function variance: emit a wrapper closure.
  - **Hack-the-type-system option.** Investigate whether Go's existing type assertions + unsafe.Pointer can fake covariance for slices of interfaces under specific layout conditions (interface header is fixed-size, so `[]*T → []I` *might* be a no-op cast under tight constraints — but Go's runtime layout for interface slices includes itab pointers, so a memcpy fake almost certainly UB. Document conclusively before promising anything.)
  - **Surface options to compare:** declarative (`type Container[+A] struct{...}` with the rewriter generating the conversion at use-site), imperative (`q.Widen[Iface](slice)` returns the converted slice), or annotation on existing types (`var _ = q.Covariant[*Dog, Animal]()` directives that set up auto-conversion rules globally for the package).

  **Realistic scope:** this is a research entry. The hard part is the type-system spelunking, not the codegen. Pick the most fun shape first — slice element widening is the canonical entry point and exercises the rewriter's per-element copy machinery.

- **#96 — Implicit conversions, Scala-style.** Scala 2's `implicit def` and Scala 3's `given`/`Conversion` let the compiler insert a conversion between mismatched types at the call site without the user spelling it. Useful when calling APIs that want a slightly different type than the one in hand, and the conversion is unambiguous (`int → BigInt`, `String → Path`, domain-specific units like `Duration → Long`). Both versions paid for the convenience: Scala 2 implicits became infamous for invisible action-at-a-distance; Scala 3's `given` pulled the surface toward explicit `using` clauses to claw clarity back. Worth investigating which (if either) generalises into a sane Go-via-q feature.

  **What an analogue would look like:**
  - User declares a registered conversion: `var _ = q.Implicit(func(d time.Duration) int64 { return int64(d) })`. The directive is read by the rewriter at scan time.
  - At any call site where Go's type checker would reject (e.g. `setTimeout(5*time.Second)` where `setTimeout` takes `int64`), the preprocessor consults its registered conversions and, if exactly one applies, inserts the call: `setTimeout(int64(5*time.Second))`.
  - Ambiguity (two conversions both apply) is a build-time error with both candidates listed.
  - Visibility scopes: per-package, per-file, or per-import. Scala 3's `given` is the lesson here — Scala 2 leaked implicits across the whole compilation unit and projects regretted it.

  **Comparing Scala 2 vs Scala 3 framings:**
  - **Scala 2 (`implicit def`)**: implicit conversions auto-applied wherever a type mismatch exists; readers can't see the conversion at the call site. Frequently led to surprise bugs when removed conversions caused wide breakage. Lesson: don't make conversions invisible at call sites.
  - **Scala 3 (`given`/`Conversion[A, B]` + `import x.given`)**: conversions are still implicit but require an opt-in import; the type signature `Conversion[A, B]` is more discoverable than a free-standing `implicit def`. Lesson: explicit-import gates make implicits manageable.
  - **Hybrid for q:** make the directive opt-in per-file (e.g. `var _ = q.UseImplicits(myConversionSet)` at file scope), and require the conversion's source/target pair to be unambiguous within the registered set. Build-time errors over runtime panics; per-file activation over global.

  **Why this is q-shaped, not Go-shaped:** Go intentionally rejects implicit conversion to keep call sites obvious. q can offer this as a deliberate opt-in feature for codebases that want it, with the rewriter making the inserted call explicit in the generated code (an IDE that follows q's debug output sees the explicit conversion). The rejection-by-default rule of Go stands; q lets a project lift it locally.

  **Open questions that gate any work:** is the build-time discoverability cost (conversions are non-local — readers might not know which conversions are registered without reading the directives) worth the call-site brevity? The Scala 3 `using`-import gating is a workable answer; whether the noise reduction at `int64(5*time.Second)`-style call sites earns the invisibility cost is the call to make.

- **#97 — Truly immutable data, with build-time enforcement.** Go's only built-in immutability is `const` (basic types only) and unexported fields (which discipline the *package*, not the *value*). Real immutability — "this struct CANNOT be mutated after construction, period, and the compiler tells you when you try" — is missing. q's preprocessor sees enough source to enforce it; the question is whether the surface earns the lift.

  **What "immutable" means here:**
  - Direct field write rejected: `cfg.DB = "x"` → build error.
  - Pointer rebinding rejected: if `cfg *Config` is immutable, `*cfg = Config{...}` → build error (same address, mutated payload).
  - Slice/map/chan elements: trickier — Go's slice/map are reference types; mutation through them isn't a write to the holder. Either declare the holder *and* its contents recursively immutable, or stick to value semantics.
  - Method calls that mutate via pointer receiver: rejected unless the method is itself marked pure.

  **Surface candidates:**
  - **Type-level marker.** `type Config q.Immutable[struct{ DB string }]` — the marker is erased at rewrite time but the scanner records that any value of `Config` is immutable.
  - **Field-level marker.** `type Config struct { DB string `q:"immutable"` }` — finer grain, allows mixed structs.
  - **Construction-only.** `cfg := q.Frozen(&Config{DB: "x"})` returns a typed-immutable wrapper; the wrapper is unwrapped only by `q.Read(cfg).DB` access syntax. Loud at use sites.

  **Enforcement mechanism — preprocessor's role:**
  - At scan time, build a set of immutable type identities (from markers) and immutable-value bindings (from `q.Frozen` results).
  - Walk every assignment / address-taking / pointer-deref-write site; flag any write whose LHS resolves to an immutable type or binding.
  - Emit build-time diagnostics with file:line:col, same format as the existing q.Assemble diagnostics.
  - Method calls: walk method bodies looking for writes to `*receiver`; flag any non-pure method called on an immutable value.

  **Slice/map element story.** Two layers to consider:
  - Holder immutability: the field referencing the slice can't be reassigned (covered by direct write enforcement).
  - Element immutability: the slice's contents can't change. Requires the slice's *element type* to also be immutable, or a marker like `[]q.Immutable[T]`. Recursive depth.

  **Why this is q-shaped:**
  - Pure runtime can't enforce it (reflection can copy and mutate; finalizers see writes).
  - Codegen can detect-and-reject at scan time. The user's effort is one annotation per type; q does the consistency check across the whole compile.
  - IDE-friendly: the markers are valid Go (a generic type wrapper or a struct tag); editors don't squiggle. The immutability is a build-time invariant, not a syntax extension.

  **Known hard cases:**
  - **Embedded struct with mutable fields:** does the immutability propagate? Probably yes, recursively, with explicit opt-out via `q.Mutable[X]` for narrow fields that need it.
  - **Interfaces:** an interface holding an immutable concrete is fine; an interface holding a `*Mutable` is not. Tracking this requires the rewriter to know, at every interface-typed binding, what concrete type was assigned.
  - **Generics:** `func Update[T any](v T)` — the body can't write to `v` (Go already restricts that; T-via-pointer is the loophole). The check needs to be on the call site or on the param type.

  **Open question worth chewing on:** what fraction of the bugs an immutable-data feature would prevent are *not* already prevented by Go's value semantics + small structs? Real codebases use mutability deliberately for builders, accumulators, lazy initialization. The pitch is "truly immutable for the cases where mutation is wrong" — the framing matters more than the surface here. Reference: Scala's `val`, Rust's `let` (vs `let mut`), Swift's `let`, and Kotlin's `val` are all worth comparing for surface trade-offs.

### Future / parking lot

- **#84 — `q.Assemble` parallel construction (Phase 4).** Surface: `q.WithAssemblyPar(ctx, n)` rides on the ctx like `q.WithAssemblyDebug`; rewriter emits topo waves with `sync.WaitGroup` per wave. Phases 1–3 shipped (single-entry auto-derived DI, `AssembleAll`, `AssembleStruct`, AssemblyResult chain with `.DeferCleanup()` / `.NoDeferCleanup()`). Parked because the sequential path is fast enough for current workloads — revisit if profiles show construction time as a measurable cost. Plan still lives in [`docs/planning/assemble.md`](assemble.md) for when it's picked back up.

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

- **#93 follow-up — q.FnParams: embedded struct propagation.** v1 of `q.FnParams` validates only direct field membership. Embedded structs are skipped — if `Inner` has the marker and `Outer` embeds `Inner`, `Outer` literals that don't go through Inner aren't validated. Decide whether the marker should propagate (and how — flatten vs. recursive validation) before users hit it in practice.

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
