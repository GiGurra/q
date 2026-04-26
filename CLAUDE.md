# CLAUDE.md

## Project overview

`q` is a `-toolexec` preprocessor for Go ("Go wild with Q, the funkiest -toolexec preprocessor"). It originally landed
as the question-mark / `try` operator (the seed proposal Go rejected) and has since grown a playground of helpers Go
didn't ship: ctx cancellation checkpoints, futures and fan-in, panic→error recovery, mutex sugar, runtime preconditions,
compile-time `dbg!`-style prints / slog attrs. Each `q.Try(call)` / `q.NotNil(p)` / `q.TryE(call).Method(…)` /
`q.NotNilE(p).Method(…)` call site is rewritten at compile time into the conventional `if err != nil { return zero, … }`
shape — so call sites read flat, but the generated code is identical to hand-written error forwarding with zero runtime
overhead.

## Authoritative docs

- [`README.md`](README.md) — user-facing API tour, install, smallest end-to-end example.
- [`docs/design.md`](docs/design.md) — authoritative design: link-gate mechanism, the bubble-family entries (Try /
  NotNil / Check / Open and the later additions), the chain method semantics, and the rewriter's contract.
- [`docs/planning/TODO.md`](docs/planning/TODO.md) — **persistent backlog. The resume-point for any new session.**
  Mirrors the in-session task list; updated in the same commit as task creation / completion. If picking up a fresh
  chat, scan this first.
- [`docs/planning/atcompiletime.md`](docs/planning/atcompiletime.md) — design notes for q.AtCompileTime (mechanism,
  edge cases, fixture matrix). Reference when changing that subsystem.
- [`docs/planning/assemble.md`](docs/planning/assemble.md) — Phase 4 plan for q.Assemble (TODO #84, parked).
  Parallel topo-wave construction via `q.WithAssemblyPar`. Self-contained; cold-state implementer can resume
  from this doc alone if Phase 4 gets unparked. Current public surface is documented in `docs/api/assemble.md`.

## No implementation history in docs

**Where history and detailed decisions live:** `git log` (what shipped, when, and why) and `docs/design.md` + the rest of `docs/` (how the system works today, including the design rationale for current decisions). Nowhere else.

**Where they MUST NOT live:** `CLAUDE.md`, `README.md`, `docs/planning/TODO.md`, or any other doc outside `docs/`. These describe the *current* state and *open* design only. Do not write:

- "Shipped this session: …" / "As of commit X: …" / "Status update (date): …" — git log covers this.
- "Done" ledgers, changelogs, or "what we built last sprint" sections.
- `[x]`-marked completed items in TODO.md (delete the entry when it ships).
- "We previously tried X and reverted because Y" prose — git log + commit messages cover the reasoning. The narrow exception is a "considered and dropped" list of *forward-looking* guidance that prevents re-proposing a known-bad shape; keep those entries to one sentence each.
- Rationale for past renames or refactors. The current name is the only name docs need to mention.

When you ship work, **delete** its TODO.md entry rather than moving it to a "Done" section. When you change an API, **rewrite** the doc to describe the new surface; do not leave the old surface alongside it with a "previously" annotation.

## Conventions

- **Golden rule: only accept Go-valid syntax.** The surface q exposes to users must parse and type-check as plain Go —
  what `go build` / `gopls` / the IDE's analyzer sees before the toolexec pass. Never design a feature that relies on
  syntax Go itself would reject (e.g. auto-inferring a trailing `, nil` in a return with fewer values than the signature
  requires, or omitting an argument). That would produce red squiggles in every editor, defeating the whole reason we
  chose a toolexec rewriter over a custom parser: IDE integration. If a proposal requires Go to accept something it
  doesn't, reject the proposal — no matter how ergonomic the post-rewrite code would look.
- The preprocessor's job is narrow: scan bodies for `q.*` call expressions, pattern-match the chain shape (
  `q.TryE(call).Method(args)` is one expression, not two), build the inlined replacement using the enclosing function's
  return-type list, write the rewritten file to a tempdir, and substitute it into the compile's argv. No type-level
  algebra, no SMT.
- **Parse, don't template.** Every pass works from the AST of the source files the Go toolchain handed the compiler (
  `go/parser`, `go/ast`, `go/printer`); shape follows `proven` and `rewire`. Synthesized files go to `$TMPDIR` and join
  the compile argv. The on-disk source tree is never modified. Hardcoded textual templates that duplicate what is
  already in source are a smell — derive from the AST so API evolution in `pkg/q` flows through mechanically.
- Test via the e2e fixture harness, not ad-hoc shell scripts. Each new rewriter pattern gets a fixture under
  `internal/preprocessor/testdata/cases/<name>/`.

## Developing the preprocessor

**Cache discipline.** Go's build cache key does not include the toolexec binary's effect on source. A cached `pkg/q.a`
from a plain `go test` (no stub) and one from a `-toolexec=q` build (with stub) have the same key — whichever ran first
wins. Symptoms:

- `relocation target _q_atCompileTime not defined` — a toolexec build reused a stub-less artifact from an earlier
  non-toolexec run.
- A negative link-gate test (expected to fail without the preprocessor) silently succeeds — a non-toolexec build reused
  a stub-containing artifact.

Two protections in place so this rarely matters:

1. The e2e harness sets `GOCACHE` to a harness-owned tempdir. Tests under `go test ./...` and
   `go test ./internal/preprocessor/` do not cross-contaminate.
2. `TestLinkGateFailsWithoutPreprocessor` allocates *its own* tempdir cache via `runWithCache`, so even if the harness
   cache holds a stub-containing artifact from `TestFixtures` running first, the negative test is hermetic.

Manual toolexec runs against an existing module should keep their own cache:
`GOCACHE=$(mktemp -d) go build -toolexec=q ./...`.

**Rebuild the binary after preprocessor changes.** `go install ./cmd/q` (or whatever build command is used) before
re-running toolexec builds, or the old binary on `$PATH` will run instead. The e2e harness does this automatically in
`TestMain`.

## Keep the docs fresh

After every significant change — a new or renamed API, a new package, a new convention, a design reconsideration, or
deletion of code that held important context — update the persisted docs **in the same commit**. Goal: a cold-state
reader (no conversation memory) can reconstruct the project state and pick up the next task without re-deriving
decisions.

Files to scan after each change:

- `README.md` — if the user-facing surface or smallest example changes.
- `CLAUDE.md` — this file. Conventions.
- `docs/design.md` — if the authoritative design shifts or gains new clauses.
- `docs/api/<feature>.md` — if a feature's surface or semantics change.

If a commit deletes code or infrastructure that held context, the context it captured must move into a persistent doc in
the same commit. That context belongs in `docs/` (where detail is allowed) — see "No implementation history in docs"
above for what stays out of `CLAUDE.md` / `README.md` / `TODO.md`.

## Naming is not frozen

API identifiers, package boundaries, and file layouts here are chosen to read well at their point of use, not to be
permanent. If a better name emerges during implementation or review — clearer reading, less ambiguity, better match to
semantics — rename it. Update every reference (code, tests, docs, README snippets) in the same commit, and note the
prior name and rationale in the commit message so the change is easy to trace.
