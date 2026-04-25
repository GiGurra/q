# q.Assemble — comprehensive implementation plan

This document is the resume-point for implementing TODO #84 (`q.Assemble`). It is self-contained — a fresh session with no prior context should be able to read this and start implementing without re-deriving design decisions.

Cross-references:
- Surface sketch in [TODO.md #84](TODO.md) (kept lean; this doc is the authoritative plan)
- Existing patterns to reuse: scanner family pattern (`familyMatch`, `familyAtCompileTime`), per-call typecheck (`resolveMatch`), source-rewrite (`buildMatchReplacement`), q.Try bubble shape (`renderTry`), q.Tagged for branding.

## Goal

ZIO ZLayer-style auto-derived dependency injection at preprocess time. The user lists "recipes" (plain Go functions) at the call site; the preprocessor builds a dep graph keyed by output type, topo-sorts it, and emits a flat sequence of calls building the requested target. No codegen step, no runtime reflection, no manual ordering.

The pitch is: stop reaching for codegen tools (google/wire) or runtime DI containers (uber/fx, samber/do). The recipes are plain Go functions; the orchestration is one expression.

We use the term "Recipes" rather than "Layers" — Go's flat-functions-and-types model maps better onto the cooking metaphor than ZIO's monadic ZLayer.

```go
type Config struct{ DB string }
type DB struct{ cfg *Config }
type Server struct{ db *DB; cfg *Config }

func newConfig() *Config              { return &Config{DB: "..."} }
func newDB(c *Config) (*DB, error)    { return &DB{cfg: c}, nil }
func newServer(db *DB, c *Config) *Server { return &Server{db: db, cfg: c} }

server := q.Try(q.AssembleErr[*Server](newConfig, newDB, newServer))
```

## Phasing summary

The work splits cleanly into three phases. Each phase ships independently and is useful on its own.

- **Phase 1 — auto-derived assembly with diagnostics.** The core feature: list recipes, get the target. Compile-time errors for missing dep / duplicate provider / cycle / unused recipe / unsatisfiable T. Tagged services and inline-value providers come for free.
- **Phase 2 — selection & multi-output.** `q.AssembleAll[T]` (multiple legitimate providers of T), struct-target multi-output (`q.Assemble[Apps]` populates each `Apps` field), nicer ergonomics for slice/append composition.
- **Phase 3 — resource lifetime.** Recipes that produce resources (returning `q.OpenResult[T]`) integrate with q.Open's existing deferred-cleanup machinery. Reverse-topo teardown is automatic via Go's defer LIFO order.

ZIO features intentionally NOT carried over:
- **Composition operators (`++`, `>>>`, `>+>`)** — don't fit Go syntax. Recipes are listed at the call site; group them via package-level slices + `slices.Concat` if needed.
- **Service pattern (`ZIO.service[DB]`)** — needs ZIO's monadic env. Replaced by named function inputs.
- **Failures vs defects** — Go has one error.

## Phase 1 — auto-derived assembly

### Public surface

```go
// Pure assembly — every recipe is f(deps...) T (no error).
func Assemble[T any](recipes ...any) T

// Errored assembly — at least one recipe is f(deps...) (T, error).
// Composes with q.Try / q.TryE.
func AssembleErr[T any](recipes ...any) (T, error)

// Chain variant — composes with .Wrap, .Catch, .Err, .ErrF, .Wrapf.
func AssembleE[T any](recipes ...any) ErrResult[T]
```

`recipes` is `...any` because Go's type system can't express "any function with any number of inputs and one output". The preprocessor's typecheck pass takes over validation — same shape as `q.Match`'s `value any`.

### What counts as a recipe

Each variadic argument can be:

1. **A function reference.** Any function whose return signature is one of:
   - `(R)` — pure recipe.
   - `(R, error)` — errored recipe (only valid in `AssembleErr` / `AssembleE`).

   Inputs become required deps. Recipes can be top-level functions or methods (bound), as long as `go/types` resolves a `*types.Signature` for them.

2. **An inline value.** Any non-function expression. Its type IS the provided type; no inputs are required. Direct ZIO `ZLayer.succeed` analogue.

   ```go
   cfg := loadConfig()
   server := q.Try(q.AssembleErr[*Server](newDB, newServer, cfg))
   //                                                         ^^^ provides *Config
   ```

   Distinguishing function refs from values: inspect the AST node type at the q.Assemble call. `*ast.Ident` whose resolved object is `*types.Func` → recipe. `*ast.SelectorExpr` resolving to `*types.Func` (e.g. `pkg.NewDB`) → recipe. Anything else → inline value.

   Function *calls* (`getRecipe()`) are also accepted as inline values — the preprocessor sees them as expressions whose type is the call's return type. That mirrors how `q.Match`'s value arg works.

### Resolution mechanism

In the typecheck pass, after `go/types` has resolved expression types:

1. **Classify each recipe arg:**
   - Function ref → extract `*types.Signature`. Inputs = `Signature.Params()`. Output = `Signature.Results().At(0)`. If `Results().Len() == 2 && Results().At(1) == error`, mark as errored.
   - Inline value → input list empty. Output = expression's type.

2. **Build the provider map.** For each recipe, key the provided output type to the recipe index. If two recipes provide the same type → diagnostic `q: q.Assemble has duplicate provider for type X`.

3. **Topo-sort via Kahn's algorithm.** Start from recipes with empty input lists (leaves). For each, add its output to a "ready" set, then walk recipes whose inputs are all in "ready" — add them in turn. Detect cycles when no progress is possible but recipes remain unprocessed.

4. **Trace deps for the requested T.**
   - If T isn't in the provider map → diagnostic `q: q.Assemble's target type X is not produced by any recipe`.
   - Otherwise, walk T's dep tree; collect every transitively-required recipe.
   - Recipes not transitively reached → diagnostic `q: q.Assemble has unused recipes: <list>`. (Strict by default; could relax behind a flag if it's annoying in practice.)

5. **Diagnostics carry file:line:col.** The position of the recipe argument in the q.Assemble call.

### Tagged services (free win)

`q.Tagged[U, T]` already produces distinct types per brand. So:

```go
type _primaryDB struct{}
type _replicaDB struct{}

func newPrimary() (q.Tagged[*DB, _primaryDB], error) { ... }
func newReplica() (q.Tagged[*DB, _replicaDB], error) { ... }
func newServer(p q.Tagged[*DB, _primaryDB], r q.Tagged[*DB, _replicaDB]) *Server { ... }

server := q.Try(q.AssembleErr[*Server](newPrimary, newReplica, newServer))
```

The graph treats `Tagged[*DB, _primary]` and `Tagged[*DB, _replica]` as distinct keys — no special code needed. This falls out of phase 1 if we don't do anything special; it's worth a fixture but not implementation effort.

### Rewrite emission

```go
server := q.Try(q.AssembleErr[*Server](newConfig, newDB, newServer))
```

Rewrites to the equivalent of:

```go
server := func() *Server {
    _qDep0 := newConfig()
    _qDep1, _qErr1 := newDB(_qDep0)
    if _qErr1 != nil { return *new(*Server) }
    _qDep2, _qErr2 := newServer(_qDep1, _qDep0)
    if _qErr2 != nil { return *new(*Server) }
    return _qDep2
}()
```

Wait — the `if _qErr != nil` shape needs to bubble. The exact bubble-shape depends on the form:

- `q.Assemble[T]` (pure): no error path; emit straight assignments.
- `q.AssembleErr[T]` (returns (T, error)): bubble via the IIFE's own return: `func() (T, error) { ...; if _qErr != nil { return zero, _qErr }; return result, nil }()`.
- `q.AssembleE[T]` (chain): same as AssembleErr but the chain method shapes the error (Wrap/Catch/etc.) before returning.

Composition with `q.Try`: `q.Try(q.AssembleErr[*Server](...))` is the natural shape. The rewriter sees `q.Try` wrapping `q.AssembleErr` and the existing q.Try machinery handles the error bubble at the enclosing function's level — q.AssembleErr's own IIFE returns `(T, error)`, q.Try unpacks it.

### Naming inside the rewrite

- `_qDep<N>`: each recipe's output, indexed in topo order.
- `_qErr<N>`: each errored recipe's error slot.
- For named types, the temp could be `_qDep<basename>` for readability (`_qDepConfig`, `_qDepDB`), with `_qDep<N>` as a fallback if collisions happen. Stretch goal — `_qDep<N>` is fine for v1.

### Per-`qSubCall` storage

Add to `qSubCall`:

```go
AssembleTargetType   ast.Expr   // the [T] type arg
AssembleRecipes      []ast.Expr // raw recipe expressions
AssembleResolved     []recipeStep // populated by typecheck (topo-ordered)
```

`recipeStep` carries: which recipe (function ref / inline value), input dep types in order, output type, errored flag.

### Five forms

Same as q.Try / q.AssembleErr / etc. Inherit existing form-handling — q.AssembleErr is value-producing, so `formDefine` / `formAssign` / `formDiscard` / `formReturn` / `formHoist` all work via the existing IIFE-substitution path the rewriter uses for q.Match.

### Tradeoffs vs. existing tools

- **vs. google/wire** — wire generates a separate file via codegen step; q.Assemble is inline, no `wire.go` to keep in sync. Same compile-time guarantees.
- **vs. uber/fx / samber/do** — those resolve at runtime via reflection. Slower startup, errors at runtime. q.Assemble errors at build time; fixture for "missing dep" build-fails as expected.

### Phase 1 deliverables

1. **Stubs in `pkg/q/assemble.go`** — three top-level funcs + a phantom `AssembleArm`-style type if needed for chain composition.
2. **Scanner** — `familyAssemble` / `familyAssembleErr` / `familyAssembleE`. Classify the call, capture target type + recipe expressions.
3. **Typecheck** — `resolveAssemble`: per-recipe type extraction, topo-sort, missing/duplicate/cycle/unused/unsatisfiable diagnostics. Mirror `resolveMatch`'s structure.
4. **Rewriter** — `buildAssembleReplacement`: emit the IIFE with the topo-ordered call sequence; bubble errors via the IIFE's return for the Err / E forms. Reuse q.TryE's chain-method dispatch for AssembleE.
5. **Fixtures** — `assemble_run_ok`, `assemble_err_run_ok`, `assemble_e_chain_run_ok`, `assemble_tagged_run_ok`, `assemble_inline_value_run_ok`. Plus rejected: `assemble_missing_dep_rejected`, `assemble_duplicate_provider_rejected`, `assemble_cycle_rejected`, `assemble_unused_recipe_rejected`, `assemble_unsatisfiable_rejected`.
6. **Docs** — `docs/api/assemble.md` describing the surface, dispatch rules, diagnostics. Update mkdocs nav and add a README section.
7. **Remove #84 phase 1 scope from TODO.md** in the same commit; keep phase 2/3 entries.

## Phase 2 — selection & multi-output

### `q.AssembleAll[T]` — multiple providers

When several recipes legitimately produce the same type (plugins, handlers, middlewares). Returns `[]T` instead of erroring on duplicate-provider.

```go
type Plugin interface{ Name() string }

func newAuthPlugin()    Plugin { return &authPlugin{} }
func newLoggingPlugin() Plugin { return &loggingPlugin{} }
func newMetricsPlugin() Plugin { return &metricsPlugin{} }

plugins := q.AssembleAll[Plugin](newAuthPlugin, newLoggingPlugin, newMetricsPlugin)
```

Implementation: same dep graph as phase 1, but the duplicate-provider check is replaced with "collect all providers of T". The output is a `[]T` literal of all collected values.

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

Implementation: when T is a struct, iterate its fields. Each field's type must be in the provider map; otherwise diagnostic. The IIFE emits a struct literal initialised from the dep temps.

### Inline-value-as-explicit-recipe

Already covered in phase 1 (any non-function arg is treated as a constant provider). Phase 2 might add `q.Provide[T](v T)` as an explicit constructor for cases where Go can't infer the provider type from the value alone (e.g., interfaces). Probably YAGNI; ship phase 1 without it.

### Phase 2 deliverables

1. Stubs for `q.AssembleAll[T]`.
2. Typecheck + rewriter handling for the `[]T` output shape.
3. Struct-target detection and emission (modify the existing `buildAssembleReplacement`).
4. Fixtures covering: AssembleAll with 2+ plugins; struct-target with mixed recipe types.
5. Docs update.

## Phase 3 — resource lifetime

The piece that makes q.Assemble compete with ZIO's `ZLayer.scoped`. When a recipe acquires a resource that needs cleanup, it returns `q.OpenResult[T]`; the assembler integrates with q.Open's existing machinery so the cleanup is registered as a `defer` at the enclosing function — fired in reverse-topo order automatically by Go's defer LIFO.

```go
func openDB(c *Config) q.OpenResult[*DB] {
    return q.Open(connectDB(c.URL))
}

func openServer(db *DB, c *Config) (*Server, error) {
    return &Server{db: db, cfg: c}, nil
}

server := q.Try(q.AssembleErr[*Server](newConfig, openDB.Release((*DB).Close), openServer))
```

Wait — q.Open(...).Release returns T, not OpenResult[T]. The recipe shape would need a slight adaptation.

**Two options for resource recipes:**

1. **`(T, func(), error)`-returning recipes.** The recipe returns the resource, an explicit cleanup thunk, and an error. The assembler emits `defer cleanup()` after a successful build. Simple, no q.Open coupling.

   ```go
   func openDB(c *Config) (db *DB, cleanup func(), err error) {
       db, err = connectDB(c.URL)
       if err != nil { return nil, nil, err }
       return db, func() { db.Close() }, nil
   }

   server := q.Try(q.AssembleErr[*Server](newConfig, openDB, newServer))
   // generated code: ...; defer cleanup(); ...
   ```

2. **q.OpenResult[T] integration.** The recipe returns `q.OpenResult[T]` directly; the assembler unpacks it via the existing Release machinery. Tighter coupling with q.Open but reuses more infrastructure.

**Lean toward option 1.** Simpler shape, doesn't require recipes to know about q.Open's chain types. A user who wants q.Open-style cleanup writes the boilerplate explicitly:

```go
func openDB(c *Config) (*DB, func(), error) {
    db, err := connectDB(c.URL)
    if err != nil { return nil, nil, err }
    return db, func() { _ = db.Close() }, nil
}
```

The assembler sees the `(T, func(), error)` shape and treats it as a "resource recipe": registers the cleanup as a `defer` after the recipe call.

### Reverse-topo teardown

Go's `defer` is LIFO — the last-registered defer fires first. Since topo-order has dependencies created BEFORE their dependents, registering `defer` at each step means dependents are torn down before their dependencies. Exactly the right order. No special scheduling logic needed.

### Failure semantics

If recipe N fails (`err != nil`), recipes N+1...end never run, and only the cleanups registered before N's failure fire. Same as Go's natural defer-on-error pattern.

The IIFE emits something like:

```go
func() (*Server, error) {
    _qDep0 := newConfig()
    _qDep1, _qCleanup1, _qErr1 := openDB(_qDep0)
    if _qErr1 != nil { return nil, _qErr1 }
    defer _qCleanup1()   // teardown registered ONLY after success
    _qDep2, _qErr2 := newServer(_qDep1, _qDep0)
    if _qErr2 != nil { return nil, _qErr2 }
    return _qDep2, nil
}()
```

But wait — the function returns `_qDep2` which depends on the resource `_qDep1` that's about to be cleaned up via the deferred `_qCleanup1()`. This is the same use-after-close pattern q.Open has — and the resource-escape detection (see `escape.go`) already catches it!

So we get the safety check for free: the assembler emits ordinary `defer cleanup()` calls, and the existing escape detection catches "resource escapes the function via the assemble result." Users who genuinely want to factory out a resource use the `//q:no-escape-check` opt-out.

### Scoped variant

For "the resource lives only as long as this scope" — that's already what the IIFE form does. The cleanups fire when the IIFE returns. If the user assigns the result to a variable that outlives the assembly, the resources are already torn down. Same use-after-close concern; same escape check.

**No special `q.AssembleScoped[T]` needed** — the regular `q.Assemble` family naturally has the right scoping.

### Phase 3 deliverables

1. Detect `(T, func(), error)` recipe signature in the typecheck pass.
2. Rewriter emits `defer _qCleanupN()` after each successful resource recipe call.
3. Fixtures: resource recipe with cleanup; chained resources where teardown order matters; failure mid-chain triggers partial cleanup; escape detection catches return-of-resource-through-assemble.
4. Docs update covering the resource-recipe shape.

## Open questions / future considerations

- **Recipe groups via package-level slices.** ZIO has `ZLayer ++ ZLayer` for combining layer sets. In Go, the equivalent is `slices.Concat` over slices of recipe values. q.Assemble accepts `recipes ...any` already; spreading a slice via `recipes...` works. We could ship a tiny helper like `func RecipeSet(recipes ...any) []any` for grouping, but YAGNI — `[]any{newConfig, newDB, newServer}` works as-is.

- **Subset assembly / "request these N types".** Sometimes the user wants several products from one recipe set. Phase 2's struct-target covers this idiomatically. Multi-return form `db, cache := q.Assemble2[*DB, *Cache](...)` would be more direct but adds N families (Assemble2, Assemble3, ...). Skip in favour of struct-target.

- **Provider override.** ZIO has `ZLayer.fresh` and `ZLayer.passthrough` for re-providing a layer in a sub-scope. With q's per-call recipe scope (recipes are listed at each q.Assemble call), this falls out — just list the override recipe instead of the original.

- **Diagnostics include suggestions.** When the user forgets a recipe, the diagnostic should list "type X needed by Y" — phase 1 already does this; consider also "did you mean: <closest matching provider>?" for typo-style mistakes. Stretch goal.

- **Cross-package recipe types.** When a recipe lives in a different package than the q.Assemble call, the type qualifier needs to handle qualified names. Mirror what `resolveMatch`'s `qualifier` callback does.

- **Performance.** Topo-sort over N recipes is O(N²) if implemented naively (each recipe checks all others' inputs). Real-world recipe sets are small (10s, maybe 100s), so we can stick with the simple Kahn's algorithm without indexing.

## Resume checklist for a cold-state implementer

1. Read this doc end-to-end.
2. Scan `internal/preprocessor/scanner.go`'s `familyMatch` + `parseMatchArms` for the per-call typecheck pattern to mirror.
3. Scan `internal/preprocessor/typecheck.go`'s `resolveMatch` for the type-resolution / diagnostic pattern.
4. Scan `internal/preprocessor/enums.go`'s `buildMatchReplacement` for the IIFE-substitution rewrite pattern.
5. Read `pkg/q/match.go` for the stub style.
6. Start with phase 1 stubs (`pkg/q/assemble.go`), then scanner, typecheck, rewriter, fixtures, docs — in that order.
7. Ship phase 1 as one commit, then phase 2 (smaller), then phase 3 (resource integration).
