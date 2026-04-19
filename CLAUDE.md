# CLAUDE.md

## Project overview

`q` is the question-mark operator for Go, delivered as a `-toolexec` preprocessor. Each `q.Try(call)` / `q.NotNil(p)` / `q.TryE(call).Method(…)` / `q.NotNilE(p).Method(…)` call site is rewritten at compile time into the conventional `if err != nil { return zero, … }` shape — so call sites read flat, but the generated code is identical to hand-written error forwarding with zero runtime overhead.

**Link gate via `_qLink`.** `pkg/q` declares a single bodyless `_qLink()` via `//go:linkname _qLink _q_atCompileTime` and forces its resolution with a package-level `var _ = _qLink`. `gopls`, `go vet`, and IDEs see ordinary Go — completion and analysis stay green. But `go build` / `go test` of any main or test target that imports `pkg/q` refuses to link without the preprocessor: the toolexec pass injects a no-op `_q_atCompileTime` companion file into `pkg/q`'s compile, so with `-toolexec=q` the build resolves and without it the linker fails on the missing symbol. Forgetting the preprocessor is a loud, deterministic error, never a silent loss of rewriting.

**Loud bodies for any rewriter miss.** Every public helper in `pkg/q` (`Try`, `NotNil`, `TryE`, `NotNilE`, plus all chain methods on `ErrResult[T]` / `NilResult[T]`) has a body that calls `panicUnrewritten("q.<name>")` and then returns the zero value. The preprocessor is supposed to rewrite every call site away before the user package is compiled; if any call survives the rewrite (rewriter bug, unsupported pattern), running it panics with a clear diagnostic naming the unrewritten helper. There is no silent fall-through to a "looks correct but discards the error" path.

**Why the bodies aren't bodyless.** Generic functions like `Try[T]` and methods on generic types like `ErrResult[T]` produce one mangled symbol per type instantiation. `//go:linkname` redirects a single local symbol to a single external one, so a fully bodyless generic declaration cannot be link-named to one universal stub the preprocessor supplies. The minimal-body shape (panic + return zero) is the closest equivalent.

## Authoritative docs

- [`README.md`](README.md) — user-facing API tour, install, smallest end-to-end example.
- [`docs/design.md`](docs/design.md) — authoritative design: link-gate mechanism, the four entry helpers, the chain method semantics, and the rewriter's contract.
- [`docs/planning/TODO.md`](docs/planning/TODO.md) — **persistent backlog. The resume-point for any new session.** Mirrors the in-session task list; updated in the same commit as task creation / completion. If picking up a fresh chat, scan this first.

## Current implementation state

- `pkg/q/q.go` — public surface:
  - `Try[T any](v T, err error) T` — bare error bubble.
  - `NotNil[T any](p *T) *T` — bare nil bubble using sentinel `q.ErrNil`.
  - `TryE[T any](v T, err error) ErrResult[T]` — chain entry for custom error handling.
  - `NotNilE[T any](p *T) NilResult[T]` — chain entry for custom nil handling.
  - `ErrResult[T]` methods: `.Err(error)`, `.ErrF(func(error) error)`, `.Catch(func(error) (T, error))`, `.Wrap(string)`, `.Wrapf(string, ...any)`.
  - `NilResult[T]` methods: `.Err(error)`, `.ErrF(func() error)`, `.Catch(func() (*T, error))`, `.Wrap(string)`, `.Wrapf(string, ...any)`.
  - `ErrNil` sentinel, exposed for `errors.Is` checks against the bare `q.NotNil` bubble.
  - `_qLink` plus `func init() { _qLink() }` — the package-level link gate.
