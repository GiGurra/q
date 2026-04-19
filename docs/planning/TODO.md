# TODO

The persistent backlog for `q`. Mirrors the in-session task list so a fresh conversation (or anyone reading the repo cold) can pick up where we left off without re-deriving priorities.

**Standing rule (bookkeeping).** When a task lands, move it to the "Done" section *in the same commit* with a one-line note about what shipped (and a commit ref if useful). When a new task is created, add it here *in the same commit* that creates the in-session task. The two views must not drift.

**Standing rule (design).** q only accepts syntax Go itself accepts — see `docs/design.md` §7. Reject any feature that would light up as a type/syntax error in gopls. This kills some nice-reading shapes (auto-inferred `, nil` tails, auto-injected trailing `return nil`, omitted return values) but it's non-negotiable: the IDE experience is the whole reason we're a toolexec rewriter instead of a custom parser.

## Open

### High-impact gaps in the public surface

### Future / parking lot

- [ ] **#11 — `q.<X>` for is-nil-as-failure / comma-ok / etc.** Open question from design discussion: a counterpart helper for cases where the bubble-trigger isn't `(T, error)` or `*T == nil`. Possible shapes: `q.IfNil(x)` — bubble when x is nil; `q.Ok(v, ok)` — bubble when ok is false (comma-ok pattern); `q.Recv(ch)` — bubble on channel close. Get exact semantic from user before designing.

- [ ] **#16 — Multi-LHS from a single q.\*** (deferred). `v, w := q.Try(call())` where we'd want q.Try to split a multi-result producer. Requires new runtime helpers `q.Try2[T1, T2]` / `q.Try3` and matching rewrite templates. The hoist infrastructure already handles *incidental* multi-LHS (where the RHS call itself returns multi, and a q.* is nested in its args — see `multiLHS` in the hoist fixture). This parking-lot item is strictly the shape where q.* IS the multi-result producer; deprioritised in favour of #15 / #17.

## Done

A short ledger of what's shipped — newest first. Look at `git log` for the full story.

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
