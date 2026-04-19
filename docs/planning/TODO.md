# TODO

The persistent backlog for `q`. Mirrors the in-session task list so a fresh conversation (or anyone reading the repo cold) can pick up where we left off without re-deriving priorities.

**Standing rule.** When a task lands, move it to the "Done" section *in the same commit* with a one-line note about what shipped (and a commit ref if useful). When a new task is created, add it here *in the same commit* that creates the in-session task. The two views must not drift.

## Open

### High-impact gaps in the public surface

- [ ] **#16 — Remaining shape gaps: nested-in-call (non-return), multi-LHS.** Return-position is shipped (see Done); these two are what's left.
  - **nested-in-call** (`f(q.Try(call()))`): hoist the q.* call into a preceding statement that binds to a temp, then rewrite the outer call to reference the temp. Needs careful AST manipulation around statement insertion. More general than the return-position case because we can't build a replacement statement tail — we have to *add* a preceding statement and edit the outer call's sub-expression in place.
  - **multi-LHS** (`v, w := q.Try(call())`): only meaningful if call returns 3+ values, e.g. q.Try3 family. Probably out of scope until we add multi-T support to the runtime helpers.
  - Each shape gets a fixture under `internal/preprocessor/testdata/cases/`.


- [ ] **#15 — Add `q.Check` (or final-named) for error-only returns.** Public-surface gap: `q.Try` requires `(T, error)`. For functions returning only `error` (`file.Close()`, `db.Ping()`, `validate(input)`), the user has to hand-write `if err := f(); err != nil { return ..., err }`.
  - Proposed name: `q.Check(fn())` (Johan suggested `q.TryValidate`; alternatives `Bail` / `OnErr`). Plus chain entry `q.CheckE(fn()).Wrapf` / `.Err` / `.ErrF` / `.Catch`.
  - `Catch`'s fn signature is `func(error) error` (nil = suppress and continue, non-nil = bubble) since there's no value to recover.
  - Pick name with Johan, then mirror the Try family implementation.

- [ ] **#17 — Add `q.TryManage` (defer-on-success cleanup).** Resource-management sugar: open something, register cleanup, propagate errors. Two shapes worth considering:

  ```go
  conn := q.TryManage(openConn(), func(c *Conn) { c.Close() })
  // or via chain:
  conn := q.TryE(openConn()).WithDefer(func(c *Conn) { c.Close() }).Wrap("opening")
  ```

  TryManage is more concise; WithDefer composes with the existing chain methods. Semantics: on err, bubble (no defer registered); on success, register `defer cleanup(conn)` in the enclosing function and return conn. Pick name (TryManage / TryWith / Open / Acquire), then add to scanner+rewriter+fixture.

### Future / parking lot

- [ ] **#11 — `q.<X>` for is-nil-as-failure / comma-ok / etc.** Open question from design discussion: a counterpart helper for cases where the bubble-trigger isn't `(T, error)` or `*T == nil`. Possible shapes: `q.IfNil(x)` — bubble when x is nil; `q.Ok(v, ok)` — bubble when ok is false (comma-ok pattern); `q.Recv(ch)` — bubble on channel close. Get exact semantic from user before designing.

## Done

A short ledger of what's shipped — newest first. Look at `git log` for the full story.

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
