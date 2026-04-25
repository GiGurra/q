# TODO

The persistent backlog for `q`. Mirrors the in-session task list so a fresh conversation (or anyone reading the repo cold) can pick up where we left off without re-deriving priorities.

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

- [ ] **#70 — `match` expression.** `q.Match[R](x, q.Case(…), q.Case(…))` rewrites to a switch returning a value. Pairs especially well with type-assertion dispatch.

  **Surface:**
  - `q.Match[R any, V any](v V, cases …MatchCase[V, R]) R` — switch by equality on V, exhaustive-by-default (default branch zero-values R if no case matches; opt in to panic via `q.MatchExhaustive`)
  - `q.Case[V, R](v V, result R) MatchCase[V, R]` / `q.CaseFn[V, R](v V, fn func() R)`
  - `q.MatchType[R, X any](x X, cases …TypeMatchCase[R]) R` — type-switch dispatch
  - `q.OnType[T any, R any](fn func(T) R) TypeMatchCase[R]` — case for type T

  **Go-validity:** generic function calls, all parsed normally. `R` and `V` are explicit type args where inference fails.

  **Rewriter sketch:**
  - Recognise the `q.Match` outer call. Extract each inner `q.Case` / `q.OnType` from the variadic argument list.
  - Emit a `switch` (or `switch x := x.(type)` for the type variant) and, per case, the corresponding body. The match value is captured in a `_qMatchN` temp so it's evaluated once.
  - Exhaustiveness: opt-in via `q.MatchExhaustive` — the rewriter compares the matched type to known enum values (when V is a `q.GenEnum`-decorated type) and diagnoses missing cases.

- [ ] **#71 — Compile-time reflection.** Replace runtime `reflect` for the common "give me the field names / type name / struct tag" cases. All return values are folded to literals at the call site.

  **Surface:**
  - `q.Fields[T]() []string` → literal slice of exported field names (or all, with `q.AllFields[T]`)
  - `q.TypeName[T]() string` → e.g. `"main.User"` or `"github.com/x/y.User"` (qualified)
  - `q.Tag[T](field, key string) string` → struct tag value at compile time, e.g. `q.Tag[User]("Name", "json")` → `"name,omitempty"`
  - `q.Methods[T]() []string` → literal slice of method names defined on T
  - `q.Size[T]() uintptr` → `unsafe.Sizeof((*T)(nil))`-equivalent, but constant-folded

  **Go-validity:** generic calls. The first arg of `q.Tag` is a string literal — diagnose if dynamic.

  **Use case:** zero-cost JSON / CSV / SQL row mappers without `reflect`. The downstream user writes a tiny per-type marshaller that uses these constants for column names and tags; with q, the marshaller compiles to a direct field-access table without runtime introspection.

  **Rewriter sketch:** types pass resolves T, walks its method set / fields, splices a literal `[]string{"a","b","c"}` or `"foo"` at the call site. Inject `unsafe` import for `Size`.

- [ ] **#72 — Named arguments via `q.Call` + `q.Named`.** Proposal #12854 (default arguments) and #29137 (named args) were both rejected. q can offer a workable shape for the named-args half.

  **Surface:**
  - `q.Call(fn, q.Named("timeout", 5*time.Second), q.Named("retries", 3))` — rewrites to a positional call with name → param-position mapping resolved by the rewriter. Arguments not named default to the param's zero value.
  - Default values via signature annotation: not feasible without new syntax. Skip.

  **Go-validity:** `q.Call` is a function call. The first arg is the callee (any func value), subsequent args are `q.Named(name, value)` results. The runtime stub for `q.Call` panics if reached (rewriter must transform it).

  **Rewriter sketch:** types pass resolves the callee's parameter names from `*types.Signature`. For each `q.Named(name, value)` arg, look up the position. Emit `fn(positional1, positional2, …)`.

  **Tradeoff:** doesn't extend to method values where the name is dynamic, doesn't help with overloading. Diagnostics for: name not found in signature, duplicate name, name on a callee whose params are unnamed.

- [ ] **#73 — Compile-time string ops `q.Snake` / `q.Upper` / `q.Lower` / `q.Camel` / `q.Kebab`.** All take `string` literals, all fold to a `string` literal at the call site. Useful for codegen-adjacent helpers (column names, URL paths, env var keys) without runtime cost.

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
