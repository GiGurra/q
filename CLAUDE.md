# CLAUDE.md

## Project overview

`q` is the question-mark operator for Go, delivered as a `-toolexec` preprocessor. Each `q.Try(call)` / `q.NotNil(p)` / `q.TryE(call).Method(…)` / `q.NotNilE(p).Method(…)` call site is rewritten at compile time into the conventional `if err != nil { return zero, … }` shape — so call sites read flat, but the generated code is identical to hand-written error forwarding with zero runtime overhead.

**Link gate via `_qLink`.** `pkg/q` declares a single bodyless `_qLink()` via `//go:linkname _qLink _q_atCompileTime` and forces its resolution with a package-level `var _ = _qLink`. `gopls`, `go vet`, and IDEs see ordinary Go — completion and analysis stay green. But `go build` / `go test` of any main or test target that imports `pkg/q` refuses to link without the preprocessor: the toolexec pass injects a no-op `_q_atCompileTime` companion file into `pkg/q`'s compile, so with `-toolexec=q` the build resolves and without it the linker fails on the missing symbol. Forgetting the preprocessor is a loud, deterministic error, never a silent loss of rewriting.

**Loud bodies for any rewriter miss.** Every public helper in `pkg/q` (`Try`, `NotNil`, `TryE`, `NotNilE`, plus all chain methods on `ErrResult[T]` / `NilResult[T]`) has a body that calls `panicUnrewritten("q.<name>")` and then returns the zero value. The preprocessor is supposed to rewrite every call site away before the user package is compiled; if any call survives the rewrite (rewriter bug, unsupported pattern), running it panics with a clear diagnostic naming the unrewritten helper. There is no silent fall-through to a "looks correct but discards the error" path.

**Why the bodies aren't bodyless.** Generic functions like `Try[T]` and methods on generic types like `ErrResult[T]` produce one mangled symbol per type instantiation. `//go:linkname` redirects a single local symbol to a single external one, so a fully bodyless generic declaration cannot be link-named to one universal stub the preprocessor supplies. The minimal-body shape (panic + return zero) is the closest equivalent.

## Authoritative docs

- [`README.md`](README.md) — user-facing API tour, install, smallest end-to-end example.
- [`docs/design.md`](docs/design.md) — authoritative design: link-gate mechanism, the four entry helpers, the chain method semantics, and the rewriter's contract.

## Current implementation state

- `pkg/q/q.go` — public surface:
  - `Try[T any](v T, err error) T` — bare error bubble.
  - `NotNil[T any](p *T) *T` — bare nil bubble using sentinel `q.ErrNil`.
  - `TryE[T any](v T, err error) ErrResult[T]` — chain entry for custom error handling.
  - `NotNilE[T any](p *T) NilResult[T]` — chain entry for custom nil handling.
  - `ErrResult[T]` methods: `.Err(error)`, `.ErrF(func(error) error)`, `.Catch(func(error) (T, error))`, `.Wrap(string)`, `.Wrapf(string, ...any)`.
  - `NilResult[T]` methods: `.Err(error)`, `.ErrF(func() error)`, `.Catch(func() (*T, error))`, `.Wrap(string)`, `.Wrapf(string, ...any)`.
  - `ErrNil` sentinel, exposed for `errors.Is` checks against the bare `q.NotNil` bubble.
  - `_qLink` plus `var _ = _qLink` — the package-level link gate.
- `cmd/q/main.go` — toolexec shim, thin wrapper around `internal/preprocessor.Run`.
- `internal/preprocessor/run.go` — toolexec entry: dispatch by tool, plan, forward.
- `internal/preprocessor/compile.go` — per-package dispatch; `Plan` and `Diagnostic` types; argv flag/source helpers.
- `internal/preprocessor/qstub.go` — Phase 1 handler: scans `pkg/q`'s sources for the `//go:linkname` directive, synthesizes a no-op companion file supplying `_q_atCompileTime`, and appends it to `pkg/q`'s compile argv.
- `internal/preprocessor/e2e_test.go` — fixture-based e2e harness mirroring proven's pattern. `TestMain` builds `cmd/q` once into a tempdir, every fixture under `internal/preprocessor/testdata/cases/<name>/` runs in its own tempdir with a synthesized `go.mod` containing a local replace, and the harness asserts on `expected_build.txt` (build outcome) and `expected_run.txt` (runtime stdout, when present).
- `internal/preprocessor/linkgate_test.go` — `TestLinkGateFailsWithoutPreprocessor`: builds a tiny main importing `pkg/q` *without* `-toolexec=q` in an isolated `GOCACHE`, asserts the link fails with a diagnostic naming `_q_atCompileTime`. Locks in the negative half of the gate's contract.
- `internal/preprocessor/testdata/cases/try_bare_link_ok/` — first fixture: a main that uses bare `q.Try`, asserted to build cleanly under `-toolexec=q`.
- `example/basic/basic.go` — small library showing all four entry helpers in idiomatic positions.

**Phase 1 (link gate + stub injection) is implemented and verified end-to-end. Phase 2 (the rewriter that actually transforms `q.*` call sites) is not yet started.** Without Phase 2, any binary that uses `q.*` will link cleanly under `-toolexec=q` but panic at runtime when the helper bodies execute (because no rewriting has erased them). The next pieces:

- **Phase 2a (`q.Try` / `q.TryE` rewriter).** Walk every user-package compile, find `q.Try(call)` and `q.TryE(call).Method(...)` chain expressions, replace each with the inlined `if err != nil { return …, <wrapped> }` shape using the enclosing `FuncDecl`'s return-type list to synthesize zero values.
- **Phase 2b (`q.NotNil` / `q.NotNilE` rewriter).** Mirror of Phase 2a with the bubble condition `p == nil` and the `NilResult` chain methods.
- More fixtures: at minimum one per chain method × source monad, plus a fixture per declined "not yet supported" pattern (so future expansion has a regression target).

## Conventions

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
