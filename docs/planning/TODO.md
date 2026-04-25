# TODO

The persistent backlog for `q`. Mirrors the in-session task list so a fresh conversation (or anyone reading the repo cold) can pick up where we left off without re-deriving priorities.

## Session snapshot (latest first)

**As of commit `6203ec7` (2026-04-25):** the rejected-Go-proposal expansion is well underway. Shipped this session: `q.Enum*`, `q.Exhaustive`, `q.F`/`Ferr`/`Fln`, `q.SQL`/`PgSQL`/`NamedSQL`, `q.GenStringer`/`GenEnumJSONStrict`/`GenEnumJSONLax`, `q.Upper`/`Lower`/`Snake`/`Kebab`/`Camel`/`Pascal`/`Title`, `q.Fields`/`AllFields`/`TypeName`/`Tag`, `q.Match`/`Case`/`CaseFn`/`Default`/`DefaultFn`, plus a generalisation pass on the rewriter (drop self-substitution exclusion + broaden direct-bind check + walk container-statement headers + line-preserving rewrites skip the //line directive). Open from the proposal expansion: `#72` named args, `#74` sum types, `#75` phantom types, `#76` ternary, `#78` embed rewire/proven. Plus #67 (GLS) parked with full design notes, and a stretch goal: enforce `q.Exhaustive` / `q.Match` require `default:` when V is opted into `q.GenEnumJSONLax`.

**Next-up addition (2026-04-25):** Scala/lo-style functional data ops added to the backlog — `#80` (sequential `q.Map`/`FlatMap`/`Filter`/`GroupBy`/`Exists`/`ForAll`/`Find`/`Reduce`/`Distinct`/etc., each with bare + `…Err` + `…E` flavours), then `#81` (`q.ParMap`/`ParFlatMap`/etc., parallel via `runtime.NumCPU()` default with ctx-carried `q.WithPar(ctx, n)` override). Pure runtime helpers (no rewriter), so they slot under `qRuntimeHelpers`. **Order is settled: ship #80 first to lock the surface, then #81 layers on the same naming.**

**Status update (2026-04-25):** #80 and #81 both shipped. Surface now covers the complete data-ops kit (sequential + parallel) without forcing users into a Either/Option monad. New backlog additions parked: `#82` (q.AtCompileTime — universal preprocessor-time evaluation escape hatch), `#83` (q.Open resource-escape detection + ARC for non-RAM resources), `#84` (q.Assemble[T] compile-time DI graph — ZIO ZLayer / google-wire shape), `#85` (coroutines — three tiers from iter.Seq sugar to stackless preprocessor-rewritten state machines).

The big-picture trajectory: q is becoming a Scala-style compile-time macro toolkit for Go — every shipped helper folds at the AST level, runtime cost is zero, IDE sees ordinary Go. Each new feature reuses the typecheck pass + file-synthesis primitive established earlier.

**Standing rule (bookkeeping).** When a task lands, move it to the "Done" section *in the same commit* with a one-line note about what shipped (and a commit ref if useful). When a new task is created, add it here *in the same commit* that creates the in-session task. The two views must not drift.

**Standing rule (design).** q only accepts syntax Go itself accepts — see `docs/design.md` §7. Reject any feature that would light up as a type/syntax error in gopls. This kills some nice-reading shapes (auto-inferred `, nil` tails, auto-injected trailing `return nil`, omitted return values) but it's non-negotiable: the IDE experience is the whole reason we're a toolexec rewriter instead of a custom parser.

## Open

### High-impact gaps in the public surface

**E-variant convention.** Every bubble-shaped feature gets both a bare form and an `…E` chain form that exposes the standard vocabulary — `.Err(error)`, `.ErrF(fn)`, `.Wrap(msg)`, `.Wrapf(format, args…)`, `.Catch(…)` — matching what Try/NotNil/Check/Open already expose. Features that do not bubble (compile-time prints, panics, defer sugar) explicitly have no E-variant.

_(All of #21–#30 and #32 shipped — see the Done ledger. Open list is now empty of top-priority items; reach for the parking lot below or propose something new.)_

**Dropped from the plan**

- ~~**#23 — `q.Default` / `q.DefaultE`**~~ — **removed after shipping**. The 2-arg form `q.Default(strconv.Atoi(s), -1)` looked ergonomic but isn't valid Go: the `f(g())` multi-return spread rule only applies when `g()` is the *sole* argument to `f`, and `q.Default`'s trailing `fallback` arg breaks that. IDEs would flag it. The 3-arg pre-destructured form was valid but redundant with `q.TryE(call).Catch(func(e error) (T, error) { return fallback, nil })`, which reads more explicitly anyway. Removed to honour the "only accept Go-valid syntax" golden rule.
- ~~**#28 — `q.Go`**~~ — **removed after shipping**. Overly opinionated: locked in `println`-to-stderr for panic logging, a compile-time file:line format nobody asked for, and a "recovery" policy that some apps explicitly don't want (process supervisors prefer crash-fast). Plain `go fn()` is one word longer and gives callers full control over panic policy + logging library. A 4-line wrapper in the caller's own module is cheaper than a q-level opinion.
- ~~**#29 — `q.TryCatch`**~~ — **removed after shipping**. The `.Catch(handler func(any))` handler has no return path, so panics caught by the block can't flow into the enclosing function's error return. `q.Recover` / `q.RecoverE` already cover the useful cases (function-boundary panic→error with full chain vocabulary). Block-scoped try/catch was pure satire and not worth the surface.
- ~~**#31 — `q.Must` / `q.MustE`**~~ — removed. Panicking is the opposite of what q exists to enable; the library's pitch is IDE-friendly explicit error forwarding, not abort. Callers who need "fail loudly at startup" already have `if err != nil { panic(err) }`. Not tracked for future implementation.

### Next up — rejected-Go-proposal expansion

The features below all fit q's model: parse as valid Go, rewrite at compile time, zero runtime overhead, IDE-native. Numbered in the order they came up. Each entry specs the surface, the Go-validity check, the rewriter sketch, and any tradeoffs we already worked through.

**Shipped from this batch (see Done ledger):** #68 (q.Enum* helpers), #69 (q.F / q.Ferr / q.Fln), #77 (q.SQL / q.PgSQL / q.NamedSQL).

- [x] **#68 — Enums (a serious attempt to make Go enums useful).** Go's `const X = iota` pattern is the de facto enum, but it ships with nothing — no String, no Parse, no list-all, no validity check, no JSON. The plan is to keep the existing declaration shape (so the user's source still reads as plain Go) and layer compile-time helpers + opt-in method synthesis on top.

  **Helper surface (call-site rewrite, cheapest first):**
  - `q.EnumValues[T]() []T` → literal slice of all const values of type T in declaration order
  - `q.EnumNames[T]() []string` → literal slice of identifier names
  - `q.EnumName[T](v T) string` → switch on value, return name; `""` for unknown
  - `q.EnumParse[T](s string) (T, error)` → switch on string, return value; sentinel error (`q.ErrEnumUnknown`) for unknown
  - `q.EnumValid[T](v T) bool` → membership check
  - `q.EnumOrdinal[T](v T) int` → 0-based position in declaration order

  **Directive surface (synthesize a method file, opt in):**
  - `var _ = q.GenStringer[T]()` → companion file with `func (T) String() string` using `EnumName` switch shape
  - `var _ = q.GenEnumJSON[T]()` → companion file with `MarshalText` / `UnmarshalText`
  - `var _ = q.GenEnum[T]()` → all of the above

  **Go-validity:** every form is a function call (helpers) or a `var _ = …` declaration (directives). No new syntax. The earlier `const Red = q.EnumValue[Color]()` form was rejected because Go forbids function calls in const initializers.

  **Rewriter sketch:**
  - Scanner: recognise `q.EnumX[T](…)` families. Capture T from the `IndexExpr.Index`.
  - Types pass: resolve T to a `*types.Named` in the importer-resolved package, walk its `*types.Package.Scope()` for `*types.Const` decls whose `Type()` matches T (in declaration source order — fall back to FileSet position sort).
  - For helpers: emit a literal slice / switch expression spliced at the call site. No bubble path; these are pure value rewrites.
  - For directives (`q.GenX`): add a file-synthesis pass alongside the existing per-package rewrite. Writes `_q_enum_<TypeName>.go` to `$TMPDIR` and appends to the compile argv (same primitive as `runtimestub.go`'s `writeTempGoFile`).
  - Detect collisions: if T already has a `String()` method, the GenStringer directive emits a diagnostic instead of generating (compiler would reject duplicate method anyway, but we want the message to point at the directive).

  **Bitset/flag variant (later wave):** `q.EnumFlagsString[T](v T) string` returns `"Read|Write"`; `q.EnumFlagsParse[T](s string) (T, error)` reverses. Detect via opt-in `var _ = q.GenFlags[T]()` rather than guessing from values.

- [x] **#69 — String interpolation `q.F`.** `q.F("hi {name}, age {age+1}")` rewrites to `fmt.Sprintf("hi %v, age %v", name, age+1)`. The string parses as plain Go (it's just an opaque literal), so IDE doesn't choke on the placeholders.

  **Surface:**
  - `q.F(format string, …) string` — base form, returns the formatted string
  - `q.Ferr(format string, …) error` — `errors.New(q.F(…))`-style shortcut
  - `q.Fln(format string, …)` — println to stderr, debug-shaped (similar to `q.DebugPrintln`)

  **Go-validity:** the input is a Go string literal. Variadic `…` is unused at the source level — present in the signature so any args after the format string also work for callers who want explicit positional, but the `{expr}` form is the primary path.

  **Rewriter sketch:**
  - Scanner: recognise `q.F(…)` family. The format-string argument must be a `*ast.BasicLit` of kind `STRING` (rejected diagnostic if dynamic).
  - Format parser: walk the literal text, find `{…}` segments, parse each segment as a Go expression via `parser.ParseExpr`. Brace-escape via `{{` / `}}` mirroring `text/template`. Reject unbalanced braces.
  - Emit: replace each `{expr}` in the literal with `%v`, build a positional `fmt.Sprintf(rewrittenFormat, expr1, expr2, …)`. Inject `fmt` import via existing `ensureImport`.

  **Tradeoff:** the IDE doesn't see `name` inside `q.F("hi {name}")` as a referenced identifier — go-to-def, rename, and unused-var detection don't apply to identifiers that exist only inside the literal. This is a real DX hit. Mitigation: emit a `var _ = name` companion expression alongside the rewrite? Probably not — clutters the rewritten file. Document it as the cost of admission.

- [x] **#70 — `match` expression.** Shipped — see Done ledger.

  **Surface (final design):**
  ```go
  // q.Match folds to an IIFE switch returning R. V must be comparable
  // (Go switch requirement). When V is an enum type and no q.Default
  // is supplied, the typecheck pass enforces coverage.
  func Match[V comparable, R any](value V, cases ...MatchCase[V, R]) R

  // q.Case constructs one case arm. Same Go-syntax shape used twice
  // for value-based and default arms.
  func Case[V, R any](v V, r R) MatchCase[V, R]
  func Default[V comparable, R any](r R) MatchCase[V, R]   // sentinel for default arm
  ```

  **Rewriter sketch:**
  - Scanner: recognise `q.Match` outer call. Walk `Args[1:]` for inner `q.Case` / `q.Default` calls, extract the value + result expressions per case onto the qSubCall.
  - Typecheck: when V is a `*types.Named` with constants and no q.Default arm exists, validate all constants are covered (reuses the q.Exhaustive coverage logic).
  - Rewriter: emit `(func(_v V) R { switch _v { case <vN>: return <rN> ... }; var zero R; return zero }(value))`. Default arm replaces `var zero R; return zero` with `return <defaultExpr>`.

  **Fixture targets:** value-based int / string switching, exhaustiveness over enums, q.Default arm path, composition with q.Try inside case results.

- [x] **#71 — Compile-time reflection.** Shipped — see Done ledger.

  **Surface:**
  - `q.Fields[T]() []string` → literal slice of exported field names (or all, with `q.AllFields[T]`)
  - `q.TypeName[T]() string` → e.g. `"main.User"` or `"github.com/x/y.User"` (qualified)
  - `q.Tag[T](field, key string) string` → struct tag value at compile time, e.g. `q.Tag[User]("Name", "json")` → `"name,omitempty"`
  - `q.Methods[T]() []string` → literal slice of method names defined on T
  - `q.Size[T]() uintptr` → `unsafe.Sizeof((*T)(nil))`-equivalent, but constant-folded

  **Go-validity:** generic calls. The first arg of `q.Tag` is a string literal — diagnose if dynamic.

  **Use case:** zero-cost JSON / CSV / SQL row mappers without `reflect`. The downstream user writes a tiny per-type marshaller that uses these constants for column names and tags; with q, the marshaller compiles to a direct field-access table without runtime introspection.

  **Rewriter sketch:** types pass resolves T, walks its method set / fields, splices a literal `[]string{"a","b","c"}` or `"foo"` at the call site. Inject `unsafe` import for `Size`.

- [ ] **#79 — Enforce `default:` on `q.Exhaustive` / `q.Match` for Lax-opted types.** When a type opts into `q.GenEnumJSONLax`, the wire format admits unknown values — that openness should propagate to switching: every `q.Exhaustive(v)` and `q.Match(v, …)` on such a type should require an explicit `default:` arm so unknowns are handled (rather than falling through to the missing-cases diagnostic).

  **Mechanism sketch.** When the typecheck pass walks the user-package's shapes, it already collects every `Gen*` directive (in `collectGenDirectives`). Build a per-package set of "Lax types" from those; pass the set to `validateExhaustive` and `resolveMatch`. When the matched value's type is in the set and no `default:` arm is present, surface a diagnostic: `q.Exhaustive on Color is opted into q.GenEnumJSONLax; the default: arm is required so unknown wire values are handled explicitly.`

  **Why parked.** Small, well-scoped, and design-aligned with the user's earlier "Lax should be allowed into exhaustive when using a default" framing. Pure addition: existing q.Exhaustive / q.Match with all-cases-covered + default still pass; only Lax-opted types without a default newly fail.

- [ ] **#72 — Named arguments via `q.Call` + `q.Named`.** Proposal #12854 (default arguments) and #29137 (named args) were both rejected. q can offer a workable shape for the named-args half.

  **Surface:**
  - `q.Call(fn, q.Named("timeout", 5*time.Second), q.Named("retries", 3))` — rewrites to a positional call with name → param-position mapping resolved by the rewriter. Arguments not named default to the param's zero value.
  - Default values via signature annotation: not feasible without new syntax. Skip.

  **Go-validity:** `q.Call` is a function call. The first arg is the callee (any func value), subsequent args are `q.Named(name, value)` results. The runtime stub for `q.Call` panics if reached (rewriter must transform it).

  **Rewriter sketch:** types pass resolves the callee's parameter names from `*types.Signature`. For each `q.Named(name, value)` arg, look up the position. Emit `fn(positional1, positional2, …)`.

  **Tradeoff:** doesn't extend to method values where the name is dynamic, doesn't help with overloading. Diagnostics for: name not found in signature, duplicate name, name on a callee whose params are unnamed.

- [x] **#73 — Compile-time string ops `q.Snake` / `q.Upper` / `q.Lower` / `q.Camel` / `q.Kebab` / `q.Pascal` / `q.Title`.** Shipped — see Done ledger.

  **Go-validity:** function call with string literal arg. Reject dynamic args at scan time.

  **Rewriter sketch:** AST literal extraction → string transform → emit `*ast.BasicLit` with the new value.

- [ ] **#74 — Sum types via `q.OneOf` / `q.Switch`.** Discriminated unions, the most-rejected of all rejected proposals.

  **Surface:**
  - `type Result q.OneOf2[Success, Failure]` — alias to a real generic struct that holds an `any` value + a small tag int
  - `q.MakeOneOf[Success, Result]("Success", success)` constructor (or per-arm sugar like `q.As1[Success, Result](v)`)
  - `q.Switch[R, U any](u U, arm1, arm2…) R` — exhaustive type-tagged dispatch; rewriter checks the union has exactly N arms and N cases passed

  **Go-validity:** all generic calls / generic type aliases. `q.OneOf2` is a real type with stub methods; the value lives at runtime as `any` + tag.

  **Tradeoff:** runtime cost is one `any` box (interface conversion). To avoid it for primitive variants, the rewriter could specialise `q.OneOf2[int, string]` to a struct-with-discriminator at compile time. Big lift; defer the optimisation.

  **Why now-ish vs much later:** sum types are the headline rejected proposal. Even a runtime-boxed version with exhaustiveness checking would be high-impact for users.

- [ ] **#75 — Phantom types / brands `q.Tagged[Underlying, Tag]`.** Compile-time distinct types over the same underlying type, no runtime cost.

  **Surface:**
  - `type UserID = q.Tagged[int, struct{ _userID }]` — `q.Tagged[U, T]` is a generic struct or alias chosen so the underlying ops still work
  - `q.UnTag[U, T any](v Tagged[U, T]) U` — unwrap
  - `q.MkTag[U, T any](v U) Tagged[U, T]` — wrap

  **Tradeoff:** without operator support, you can't write `userID + 1` directly. The rewriter could rewrite `q.MkTag[int, T](q.UnTag(x) + 1)` automatically for arithmetic, but it's clunky. Maybe just expose the unwrap/wrap and accept the verbosity.

- [ ] **#76 — Conditional expression `q.Ternary`.** Two shapes considered, only the thunked one is acceptable.

  **Eager form (rejected):** `q.Ternary(cond, a, b)` — both `a` and `b` evaluate as Go arguments before the call. The rewriter would have to lazily drop the unchosen arg, which silently changes the semantics from what plain-Go interpretation would suggest. Violates the principle that the rewrite is consistent with what the source-as-Go would mean.

  **Thunked form (accepted):** `q.Ternary(cond, func() T { return a }, func() T { return b })` — rewrites to `if cond { v = arg1() } else { v = arg2() }`. Verbose but correct: only one arg runs.

  Tradeoff: thunks are syntactic noise. Probably skip until a use case justifies it.

- [x] **#77 — Injection-safe SQL via `q.SQL`.** A specialised cousin of `q.F` (#69): same `{name}` interpolation surface, but rewrites to placeholder-style parameterised SQL — never inlines user values into the query string. The point is to make the safe form as ergonomic as f-string-style concatenation, so devs reach for it by reflex.

  **Surface:**
  ```go
  s := q.SQL("SELECT * FROM users WHERE id = {id} AND status = {status}")
  // s.Query → "SELECT * FROM users WHERE id = ? AND status = ?"
  // s.Args  → []any{id, status}
  db.QueryRowContext(ctx, s.Query, s.Args...)
  ```

  Dialect variants (same template parsing, different placeholder generator):
  - `q.SQL("…")` — `?` placeholders (default; SQLite, MySQL, database/sql with normalisation)
  - `q.PgSQL("…")` — `$1`, `$2`, … (lib/pq, pgx)
  - `q.NamedSQL("…")` — `:id`, `:status`, … (sqlx, named-param drivers)

  Returns a `q.SQLQuery` struct (real type, not a panic stub on the value side):
  ```go
  type SQLQuery struct {
      Query string
      Args  []any
  }
  ```

  **Go-validity:** function call with a string literal arg. The struct return is a valid Go type at all times.

  **Rewriter sketch:**
  - Reuses #69's format-literal parser. Each `{expr}` becomes a placeholder (numbered for `PgSQL`, kept positional for `SQL`, or named for `NamedSQL`); the inner expression is appended to a `[]any{…}` literal.
  - Emits `q.SQLQuery{Query: "rewritten", Args: []any{e1, e2, …}}` at the call site.
  - Brace-escape `{{`/`}}` like `q.F`. Reject dynamic format strings — that defeats the whole point (an attacker-influenced format would let `;DROP TABLE` slip in).

  **Tradeoff vs. raw `fmt.Sprintf` approach:** the rewriter physically cannot put user values into the query string — even if the developer mis-types, the output is still parameterised. This is the safety guarantee the helper exists to provide.

  **Stretch:** a `q.SQLIn(values)` placeholder for `IN (…)` lists (expands to `?, ?, ?` matching `len(values)` and appends each to Args). Bare `{values}` for a slice would be ambiguous (one placeholder vs N), so an explicit helper is clearer.

- [x] **#80 — Data manipulation helpers** — shipped. See Done ledger.

- [x] **#81 — Parallel data manipulation** — shipped. See Done ledger.

  **Surface:**
  - `q.ParMap[T, R](ctx context.Context, slice []T, fn func(context.Context, T) R) []R`
  - `q.ParMapErr[T, R](ctx, slice, fn func(context.Context, T) (R, error)) ([]R, error)`
  - `q.ParMapE[T, R](ctx, slice, fn func(context.Context, T) (R, error)) q.ErrResult[[]R]`
  - `q.ParFlatMap` / `q.ParFlatMapErr` / `q.ParFlatMapE` — same shape for slice-of-slice flattening
  - `q.ParFilter` / `q.ParFilterErr` / `q.ParFilterE` — likely worth it for IO-bound predicates; defer until a fixture demands it

  **The user fn takes ctx.** This is the precedent every parallel-Go library lands on: only the user code can preempt itself, so we hand it the context and document that long-running fns must check it. Sequential bare ops (#80) take `func(T) R`; parallel ops take `func(ctx, T) R`. Different signatures — by design.

  **Concurrency control via ctx:**
  - `q.WithPar(ctx, limit int) context.Context` — sets parallel limit on a private ctx key
  - `q.WithParUnbounded(ctx) context.Context` — opts out (limit = `len(input)`, fan-out everything)
  - `q.GetPar(ctx) int` — reads the limit (returns `runtime.NumCPU()` when unset)

  ctx-carried over options-arg because it composes through call graphs: top-level handler sets `ctx = q.WithPar(ctx, 8)` once, every nested ParMap respects it without re-threading. Parallel limit is naturally a "request-scoped resource budget" — same kind of thing already conventional to attach to ctx.

  **Cancellation semantics.** ParMap selects on ctx.Done in addition to worker completion. When ctx cancels, the result returns `(zero, ctx.Err())`; in-flight workers continue (Go has no goroutine kill) and their results are discarded. Mirrors `q.AwaitAllRawCtx`. Document explicitly: "Par fns don't preempt user fns — long-running user fns must check ctx themselves."

  **Error semantics for `…Err`.** First error wins (other workers' errors are discarded once one wins). Same precedent as `q.AwaitAllRawCtx`. A future `q.ParMapJoinErrors` aggregating via `errors.Join` is a possible follow-up; defer until somebody asks.

  **Implementation.** Pure runtime helper. Worker pool via a bounded `chan` for the input feed + N goroutines. Output assembled by input index so the result preserves order. `qRuntimeHelpers` carve-out. Some output-assembly + index-preservation logic may be reusable from the existing `q.AwaitAllRaw` machinery — worth a look at land-time.

  **Why later than #80.** Bare data ops earn their keep on every input size; parallel forms earn it only for IO-bound or CPU-heavy work. Shipping #80 first locks in the surface; #81 layers on top with the same naming. Skipping #80 → #81 ordering would force callers to reach for Par* on tiny slices where the goroutine overhead dominates the win.

- [ ] **#78 — Embed `rewire` and `proven` into q's toolexec dispatcher.** Right now a project that wants q + proven + rewire has to run them as a chain or pick one. q's `cmd/q` could become an umbrella dispatcher that detects which patterns are present in each compiled package and routes to the appropriate rewriter pass.

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

- [ ] **#82 — `q.AtCompileTime[R](fn func() R) R` (compile-time evaluation).** Earlier in q's design history this was excluded as too speculative; with the rest of the macro toolkit shipped (q.Match, q.Enum*, q.F, q.SQL, q.Fields, q.Tag, q.Gen*…) it now sits naturally next to them. Each call site folds at compile time to a literal carrying the result of evaluating fn. Pure runtime stub on the value path — body is `panicUnrewritten("q.AtCompileTime")` — the rewriter does the actual work.

  **Surface (working draft):**

  ```go
  // Evaluates fn at preprocessor time and splices the result as a Go
  // literal at the call site.
  func AtCompileTime[R any](fn func() R) R
  ```

  Variant ideas — pick whichever earns its keep:
  - `q.AtCompileTime(fn)` — no-arg func, returns R
  - `q.AtCompileTimeF[R any](fnExpr string) R` — evaluate a Go expression literal as a string (more powerful but harder to type-check at the IDE level — probably skip, fights the "valid Go syntax" rule)
  - `q.AtCompileTimeFor[T, R any](xs []T, fn func(T) R) []R` — fold-time map; might be cute but `q.Map` over a literal slice already constant-folds in the Go compiler if the fn is small

  **What "preprocessor time" means here.** The preprocessor would need to actually *execute* fn at toolexec time. Three escalating implementation paths:

  1. **`go run` the closure in a hermetic sub-process.** Synthesize a tiny program that calls fn and prints its result via `fmt.Sprintf("%#v", v)`; capture stdout, splice as a literal at the call site. Pros: works for any pure-Go fn the user wrote; Go's own toolchain handles imports and types. Cons: spawns a subprocess per call site (slow); the synthesized program needs to import the user's module + dependencies (build cost); fn must be self-contained (no captures from the surrounding closure).
  2. **Go-AST interpreter (constexpr-style).** Walk fn's body via `go/ast` + `go/types` and interpret a small whitelist of operations (arithmetic, string ops, slice/map literals, function calls into a stdlib whitelist). No subprocess; fast; restricted to "pure constexpr" fns. Inspired by Rust's `const fn` rules. Pros: fast, no IO; Cons: implementing the interpreter is a lift, scope creep risk.
  3. **Hybrid:** start with stdlib-whitelist interpretation for cheap calls, fall back to `go run` subprocess for anything else. Diagnostic when the subprocess fails.

  Lean: **start with path 1 (`go run` subprocess)** and document the cost. Most call sites won't trip the slow path often, and it makes the feature genuinely powerful. Optimize via path 2 later if subprocess overhead bites.

  **Constraints on fn (any path).**
  - Must be a function literal (not a named function reference) so the rewriter can extract the body.
  - No free-variable captures from the enclosing scope (the subprocess can't see them; the interpreter can't bind them).
  - Result type R must be representable as a Go literal — primitives, arrays, slices, maps, structs, pointers (rejected), channels (rejected), funcs (rejected).
  - Pure: no I/O, no time queries, no random — anything that returns different values across runs is a footgun in compile-time evaluation. (Hard to enforce without sandboxing the subprocess; document the rule and optionally enforce a `GOFLAGS=-trimpath` + restricted `GOENV` to make it deterministic.)

  **Use cases that motivate it:**
  - Precompute lookup tables: `var sinTable = q.AtCompileTime(buildSinTable)` — no init() goroutine, no first-call cost
  - Encode build metadata: `var commit = q.AtCompileTime(func() string { return git.HeadHash() })` (assuming the fn shells out to git via os/exec, which the subprocess path supports)
  - Inline expensive constants: `var maxFib = q.AtCompileTime(func() uint64 { return fib(45) })`
  - Parse-and-validate static config: `var schema = q.AtCompileTime(parseSchemaFile)`

  **Why parked vs. shipped.** It's the natural cap-stone of the macro toolkit — q.AtCompileTime is the universal escape hatch every other macro is a special case of (q.F is `q.AtCompileTime(buildFmtCall)`, q.Snake is `q.AtCompileTime(toSnake)`, etc.). But the implementation is genuinely involved (subprocess discipline, subprocess error handling, literal-printer for arbitrary R, cache key sensitivity). Park until the existing helpers settle; visit when somebody has a concrete use case the others don't cover.

- [ ] **#83 — Resource-escape detection for `q.Open` (and longer-term ARC).** A `q.Open(...).Release(cleanup)` value is alive *only* until the enclosing function returns — the rewriter registers `defer cleanup(v)` at that scope. Letting the value escape that scope (returning it, handing it to a goroutine that outlives the function, storing it in a field) is a use-after-close in waiting. Detect at compile time and surface a diagnostic.

  **Detect-and-reject phase (small, well-scoped).** The typecheck pass already knows every `q.Open` call site and which local binding holds the resource. Walk the function body for misuses:

  1. **`return v` where v is a Release-bound resource.** Always a bug — the moment the function returns, `defer cleanup(v)` fires, so the caller gets a closed resource. Diagnostic: *"q.Open(...).Release-bound value escapes via return; use .NoRelease() (caller takes ownership) or scope the work inside this function."*
  2. **`go fn(v)` / spawning a goroutine that captures v.** Goroutine-lifetime > function-lifetime → `cleanup(v)` fires while the goroutine still holds it. Diagnostic: *"q.Open(...).Release-bound value passed to goroutine that may outlive its scope; use .NoRelease() and manage cleanup explicitly, or join the goroutine before return."*
  3. **`field = v` where field is on a struct receiver / global.** Same flavour — the field outlives the function. Same diagnostic shape.
  4. **`channel <- v`** — value escapes through the channel; receiver may use it after defer fires. Diagnostic.

  Edge case to handle gracefully: passing v to a *blocking* function call that doesn't escape — `process(v)` is fine, the call completes before defer runs. Detection is local-flow + escape-analysis-lite; for v3 we could lean on `go/ssa`'s actual escape analysis, but a syntactic over-approximation is enough for v1. Document the false-positive frontier (any function call could in principle stash v in a global; we only flag goroutine spawns + return + field-store + channel-send).

  Build-out path:
  - Add a per-`qSubCall` field `ReleaseBound bool`.
  - In typecheck, walk each enclosing function's AST after the q.Open is bound; collect every reference site (`info.Uses[ident]` matching the binding) and flag escape patterns.
  - Diagnostic carries `file:line:col` of the offending site, plus the originating Open's location.
  - Negative fixtures under `internal/preprocessor/testdata/cases/open_escape_*_rejected/`.

  **Long-term phase: ARC for non-RAM resources.** "Last-usage-site closes" is what Rust gets through linear types and what Swift / Objective-C get through reference counting. q's seed for this is already half-built: `q.Open` is the resource constructor, `Release` is the destructor, and the rewriter knows where every resource binding flows.

  The ambitious version: track every reference site of an Opened resource (including escapes) and, when escape is OK (e.g. handed to a goroutine that joins), insert refcount inc/dec at each ownership-transfer point so the resource closes at the *actual* last usage rather than the function boundary. Concretely:

  - Wrap each Open value in a generated `qRC[T]{value T; rc *atomic.Int32; cleanup func(T)}` shim.
  - At each transfer site (return, channel send, field store, goroutine spawn), emit `rc.Add(1)`. At each scope exit, emit `if rc.Add(-1) == 0 { cleanup(value) }`.
  - The shim is invisible to user code — q.Open's existing surface (`Release`, `NoRelease`) stays — but the deferred cleanup becomes "decrement, free if last" instead of unconditional close.

  This is a big lift: needs flow analysis (or a heavy hand: rewrite every reference into shim-method calls), needs to interop with raw resource access (sometimes you do want the underlying *Conn), and adds a real per-call atomic. Probably not worth it for the 90% case (functions that own their own resources). But it's the natural endgame if escape patterns turn out to be common.

  **Detection-phase priority: ship before ARC.** The detect-and-reject pass alone closes the practical safety hole — most cases that would leak are caught with no runtime cost. ARC is a maybe-someday if profiles or user reports show resource ownership crosses function boundaries often enough that the diagnostics become annoying.

- [ ] **#84 — `q.Assemble[T](recipes...)` — compile-time dependency-injection graph.** ZIO `ZLayer` / Scala-Cats `MakeFromInOut` / google/wire for Go, but resolved by q's preprocessor at the call site — no codegen step, no runtime reflection, no manual ordering. The user supplies a bag of recipes (functions producing a value, possibly with deps); q topologically sorts them and emits a flat sequence of calls that build the requested target.

  **Surface (working draft):**

  ```go
  // Recipes are just functions whose inputs are required deps and whose
  // output is what they construct. Optional second return: error.
  func newConfig() *Config              { ... }
  func newDB(c *Config) *DB             { ... }
  func newServer(db *DB, c *Config) (*Server, error) { ... }

  // Build a *Server. q.Assemble figures out the order: Config first
  // (no deps), then DB (needs Config), then Server (needs DB + Config).
  server := q.Assemble[*Server](newConfig, newDB, newServer)
  ```

  **What it folds to.** The rewriter walks the recipe set, builds a dep graph keyed by output type, topologically sorts, and emits the equivalent of:

  ```go
  server := func() *Server {
      _config := newConfig()
      _db := newDB(_config)
      _server, _err := newServer(_db, _config)
      // Whether to bubble _err depends on where q.Assemble appears
      // (return / assign / discard) — same logic as q.Try.
      return _server
  }()
  ```

  Errors short-circuit using the existing q.Try machinery: any erroring recipe bubbles via the enclosing function's error return. Non-erroring recipes inline directly. Same five forms as the rest of q (define, assign, discard, return, hoist).

  **Compile-time guarantees:**
  - **Missing dep** — every recipe's input type must be produced by some other recipe (or supplied as a literal value mixed in with the recipes — `q.Assemble[*Server](newDB, newServer, &cfg)` works the same as a `func() *Config { return &cfg }` recipe). If not, compile-time diagnostic naming the type and the recipe that wanted it.
  - **Duplicate provider** — two recipes producing the same type → diagnostic.
  - **Cycle** — A needs B which needs A → diagnostic.
  - **Unused recipe** — recipe in the bag but its output isn't reachable from T → diagnostic. (Strict mode; could relax behind a flag if it's annoying in practice.)
  - **Unsatisfiable T** — no recipe produces T → diagnostic.

  All of these become regular Go test failures at build time — the same ergonomics as misspelling a const.

  **Variants worth considering:**
  - `q.AssembleErr[T](recipes...) (T, error)` — explicit `(T, error)` shape; pairs with q.Try on the call site (`server := q.Try(q.AssembleErr[*Server](...))`). Probably what we ship as primary.
  - `q.AssembleE[T](recipes...) ErrResult[T]` — chain variant, mirrors the q.TryE / q.AwaitE pattern. Composes with `.Wrap("startup")`.
  - `q.AssembleAll[T](recipes...) []T` — when multiple recipes legitimately produce the same type and the caller wants them all (plugin / handler registration).
  - **Resource-managed recipes** — a recipe that returns `q.OpenResult[T]` instead of `T` registers its cleanup via the existing `q.Open` machinery. The graph teardown is reverse-topo, automatic. This is the ZIO `ZLayer.scoped` overlap.

  **Type-resolution mechanism (preprocessor work).**

  1. Scanner recognises `q.Assemble[T](r1, r2, ...)`. Extract T from the index expression; capture the recipe expressions.
  2. Typecheck pass uses `go/types` to resolve each recipe's `*types.Signature`: input types are deps, output type is what it provides. Non-function recipe args are treated as constant providers (their type IS the provided type).
  3. Build a directed graph: edges from each recipe's input types to its output type. Run Kahn's algorithm to topo-sort. Detect cycles + missing deps + duplicates inline.
  4. Emit the flat sequence of calls in topo order, with `_qDepN` temps named after the type's basename (with disambiguation for collisions).

  **Tradeoffs vs. google/wire / uber/fx / samber/do:**
  - **vs. wire:** wire generates a separate file via codegen step; q.Assemble is inline at the call site, no separate `wire.go` to keep in sync. Same compile-time guarantees.
  - **vs. uber/fx:** fx resolves at runtime via reflection — slower startup, errors at runtime. q.Assemble errors at build time. fx supports lifecycle hooks; q.Assemble piggy-backs on q.Open for that.
  - **vs. samber/do:** also runtime, also reflection-based. Same comparison as fx.

  **Why this fits q's mission.** "Stop reaching for codegen tools" is already the through-line of q.Gen* and q.AtCompileTime. q.Assemble takes the next step: stop reaching for DI containers entirely. The recipes are plain Go functions; the orchestration is a one-liner. ZIO showed this pattern is powerful in a typed language — q can make it idiomatic in Go without the monad tax.

  **Implementation order.** Big lift. Realistically lands after #82 (q.AtCompileTime) so we can lean on its preprocessor-time evaluation primitives — though strictly speaking q.Assemble's resolution is type-only and doesn't need to *execute* anything at preprocess time, just topo-sort. Could ship as a standalone pass earlier.

- [ ] **#85 — Coroutines (`q.Coro` / `q.Yield` / `q.Resume`).** Go has goroutines (concurrency, separate stacks) and Go 1.23 has `iter.Seq` (pull-based iteration). It does not have full coroutines: bidirectional, suspendable functions you can pass values into and out of cooperatively. q's preprocessor opens the door — we could rewrite a function with `q.Yield` points into a state machine, just as C# / Kotlin do for `async`/`await` and `yield`.

  **Three escalating tiers, pick what earns its keep:**

  **Tier 1 — `iter.Seq` sugar.** Smallest. A helper that takes a body using `q.Yield(v)` and produces a stdlib `iter.Seq[T]`:

  ```go
  // Today:
  fibs := func(yield func(int) bool) {
      a, b := 0, 1
      for { if !yield(a) { return }; a, b = b, a+b }
  }

  // With q:
  fibs := q.Generator(func() {
      a, b := 0, 1
      for { q.Yield(a); a, b = b, a+b }
  })
  ```

  The rewriter wraps the body, threads the yield func through, and rewrites `q.Yield(v)` to `if !yield(v) { return }`. Result is a plain `iter.Seq[int]` — interop is free. Entirely sugar over Go 1.23's existing pull mechanism.

  **Tier 2 — Bidirectional coroutines.** Like Lua / Python generators with `.send(v)`. Caller passes a value INTO the coroutine on each resume; the coroutine sees that value at its next `q.Yield(out)` call. Implementation: synchronous goroutine + two channels (in / out), with `Resume(v)` blocking until the coroutine yields again. Looks like:

  ```go
  pingPong := q.Coro(func(in <-chan int, out chan<- int) {
      for v := range in {
          out <- v * 2
      }
  })
  reply := pingPong.Resume(21) // 42
  ```

  Implementation is goroutine + channels — no preprocessor work needed. Pure runtime helper. Real value is the API shape: Resume/Yield reads cleaner than channel ping-pong code does in production. Plus q.Resume could integrate with q.Try for fallible coroutines.

  **Tier 3 — Stackless coroutines (preprocessor-rewritten state machines).** The ambitious version. The preprocessor analyzes a function containing `q.Yield(v)` calls, identifies yield points as state-machine transitions, and rewrites the entire function into a state machine struct with a `Resume(input) (output, done)` method. No goroutine. No channel. Just a struct holding the saved local variables and a `state int` field.

  Pros: zero goroutine overhead; faster than tier 2 for tight loops (no channel send/receive on each yield).

  Cons: this is THE hard problem. Closures over local variables need to lift to struct fields. Defer / recover semantics get weird (where does a deferred call go in a state machine?). Loops that span yield points need careful state tracking. C#'s async-rewriter took years to get right, and Go's syntax is more permissive (defer, goroutine spawning, panic/recover all interact with control flow).

  Realistic scope: tier 3 might be too big for q. Tier 1 + tier 2 cover 80% of the real ergonomic wins. Park tier 3 unless someone has a specific tight-loop workload that justifies the lift.

  **Why this fits q's mission.** "Things Go didn't ship" — coroutines are exactly that. iter.Seq came in 1.23 and is half the picture; q can complete the other half. Plus q.Yield reads better than the current `yield func(T) bool` callback dance, which most users find awkward the first 5 times they write it.

  **Speculation: integration with q.Try.** A coroutine that bubbles via Try would be neat: `q.Try(coro.Resume(v))` if coro yields `(T, error)` shaped values. Would require both q.Try AND coro to know about each other's surface, but the preprocessor controls both, so this composes naturally.

### Future / parking lot

- [ ] **#11 — `q.<X>` for is-nil-as-failure / comma-ok / etc.** Umbrella ticket — superseded by #20, #24. Keep open as the catch-all for any additional bubble triggers that surface later (e.g. `q.IfNil(x)` for error-less nil checks that don't want to spell `q.NotNilE(…).Err(ErrSomething)`).

- [ ] **#67 — Goroutine-local storage with auto-cleanup on goroutine death.** `q.GoroutineLocalStorage() map[any]any` keyed by `q.GoroutineID()`, with entries removed automatically when the owning goroutine exits. The motivating use case is Java-`ThreadLocal`-style "set once, fire-and-forget" storage that doesn't leak across long-running programs that have spawned and reaped many transient goroutines.

  **Why parked.** A working end-to-end prototype was built in this session and reverted before merge — see commit history around `cae6ca7`. The mechanism works, but a few design choices need to settle before it ships.

  **Mechanism (the proven part).** Two pieces are injected into the stdlib `runtime` compile by q's preprocessor:

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

  - **Test for the cleanup invariant.** The fixture spawns N goroutines that each touch GLS, joins them, then polls `GLSEntryCount()` until it returns to baseline. Works in practice; relies on `runtime.Gosched()` + brief sleep to give `goexit0` a chance to run for every dead goroutine. Reliable enough for a fixture; would feel slightly flaky at scale.

  - **Patching runtime is a real escalation.** We were previously *appending* one file to runtime's compile. With GLS we'd also be *modifying* an existing runtime file (`proc.go`). That moves us from "runtime sees one extra file" to "runtime sees one of its own files rewritten". The AST-based patch is robust to comment / whitespace changes but breaks if Go renames `goexit0` (rare; it's load-bearing for the scheduler) or restructures it enough that "prepend at body top" is no longer a safe insertion point.

  - **Cross-Go-version stability.** Currently tested only on Go 1.26.2. Goexit0's signature has been `func goexit0(gp *g)` for years, but a bigger rework (e.g., merge with `gdestroy`) would invalidate the parameter-extraction path. Mitigation: when the patcher fails to find `goexit0`, fall back to omitting the cleanup hook — the GLS still works, just leaks. Behaviour-degraded rather than build-broken.

  **Resume steps when picking this up.**

  1. Pick the concurrency primitive (recommended: `runtime.mutex`).
  2. Re-add `pkg/q/gls.go` + the GLS pieces in `runtimestub.go` (the `patchProcGoExit0` function from the prototype is in the conversation log around 2026-04-25).
  3. Add `GoroutineLocalStorage` and `GLSEntryCount` to `qRuntimeHelpers` in `scanner.go`.
  4. Restore the `goroutine_local_storage_run_ok` fixture.
  5. Document in README + dedicated docs page (model after `docs/api/goroutine_id.md`); call out the proc.go-patch escalation explicitly.

- [ ] **#16 — Multi-LHS from a single q.\*** (deferred). `v, w := q.Try(call())` where we'd want q.Try to split a multi-result producer. Requires new runtime helpers `q.Try2[T1, T2]` / `q.Try3` and matching rewrite templates. The hoist infrastructure already handles *incidental* multi-LHS (where the RHS call itself returns multi, and a q.* is nested in its args — see `multiLHS` in the hoist fixture). This parking-lot item is strictly the shape where q.* IS the multi-result producer; deprioritised in favour of #15 / #17.

## Done

A short ledger of what's shipped — newest first. Look at `git log` for the full story.

- **#81 — Parallel data ops (q.ParMap / q.ParMapErr / q.ParFlatMap / q.ParFlatMapErr / q.ParFilter / q.ParFilterErr / q.ParEach / q.ParEachErr).** Bounded-concurrency variants of #80's data ops. Default worker count `runtime.NumCPU()`; configurable via `q.WithPar(ctx, n)` — limit travels on `context.Context`, NOT functional options or a builder. ctx-carried-limit was a deliberate departure from samber/lo PR #858 (functional options) and github.com/GiGurra/party (builder pattern); ctx-carried propagates through call graphs without re-threading and matches q's house style for ctx-aware helpers (`q.RecvCtx`, `q.AwaitCtx`, etc). All Par* take ctx as first arg; user fn in `…Err` variants takes ctx for early-exit. Bare versions IGNORE ctx cancellation (no error path to bubble it through); `…Err` versions honour cancellation and produce `ctx.Err()`. First-error wins via 1-buffered errCh + non-blocking send (no atomics needed); pattern from samber/lo PR #858. Two-phase select in dispatcher: priority check on errCh / ctx.Done before competing with work-channel send. Fixture `par_run_ok` covers ctx helpers (default = NumCPU, WithPar/WithParUnbounded round-trip, non-positive fallback), concurrency-cap-honoured (atomic max-active under WithPar(3)), composes with q.Try / q.TryE.Wrap, ctx-cancel returns context.Canceled, ParEachErr first-error-wins (limit=1 + slice-ends-at-erroring-element makes seen-before-err deterministic), ParMap unbounded all-spawned. Stable under `-race -count=5`.

- **#80 — Functional data ops (q.Map / q.FlatMap / q.Filter / q.GroupBy / q.Exists / q.ForAll / q.Find / q.Fold / q.Reduce / q.Distinct / q.Partition / q.Chunk / q.Count / q.Take / q.Drop, plus their `…Err` variants).** Scala / samber/lo-style data manipulation over slices. Pure runtime helpers — no preprocessor rewriting. Each fallible op ships in two flavours: bare and `…Err` returning `(result, error)`. Designed to compose with q.Try / q.TryE: `q.Try(q.MapErr(rows, parseRow))` and `q.TryE(q.MapErr(...)).Wrap("ctx")` work without a separate `…E` chain flavour. q.Find returns (T, bool) for q.Ok / q.OkE composition. Fold = explicit-init Scala foldLeft (R may differ from T). Reduce = no-init T-only — returns Go zero on empty (sound when fn is monoidal — `fn(zero, x) == x` — silent footgun otherwise; reach for q.Fold with explicit identity for max/min/multiply); single-element slice returns the element unchanged. Iterator (iter.Seq) variants deferred. Fixture `data_run_ok` exercises every helper plus composition with the bubble family; q.Exists delegates to slices.ContainsFunc.

- **golangci-lint fix: phantom type params on MatchCase.** v2.11 flagged `value V` and `result R` fields as unused. Replaced with `_ [0]V` / `_ [0]R` zero-size phantom-type-parameter pattern — flows V/R through the type-checker, no storage, no warnings. Commit `6203ec7`.

- **#70 — q.Match / q.Case / q.CaseFn / q.Default / q.DefaultFn (value-returning switch).** Rewrites to an IIFE-wrapped switch in expression position. The typecheck pass populates `EnumTypeText` (V's type) and `ResolvedString` (R's type) via `types.TypeString` with a same-package-unqualified qualifier, so the IIFE compiles cleanly. When V is an enum (defined named type with declared constants in the package) AND no default arm is present, coverage is enforced (mirrors `q.Exhaustive`). The scanner's `parseMatchArms` walks q.Match's tail args; each must be a q.Case / q.CaseFn / q.Default / q.DefaultFn call. q.Case / q.CaseFn / q.Default / q.DefaultFn classify as ok=false at the regular path (they have no meaning standalone — runtime panic stub fires if reached). **Lazy arms via q.CaseFn / q.DefaultFn** — result is `func() R` instead of `R`; the rewriter emits `case <val>: return (<fn>)()` so only the matching arm's func runs (verified by the side-effect-counter test). The typecheck unwraps the lazy arm's `*types.Signature` to recover R for the IIFE return type. Fixtures: `match_run_ok` (enum + non-enum + struct results + Default arm + CaseFn / DefaultFn with side-effect verification), `match_missing_rejected` (negative coverage). Result-type spelling handles same-package types unqualified (Coords vs main.Coords) so the rewritten IIFE compiles without surprise.

- **#71 — Compile-time reflection (q.Fields / q.AllFields / q.TypeName / q.Tag).** Each call site folds to a literal at compile time. Fields / AllFields list a struct's exported / all field names; TypeName produces the defined-type name as a string; Tag looks up a struct-tag value by field+key (both literal args). Pointer indirection follows for the struct-shaped helpers. Tag uses an embedded `reflect.StructTag.Get`-equivalent parser inside `typecheck.go` (`reflectStructTag`) so the rewriter doesn't pull `reflect` as a dep. Field-not-found and non-struct-T surface diagnostics. Cross-package T works fine (the result is just a literal). Fixture `reflection_run_ok` covers Fields/AllFields/Tag round-trip + composition with `q.PgSQL` to build a SELECT statement from struct metadata.

- **#68 (second wave) — q.GenStringer / q.GenEnumJSONStrict / q.GenEnumJSONLax (method generators).** Package-level directives written as `var _ = q.GenX[T]()` synthesize companion methods on T. The scanner detects the top-level shape via a new `scanTopLevelVarSpec`; the typecheck pass populates `EnumConsts`/`EnumConstValues`/`EnumUnderlyingKind` (resolveEnum extended); the rewriter substitutes each directive call with `q.GenMarker{}`. A new file-synthesis pass in `internal/preprocessor/gen.go` collects all Gen directives across the package, dedupes by (family, type), and emits a single `_q_gen.go` companion file to `$TMPDIR` with all requested methods. Strict pairs with `q.Exhaustive` (every parsed value is a declared constant). Lax pairs with `q.Exhaustive`'s `default:` arm (preserves unknown wire values). Fixture `gen_directives_run_ok`. Closes the forward-compat-JSON loop.

- **#71 (in-session) — Scan q.* in container-statement headers.** In-place q.* calls now work inside `IfStmt.Init`/`Cond`, `ForStmt.Init`/`Cond`/`Post`, `RangeStmt.X`, and `SwitchStmt.Init`/`Tag`. Common idioms like `for _, c := range q.EnumValues[Color]() { … }` and `if name := q.EnumName[Color](c); name != "" { … }` rewrite cleanly. Bubble-shape q.* in the same positions surfaces a diagnostic asking the user to extract the call to a preceding statement (a multi-line bind+check has no place inside a single-line header). Per-call synthetic ExprStmt wrappers ensure the edit span equals the q.*'s OuterCall — without per-call scoping, a container-level edit would overlap with edits the body's own statements emit. The `//line` directive injection became conditional on the rewrite actually adding lines (newline count comparison) — span-substituting in-place rewrites preserve line counts and need no directive, AND can't tolerate one being injected inside their containing header. Fixtures `container_position_run_ok` (positive) + `container_bubble_rejected` (negative).

- **#73 — Compile-time string-case ops.** `q.Upper` / `q.Lower` / `q.Snake` / `q.Kebab` / `q.Camel` / `q.Pascal` / `q.Title`. Each takes a string literal arg and folds to a string literal at compile time. Tokenisation handles camelCase, PascalCase, kebab-case, snake_case, space/dot/slash separators, and acronym runs (`XMLHttpRequest` → `XML`/`Http`/`Request`). Title is the exception — splits on space only, preserves intra-word case. `splitWords` / `joinWords` helpers in `internal/preprocessor/strings.go` are shared across Snake/Kebab/Camel/Pascal. Fixture `string_case_run_ok`. Drive-by: `TestRewriteFixtureSource_NoExceptions` now skips `_rejected` fixture dirs.

- **q.Exhaustive (compile-time switch coverage).** `switch q.Exhaustive(v) { case … }` — the typecheck pass walks v's defined type for `*types.Const` declarations, walks the SwitchStmt's case clauses (resolving idents via `info.Uses`), and diagnoses any missing constants. Multi-value cases (`case A, B:`) are honoured. **A `default:` clause does NOT replace coverage of declared constants** — it catches values outside the declared set (forward-compat with Lax-JSON-opted types, wire drift, future constants from upstream). Every declared constant still needs its own case. Adding a new constant later still triggers the missing-case diagnostic, even with `default:` present. Cross-package T is rejected. Legal only as the tag of a switch; any other position surfaces a diagnostic. Wrapper stripped at rewrite time → zero runtime cost. Fixtures: `exhaustive_switch_run_ok` + `exhaustive_switch_missing_rejected` + `exhaustive_switch_default_no_substitute_rejected`. Dedicated docs page at `api/exhaustive.md`.

- **General q.* composition: drop self-substitution exclusion + broaden direct-bind check.** Two fixes that together make any combination of q.* expressions compose without per-shape carve-outs. (1) `substituteSpans` no longer excludes exact-match spans — that was preventing legitimate composition like `q.Try(q.EnumParse[Color](s))` where the outer's InnerExpr equals an inner sub's OuterCall span. (2) `hasQRefInSub` now walks every user-supplied expression field — InnerExpr, MethodArgs, OkArgs, ReleaseArg, AsType, RecoverSteps' MatchArg/ValueArg — so the direct-bind path can't green-light a sub with nested q.*s in fields it didn't check. Verified by the enum_helpers fixture's deep composition cases (q.Try → q.EnumParse, q.TryE.Wrapf → q.EnumParse, q.EnumName → q.Try → q.EnumParse, q.EnumName nested in fmt.Sprintf).

- **#77 — q.SQL / q.PgSQL / q.NamedSQL (injection-safe parameterised SQL).** Each call site rewrites to a `q.SQLQuery{Query, Args}` composite literal. `{expr}` placeholders lift out as `?` (SQL), `$N` (PgSQL), or `:nameN` (NamedSQL) driver-appropriate placeholders, with the corresponding Go expressions in `Args []any`. Reuses q.F's `parseFFormat` brace-tracking parser via a `parseSQLFormat` twin. The rewriter physically cannot inline a user value into the Query string — every `{expr}` becomes a placeholder + Args entry, so the parameterised guarantee is structural. Format must be a Go string literal (validated at scan time); allowing a dynamic format would re-open the injection hole the helper exists to close. Fixture `sql_run_ok` covers all three families plus injection-attempt parameterisation, brace escapes, no-placeholder constants, complex expressions, and composition.

- **#69 — q.F / q.Ferr / q.Fln (compile-time string interpolation).** Each call site rewrites to `fmt.Sprintf` / `errors.New` / `fmt.Errorf` / `fmt.Fprintln(q.DebugWriter, …)` with `{expr}` placeholders extracted as positional `%v` args. Format must be a Go string literal (scan-time validated). Brace-escape `{{` / `}}`. Inside placeholders, Go string and rune literals are honoured so braces inside them don't terminate the placeholder. Each placeholder is `parser.ParseExpr`-validated; malformed placeholders abort the build with a diagnostic. `q.Ferr` without placeholders folds to `errors.New` to skip `fmt.Errorf` overhead. `q.Fln` routes through `q.DebugWriter` so fixtures can capture output. Fixture `f_format_run_ok`. Tradeoff documented: identifiers inside the format literal aren't IDE-visible (rename / go-to-def don't see them); compiler still catches typos via the rewritten Sprintf args.

- **#68 — q.Enum* family (helpers for the de-facto Go enum pattern).** Six new helpers: `EnumValues[T]() []T`, `EnumNames[T]() []string`, `EnumName[T](v) string`, `EnumParse[T](s) (T, error)`, `EnumValid[T](v) bool`, `EnumOrdinal[T](v) int`. All rewrite at compile time — literal slices for the zero-arg forms, IIFE-wrapped switches for the value-taking forms. Constants are discovered by the typecheck pass walking T's `*types.Named` declaring package for `*types.Const` of type T (in source declaration order). Same-package T only — cross-package T surfaces a diagnostic. Works for any const-able comparable type (int- and string-backed both). Plus `q.ErrEnumUnknown` sentinel wrapped via `%w` into the `q.EnumParse` bubble. NAME-based parsing semantics (matches the constant identifier, not the underlying value) — pairs cleanly with EnumName as a round-trip. Fixture `enum_helpers_run_ok`.

- **Zero-arg auto form for q.Recover / q.RecoverE.** `defer q.Recover()` / `defer q.RecoverE().<Method>(args)` now auto-wire the `&err` argument from the enclosing function's error return. Scanner adds `familyRecoverAuto` / `familyRecoverEAuto` and a new DeferStmt case in `matchStatement` that recognises zero-arg entry calls. Rewriter body path splices `&<errName>` into the deferred call. New signature-rewrite pass runs per-file, deduped per `*ast.FuncType`, and when the error slot is unnamed it rewrites the entire Results to give every slot a name (Go's all-or-nothing rule) — `_qErr` for the error slot, `_qRet0`/`_qRet1`/… for other unnamed slots. Enforces last return is the builtin `error` (rejects concrete `*MyErr` types since `&err` of a different type would mismatch q.Recover's `*error` parameter). Existing explicit `defer q.Recover(&err)` form continues to work unchanged — scanner only matches the zero-arg shape. Fixture `recover_auto_run_ok` covers: named-err reuse, unnamed single-return signature rewrite, unnamed pair `(int, error)` signature rewrite (with the non-error slot getting `_qRet0`), RecoverE.Map/.Wrap/.Err, multiple deferred auto-Recover calls in one function (signature rewritten once), and a regression guard that explicit `&err` still works.

- **#32 — q.Recover / q.RecoverE (panic→error).** Pure runtime helpers (no preprocessor rewriting — Go's `recover()` sees the panic because the terminal method IS the deferred function). `q.Recover(&err)` wraps any panic in `*q.PanicError` (preserves panic value + `debug.Stack()`). `q.RecoverE(&err)` exposes `.Map(func(any) error)`, `.Err`, `.ErrF(func(*PanicError) error)`, `.Wrap`, `.Wrapf`. `qRuntimeHelpers` extended with `Recover` and `RecoverE`. Fixture `recover_family_run_ok` covers errors.As extraction, stack preservation, Map / Err / Wrap / Wrapf / ErrF on both panic and no-panic paths.

- **#30 — q.Async / q.Await / q.AwaitE.** `q.Async(fn)` spawns a goroutine and returns `Future[T]` via a buffered channel (plain runtime fn; `Async` + `AwaitRaw` are in `qRuntimeHelpers`). `q.Await(f)` rewrites to `Try` shape with `q.AwaitRaw(f)` as the source; `q.AwaitE(f).<method>` rewrites to `TryE` shape with the same inner. Shares the existing ErrResult type so the chain vocabulary is identical to TryE. Fixture `satire_lane_run_ok` covers happy path, err-bubble, .Wrap, .Catch-recover / .Catch-bubble.

- **#27 — q.Lock (Lock + defer Unlock).** Rewrites to `_qLockN := <locker>; _qLockN.Lock(); defer _qLockN.Unlock()`. Accepts any `sync.Locker` — covers `*sync.Mutex`, `*sync.RWMutex`, `rwm.RLocker()`. Statement-only. Same fixture as #25/#26/#28.

- **#26 — q.Require (bubble-on-false), originally landed as q.Assert (panic-on-false).** Rewrites to `if !(<cond>) { return …, errors.New("q.Require failed <file>:<line>[: <msg>]") }`. Statement-only. Optional message via variadic. Subsequently refactored from panic-shaped `q.Assert` into a bubble-shaped `q.Require`: q's purpose is producing errors, not generating panics. Fixture `require_run_ok`. Build-tag compile-out (`-tags=qrelease`) deferred — current implementation always emits the check.

- **#25 — q.TODO / q.Unreachable (panic markers).** Rewrite to `panic("q.TODO <file>:<line>[: <msg>]")` / `"q.Unreachable ..."`. Statement-only, optional message. Same fixture as above. Build-time aggregation (TODO count summary) deferred.

- **#24 — q.Recv / q.RecvE / q.As / q.AsE (Ok-family extensions).** Share the Ok family's check+bubble shape through a new `okBindLineFromInner` helper that accepts a pre-computed inner text. Recv's inner is `<-(<chExpr>)`; As[T]'s inner is `(<xExpr>).(<T>)`. Bubble sentinels: `q.ErrChanClosed`, `q.ErrBadTypeAssert`. `q.As[T]` uses `IndexExpr` detection (via new `isIndexedSelector` helper) to capture the explicit type argument. Scanner handles both bare and chain cases; rewriter reuses `assembleOkBlock` / `assembleOkCatchBlock` through a new `renderOkLikeE` dispatcher. `lhsTextOrUnderscore` got `counter` added to resolve formReturn/formHoist crashes when Catch appears in return position (latent bug in OkE/NotNilE Catch). Fixture `recv_as_family_run_ok` covers 11 assertions across bare, Wrap, Wrapf, Err, Catch, errors.Is sentinel identity on both paths.

- **#22 — q.DebugPrintln + q.DebugSlogAttr (Go's missing dbg!), originally landed as q.Debug.** `q.DebugPrintln(x)` rewrites in-place to `q.DebugPrintlnAt("<file>:<line> <src-text>", x)` where DebugPrintlnAt is a runtime helper. Plain runtime default destination is `os.Stderr`, overridable via package-level `q.DebugWriter io.Writer` — the fixture uses that to capture and normalise output for assertions. Return value is unchanged, so q.DebugPrintln is usable mid-expression: `q.Try(loadUser(q.DebugPrintln(id)))`. Subsequently expanded with `q.DebugSlogAttr(x)`, which rewrites directly to `slog.Any("<file>:<line> <src-text>", x)` — no q runtime helper, expands straight to stdlib `log/slog`; rewriter detects this family at the shape level and injects the `log/slog` import. The rewriter refactor: `substituteSpans` takes per-sub replacement text (`subTexts []string`) instead of always generating `_qTmpN`, so the in-place substitution mechanism ships alongside the bubble family's temp substitutions. Fixtures `debug_run_ok` (DebugPrintln) and `debug_slog_run_ok` (DebugSlogAttr) lock in pass-through semantics + slog.TextHandler integration. Originally named q.Debug; renamed to q.DebugPrintln when q.DebugSlogAttr was added so the print-and-pass-through helper has a name distinct from the slog one.

- **#21 — q.Trace / q.TraceE (compile-time file:line).** Same shape as Try/TryE but the bubble wraps with `fmt.Errorf("<basename>:<line>: ...: %w", err)` using the call-site position captured at rewrite time. Every chain method (`Err`, `ErrF`, `Wrap`, `Wrapf`, `Catch`) composes over the prefix. Prefix is built via a new `tracePrefix(fset, pos)` helper. `Wrapf` round-trips the user's literal through strconv.Unquote/Quote to splice prefix + format + `: %w` safely. Typecheck guard extended to familyTrace/TraceE. Fixture `trace_family_run_ok` normalizes the line number to N and asserts prefix + unwrap chain integrity.

- **Bug fix: findQReference container boundaries.** `findQReference`'s recursive descent was firing false positives on `q.*` calls nested inside `*ast.CaseClause` / `*ast.CommClause` bodies — those containers hold statements directly (no BlockStmt wrap) so the existing BlockStmt guard wasn't enough. New `isContainerStmt` helper skips the unsupported-shape fallback for container stmts; `walkChildBlocks` already descends into their bodies and matches the contents properly. Surfaced by `panic_defer_family_run_ok` putting `q.Unreachable()` in a switch default.

- **#20 — q.Ok / q.OkE (comma-ok bubble).** New family that bubbles `q.ErrNotOk` when `ok` is false. Accepts two call-argument shapes — `q.Ok(v, ok)` (two separate exprs) and `q.Ok(fn())` (single CallExpr returning `(T, bool)`, expanded via Go's f(g()) rule). OkE exposes the standard chain vocabulary mirroring NotNilE: no captured source error on the not-ok branch, so `Wrap` emits `errors.New` and `Wrapf` emits `fmt.Errorf` without `%w`. Rewriter adds `okBindLineAndCheck` (tuple-binds value + `_qOkN`), `assembleOkBlock` (check is `!<okVar>`), and `assembleOkCatchBlock`. Fixture `ok_family_run_ok` asserts 21 lines covering bare × both arg shapes, every chain method on both paths, hoist form, assign + discard forms, and `errors.Is(err, q.ErrNotOk)` sentinel identity.

- **#15 + #17 — q.Check / q.CheckE + q.Open / q.OpenE.** Two new public-surface families landed together:
  - **q.Check** (void, error-only): `q.Check(file.Close())` bubbles non-nil err, otherwise no-op. Always an expression statement — the helper's return type is `()`, so `v := q.Check(...)` is a Go type error. `q.CheckE(...).<Err|ErrF|Wrap|Wrapf|Catch>` supports the same error-shape vocabulary as TryE; Catch's fn signature is `func(error) error` (nil = suppress, non-nil = bubble) since there's no value to recover.
  - **q.Open** (resource-with-defer): `q.Open(dial()).Release((*Conn).Close)` bubbles on err, otherwise registers `defer cleanup(resource)` in the enclosing function and returns the resource. Release is the terminal method; the chain variant `q.OpenE(...)` exposes Err/ErrF/Wrap/Wrapf/Catch as intermediate shape methods that return `OpenResultE[T]` so Release can still come last. Scanner recognises the chain via a new `classifyOpenChain` helper that walks outward from the Release call. Rewriter emits the usual bind+check+bubble block, then a `defer (<cleanup>)(<valueVar>)` line where valueVar is the LHS (define/assign) or a new `_qTmp<N>` temp (discard/return/hoist). Works in every form Try supports, including return-position (`return q.Open(dial()).Release(c), nil`) and nested-in-call (`id := identity(q.Open(dial()).Release(c)).id`). Catch rebinds the valueVar on recovery so the defer fires on the recovered resource, not the failed one. Fixtures `check_run_ok` (16 assertions) and `open_run_ok` (25 assertions — asserts defer-LIFO ordering and Catch-recovery cleanup target).
- **Hoist form: q.\* nested inside any non-return statement.** Scanner's direct-bind path now falls through to `matchHoist` whenever the matched q.* has nested q.*s in its InnerExpr or MethodArgs, or when the statement shape doesn't fit direct-bind (multi-LHS RHS, non-ident LHS with q.*, q.* as argument to a non-q.* call). `collectQCalls` descends through matched q.*s too so deep nesting is caught. Rewriter orders subs innermost-first, numbers counters in render order, and uses `substituteSpans` to replace immediate-child q.* spans wherever they appear — in each sub-call's InnerExpr/MethodArgs for its bind line, and in the full statement text for the final suffix. Unlocks `v := f(q.Try(call()))`, `a, b := split(q.Try(n()))`, `sink(q.Try(x()))`, `x := q.Try(Foo(q.Try(Bar())))`, `m[key] = q.Try(v())`, `m[q.Try(i())] = v`. Fixture `nested_in_call_run_ok` (17 runtime assertions) + `TestRewriteHoist_*` unit tests cover these shapes.
- **Multi-q in return + renderer refactor.** Scanner now collects every q.* call inside a return-result subtree (via `ast.Inspect`) into a `callShape.Calls []qSubCall`. Per-call fields (Family/Method/MethodArgs/InnerExpr/OuterCall) moved out of callShape into qSubCall so the rewriter can iterate. Rewriter emits one bind+check block per sub-call, then a reconstructed final return with every q.* span substituted by its own `_qTmpN`. Each sub-call has its own early-return, so later q.*s short-circuit on earlier failures — verified by the `multi_q_in_return_run_ok` fixture (14 assertions including a call-counter for short-circuit proof). Unlocks `return q.Try(a()) * q.Try(b()) / q.Try(c()), nil`.
- **#19 — q.* inside closures / anonymous functions.** Scanner now recurses into `*ast.FuncLit` bodies (new `walkFuncLits` helper), and each shape records the signature of its nearest-enclosing function — FuncLit or FuncDecl — via a `EnclosingFuncType *ast.FuncType` field (replaces the old `EnclosingFunc *ast.FuncDecl`). A FuncLit with a different result arity/types than its outer FuncDecl now bubbles to its own results, not the outer's. Fixture `closures_run_ok` covers six shapes: closure in a var, immediately-invoked closure, error-only closure, deferred closure, doubly-nested closures, and a chain-method (`q.TryE(...).Wrap`) inside a closure.
- **#16a — Return-position rewrite.** New `formReturn` form: the scanner walks each return result's AST subtree for q.* calls (anywhere, not just top-level), and the rewriter binds the q.* call to `_qTmp<N>`, emits the usual bubble block, and rebuilds the final return with `_qTmp<N>` spliced into the q.* call's source span. Unlocks `return q.Try(strconv.Atoi(s)) * 2, nil`, `return "tag", q.NotNil(p), nil`, `return q.TryE(call()).Wrap("..."), nil`. Fixture: `return_position_run_ok`. Remaining sub-tasks (nested-in-call outside returns, multi-LHS) are tracked as the reshaped #16.
- **#14 — Audit + extend runtime-behavior coverage.** Three new fixtures verify the runtime behavior: `multi_call_per_func_run_ok` (counter independence + short-circuit so a later q.* call doesn't run when an earlier one bubbles), `error_chain_unwrap_run_ok` (`errors.Is` / `errors.As` traverse `Wrap` / `Wrapf` correctly), `generics_run_ok` (q.Try / q.NotNil work in `func[T any]` and on methods of `Box[T]`). Plus a unit test `TestRewriteFixtureSource_NoExceptions` that AST-walks every fixture's rewritten output and asserts no `q.Try` / `q.TryE` / `q.NotNil` / `q.NotNilE` call survives the rewrite.
- **#18 — Scanner: walk nested blocks (if/for/switch/range/select).** Bug surfaced by the `generics_run_ok` fixture. Scanner now recursively descends into every BlockStmt-bearing statement (if/else, for, range, switch, type-switch, select, case clauses, labelled stmts). `findQReference` is bounded to the leaf statement so container statements don't double-flag a recognised inner shape as "unsupported".
- **#13 — q.Try shape gaps.** Define / assign / discard forms now all work for the Try and NotNil families, plus every chain method. Verified by `forms_assign_discard_run_ok`.
- **#12 — CI + mkdocs site mirroring proven/rewire.** `.github/workflows/ci.yml` (golangci-lint, unit tests, race tests, e2e fixtures, smoke build with -toolexec=q, auto-tagged releases) and `docs.yml` (mkdocs-material → GitHub Pages). Site live at https://gigurra.github.io/q/.
- **#10 — pkg/q public API in Try/TryE/NotNil/NotNilE shape.** Replaced the original NoErr/NoErrf/etc. draft. Surface: 4 funcs + 2 result types + 5 methods each. Bodies are panic stubs so any rewriter miss surfaces loudly at runtime.
- **#9 — gh repo created and initial push.** github.com/GiGurra/q, public, mirrors proven/rewire visibility.
- **#8 — CLAUDE.md, README.md, docs/design.md.** README restructured to mirror proven's pattern (Why+HowToUse on top, `<details>` for everything else).
- **#7 — example/basic + e2e fixture harness.** First fixture, harness builds cmd/q in TestMain and runs each case in its own tempdir.
- **#6 — q.NotNil / q.NotNilE rewriter.** Mirror of Try with `<lhs> == nil` bubble condition. Wrap/Wrapf use `errors.New` / `fmt.Errorf` since there's no source error to %w.
- **#5 — q.Try / q.TryE rewriter.** Bare and chain shapes, all five chain methods, including the Catch recovery skeleton. importcfg-extension pass added to inject `fmt` / `errors` archive paths when the rewriter introduces those imports into a user file that didn't have them.
- **#4 — cmd/q toolexec shim.** Phase 1 stub injection: synthesises a no-op `_q_atCompileTime` companion for pkg/q's compile.
- **#3 — pkg/q public API with linkname gate.** Initial design before the API rename.
- **#2 — q/ scaffold.** Module, LICENSE, directory layout.
- **#1 — proven layout study.** Reference for the toolexec architecture this project mirrors.

## How this list is maintained

- New tasks: created in-session via TaskCreate, then added here in the same commit.
- Closed tasks: moved from "Open" → "Done" in the same commit that ships the work, with a one-line note about what landed.
- Renames / reshapes of an open task: edit the entry in place and note the change in the commit message.

`CLAUDE.md` references this file under "Current implementation state"; that pointer should stay live.