- `cmd/q/main.go` — toolexec shim, thin wrapper around `internal/preprocessor.Run`.
- `internal/preprocessor/run.go` — toolexec entry: dispatch by tool, plan, forward.
- `internal/preprocessor/compile.go` — per-package dispatch; `Plan` and `Diagnostic` types; argv flag/source helpers. Routes `pkg/q` compiles to `planQStub` and every other package to `planUserPackage`.
- `internal/preprocessor/qstub.go` — Phase 1 handler: scans `pkg/q`'s sources for the `//go:linkname` directive, synthesizes a no-op companion file supplying `_q_atCompileTime`, and appends it to `pkg/q`'s compile argv.
- `internal/preprocessor/userpkg.go` — Phase 2 entry: per user-package compile, parses each source, runs the scanner, applies the rewriter, writes rewritten files to a tempdir, substitutes the temp paths into the compile argv. Diagnostics from unsupported `q.*` shapes abort the build.
- `internal/preprocessor/scanner.go` — recognises `q.*` call expressions on the AST. **Matches all four entries × five statement forms:** define (`v := …`), assign (`v = …`, including non-ident LHS like `obj.field`), discard (no-LHS expression statement), return (`return …, q.Try(…), …`), and hoist (`v := f(q.Try(…), q.NotNil(p))` — q.* nested anywhere inside any non-return statement's expressions). Each entry has a bare and chain (`.Err`/`.ErrF`/`.Catch`/`.Wrap`/`.Wrapf`) variant. Resolves the local import alias of `pkg/q` per file. Any unmatched `q.*` reference in a user file becomes a diagnostic. **Per-statement** fields live on `callShape` (Stmt, Form, LHSExpr, EnclosingFuncType); **per-call** fields live on `qSubCall` (Family, Method, MethodArgs, InnerExpr, OuterCall). A callShape owns `Calls []qSubCall` — length 1 for direct-bind forms, 1..N otherwise. `matchStatement` first tries the tight direct-bind path (single LHS, single RHS, RHS IS a q.* call with no nested q.*s); if eligibility fails, `matchHoist` / the return case collect every q.* in the statement's expressions via `collectQCalls` (which descends through matched q.*s too, so nested q.* inside another q.*'s InnerExpr gets caught). Descent stops at `*ast.FuncLit` boundaries (closures are scanned with their own `FuncType` via `walkFuncLits`).
- `internal/preprocessor/rewriter.go` — emits replacement source for one matched statement. `renderShape` orders `sh.Calls` innermost-first via `orderInnermostFirst` (so an outer q.*'s bind line can embed the inner's `_qTmpN`), allocates `_qTmpN` counters in that order so the generated code reads top-to-bottom with increasing N, then renders each sub-call's bind + bubble block and joins them with a newline + indent. `substituteSpans` is the universal "replace immediate-child q.* spans with their temps" primitive: used for each sub-call's InnerExpr text (via `commonRenderInputs` → `exprTextSubst`), for MethodArg text (Wrap/Wrapf/Err/ErrF/Catch args), and for the full-stmt reconstruction in `finalStmtSuffix`. "Immediate child" means contained in the target range but not contained by any other sub also in the range — so a great-grandchild only gets substituted once, by its direct parent's span. Per-family bind line still varies by form (`tryBindLine`, `nilBindLineAndCheck`); formReturn and formHoist both bind to `_qTmp<N>` regardless of family. Try family bubbles on `err != nil`; NotNil bubbles on `<checkVar> == nil`. The discard form binds to a fresh `_qVal<N>` for NotNil (no LHS to reuse) and `_, _qErr<N>` for Try. Bare and most chain methods share family-specific skeletons (`assembleErrBlock` / `assembleNilBlock`); `Catch` has its own assembler in each family because the err / nil branch may either recover (rebind the LHS via fn) or transform (bubble a new error). For discard form, Catch's recovery position becomes `_` since there's nowhere to put a recovered value. NotNil's `Wrap`/`Wrapf` differ from TryE's: there's no source error to %w-wrap, so they emit `errors.New(msg)` / `fmt.Errorf(format, args...)` respectively. Uses the universal `*new(T)` zero-value form. When a render needs `fmt` or `errors`, the rewriter calls `ensureImport` to inject the package into the file's import block (parenthesised form preferred; single-line and zero-import cases handled separately). Always appends a `var _ = <alias>.ErrNil` sentinel so the q import stays alive after rewrites erase the only callers.
- `internal/preprocessor/rewriter_test.go` — unit tests over scan + rewrite: bare-Try basic shape, multi-result, aliased import, no-op when q isn't imported, plus per-method coverage for TryE (`Wrapf` injects fmt, `Catch` produces the recovery shape, `Err` splices the constant error).
- `internal/preprocessor/e2e_test.go` — fixture-based e2e harness mirroring proven's pattern. `TestMain` builds `cmd/q` once into a tempdir, every fixture under `internal/preprocessor/testdata/cases/<name>/` runs in its own tempdir with a synthesized `go.mod` containing a local replace, and the harness asserts on `expected_build.txt` (build outcome) and `expected_run.txt` (runtime stdout, when present).
- `internal/preprocessor/linkgate_test.go` — `TestLinkGateFailsWithoutPreprocessor`: builds a tiny main importing `pkg/q` *without* `-toolexec=q` in an isolated `GOCACHE`, asserts the link fails with a diagnostic naming `_q_atCompileTime`. Locks in the negative half of the gate's contract.
- `internal/preprocessor/testdata/cases/try_bare_link_ok/` — Phase 1 fixture: a main using bare `q.Try`, asserted to build cleanly under `-toolexec=q`.
- `internal/preprocessor/testdata/cases/try_simple_run_ok/` — Phase 2 fixture for bare `q.Try`: runs the binary on both a successful input ("21" → 42) and a failing one ("abc" → propagated `strconv.Atoi` error).
- `internal/preprocessor/testdata/cases/tryE_chain_methods_run_ok/` — Phase 2 fixture for the full TryE chain: one helper function per method (`Err`, `ErrF`, `Wrap`, `Wrapf`, `Catch`-recovery, `Catch`-transform), each invoked on both the success and bubble paths, asserted line-by-line in expected_run.txt.
- `internal/preprocessor/testdata/cases/notnil_family_run_ok/` — Phase 2 fixture for the full NotNil family: bare `q.NotNil` plus every `q.NotNilE` chain method, each invoked on both the pointer-present and pointer-absent paths.
- `internal/preprocessor/testdata/cases/forms_assign_discard_run_ok/` — Phase 2 fixture for the assign and discard forms: `v = q.Try(...)`, `q.Try(...)` discard, `v = q.TryE(...).Wrapf(...)` chain assign, `q.TryE(...).Err(...)` chain discard, plus the NotNil counterparts. 14 expected_run.txt lines locking in every form × family combination.
- `internal/preprocessor/testdata/cases/return_position_run_ok/` — Phase 3 fixture for the return form: `return q.Try(...) * 2, nil` (nested inside an arithmetic expression), `return q.TryE(...).Wrap("..."), nil`, `return "tag", q.NotNil(p), nil` (q.* as the middle of three results), and `return q.NotNilE(p).Err(...), nil`. Eight expected_run.txt lines covering ok+bad for each shape.
- `internal/preprocessor/testdata/cases/multi_q_in_return_run_ok/` — Phase 4 fixture for multiple q.* calls in one return expression: `q.Try(a()) * q.Try(b()) / q.Try(c())` (three Trys in arithmetic), mixed `q.Try + *q.NotNil` (two families in one return), two `q.TryE(...).Wrap(...)` with different messages, plus a counter-based short-circuit check (`calls=1` proves a failing first call skips the second). 14 expected_run.txt lines.
- `internal/preprocessor/testdata/cases/closures_run_ok/` — q.* inside FuncLits: closure in a var, immediately-invoked, error-only, deferred, doubly-nested, and chain-method — all exercise the `EnclosingFuncType` plumbing and `walkFuncLits` recursion.
- `example/basic/basic.go` — small library showing all four entry helpers in idiomatic positions.

**Status: the full public surface is rewritten end-to-end across five statement forms.** Define (`v := ...`), assign (`v = ...`), discard (`q.Try(...)` as expression statement), return (`return q.Try(...), nil`), and hoist (`v := f(q.Try(...))` — q.* nested inside any non-return statement expression) all work for bare `q.Try` / `q.NotNil` and for every `q.TryE` / `q.NotNilE` chain method. Multi-q in one statement is supported (including nested: `x := q.Try(Foo(q.Try(Bar())))`). The rewriter orders nested q.*s innermost-first, substitutes each parent's InnerExpr/MethodArgs with its children's `_qTmpN`, and rebuilds the final stmt with outermost spans substituted. Pending:

- Multi-LHS from a single q.* result (`a, b := q.Try(call())`) — needs `q.Try2` / `q.Try3` multi-T runtime helpers. Tracked in `docs/planning/TODO.md` #16.

### Implementation notes from the rewriter pass

- **Universal `*new(T)` zero values.** Avoids per-type knowledge of the spelling of zero (`0` for ints, `""` for strings, `nil` for pointers, `T{}` for structs, generic-aware variants, etc.). The compiler folds `*new(T)` to a constant zero — generated machine code is identical to a hand-written zero literal.
- **Sentinel reference to keep the q import alive.** Appending `var _ = <alias>.ErrNil` after every rewritten file is cheaper than statically tracking residual usage. Good enough until / unless gofmt-quality output of the temp files becomes interesting.
- **Link-gate construction matters.** The first attempt (`var _ = _qLink` at package level) compiled fine but was dead-code-eliminated by Go's optimiser — the linker then dropped `_qLink` as unreferenced and the gate silently disengaged (build succeeded without `-toolexec=q`). Switched to `func init() { _qLink() }` which calls the function explicitly and survives every optimisation level. `TestLinkGateFailsWithoutPreprocessor` is the regression guard.

## Conventions

- **Golden rule: only accept Go-valid syntax.** The surface q exposes to users must parse and type-check as plain Go — what `go build` / `gopls` / the IDE's analyzer sees before the toolexec pass. Never design a feature that relies on syntax Go itself would reject (e.g. auto-inferring a trailing `, nil` in a return with fewer values than the signature requires, or omitting an argument). That would produce red squiggles in every editor, defeating the whole reason we chose a toolexec rewriter over a custom parser: IDE integration. If a proposal requires Go to accept something it doesn't, reject the proposal — no matter how ergonomic the post-rewrite code would look.
- The preprocessor's job is narrow: scan bodies for `q.*` call expressions, pattern-match the chain shape (`q.TryE(call).Method(args)` is one expression, not two), build the inlined replacement using the enclosing function's return-type list, write the rewritten file to a tempdir, and substitute it into the compile's argv. No type-level algebra, no SMT.
- **Parse, don't template.** Every pass works from the AST of the source files the Go toolchain handed the compiler (`go/parser`, `go/ast`, `go/printer`); shape follows `proven` and `rewire`. Synthesized files go to `$TMPDIR` and join the compile argv. The on-disk source tree is never modified. Hardcoded textual templates that duplicate what is already in source are a smell — derive from the AST so API evolution in `pkg/q` flows through mechanically.
- Test via the e2e fixture harness, not ad-hoc shell scripts. Each new rewriter pattern gets a fixture under `internal/preprocessor/testdata/cases/<name>/`.

## Developing the preprocessor

**Cache discipline.** Go's build cache key does not include the toolexec binary's effect on source. A cached `pkg/q.a` from a plain `go test` (no stub) and one from a `-toolexec=q` build (with stub) have the same key — whichever ran first wins. Symptoms:

- `relocation target _q_atCompileTime not defined` — a toolexec build reused a stub-less artifact from an earlier non-toolexec run.
- A negative link-gate test (expected to fail without the preprocessor) silently succeeds — a non-toolexec build reused a stub-containing artifact.

Two protections in place so this rarely matters:

1. The e2e harness sets `GOCACHE` to a harness-owned tempdir. Tests under `go test ./...` and `go test ./internal/preprocessor/` do not cross-contaminate.
2. `TestLinkGateFailsWithoutPreprocessor` allocates *its own* tempdir cache via `runWithCache`, so even if the harness cache holds a stub-containing artifact from `TestFixtures` running first, the negative test is hermetic.

Manual toolexec runs against an existing module should keep their own cache: `GOCACHE=$(mktemp -d) go build -toolexec=q ./...`.

**Rebuild the binary after preprocessor changes.** `go install ./cmd/q` (or whatever build command is used) before re-running toolexec builds, or the old binary on `$PATH` will run instead. The e2e harness does this automatically in `TestMain`.

## Keep the docs fresh

After every significant change — a new or renamed API, a new package, a new convention, a design reconsideration, or deletion of code that held important context — update the persisted docs **in the same commit**. Goal: a cold-state reader (no conversation memory) can reconstruct the project state and pick up the next task without re-deriving decisions.

Files to scan after each change:

- `README.md` — if the user-facing surface or smallest example changes.
- `CLAUDE.md` — this file. "Current implementation state" list, conventions.
- `docs/design.md` — if the authoritative design shifts or gains new clauses.

If a commit deletes code or infrastructure that held context, the context it captured must move into a persistent doc in the same commit.

## Naming is not frozen

API identifiers, package boundaries, and file layouts here are chosen to read well at their point of use, not to be permanent. If a better name emerges during implementation or review — clearer reading, less ambiguity, better match to semantics — rename it. Update every reference (code, tests, docs, README snippets) in the same commit, and note the prior name and rationale in the commit message so the change is easy to trace.

The current names came out of an explicit naming round (`chain` → `q`, `Wrap` → `CustErr` → `TryE`, `On*` → `Try*` family, `2`-suffix → `Catch`). Future renames are welcome under the same discipline.
