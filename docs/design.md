# q — design

This is the authoritative design document for `q`, the question-mark-operator preprocessor for Go. The user-facing tour lives in [`README.md`](../README.md); resume-state and conventions live in [`CLAUDE.md`](../CLAUDE.md). This document records *why* the design is what it is — the constraints that shaped it, the alternatives considered and rejected, and the contract every implementation phase must honour.

## 1. Goals

1. **Flat call sites.** A user writing several `(T, error)`-returning calls in sequence should not have to interleave them with `if err != nil { return …, err }` blocks. The Rust `?` operator and Swift's `try` are the role models.
2. **Zero runtime overhead.** The generated code must be the same as what a careful programmer would write by hand. No closures, no panic/recover, no reflection.
3. **IDE-native.** `gopls`, `go vet`, editors, and language servers must see ordinary Go at all times. No special syntax, no shadow files, no IDE plugins.
4. **Loud failure for misuse.** Forgetting the preprocessor must fail the *build*, not silently produce a working-looking binary that drops errors. Same for any rewriter bug that leaves a `q.*` call site untransformed.
5. **Narrow scope.** `q` is the Rust-`?` analogue, nothing more. It is not a monad library, not a general for-comprehension. The handful of helpers below is the entire surface; future expansions (e.g. comma-ok, channel receive) are deferred decisions, not pre-built parking lots.

## 2. The user-facing surface

### 2.1 Bare bubble

```go
v := q.Try(call)                                    // (T, error) — on err, bubble err
p := q.NotNil(ptr)                                  // (*T)       — on nil, bubble q.ErrNil
     q.Check(errOnlyCall)                           // error      — on err, bubble err (stmt only; void return)
c := q.Open(openCall).Release(cleanup)              // (T, error) + cleanup(T) — bubble on err; defer cleanup(c) on success
```

These are the 90% case. The bubble is unconditional: the source error (or sentinel) is forwarded unchanged to the enclosing function's return. `q.Open` is the one exception where "success" does more than pass the value through — it registers a deferred cleanup in the enclosing function, so the next thing this function's return does (normal or bubble) runs `cleanup(c)`.

### 2.2 Chain — custom error handling

When the call site needs to wrap, transform, or recover from the failure, the `E`-suffixed entries carry the captured value-plus-error into a `Result` value with method options:

```go
v := q.TryE(call).Err(constErr)            // replace the source err with constErr
v := q.TryE(call).ErrF(fn)                 // fn(err) error — transform
v := q.TryE(call).Wrap(msg)                // fmt.Errorf("<msg>: %w", err)
v := q.TryE(call).Wrapf(format, args...)   // fmt.Errorf("<format>: %w", args…, err)
v := q.TryE(call).Catch(fn)                // fn(err) (T, error) — transform OR recover

p := q.NotNilE(ptr).Err(constErr)
p := q.NotNilE(ptr).ErrF(fn)               // fn() error — computed (no source err to pass in)
p := q.NotNilE(ptr).Wrap(msg)              // errors.New(msg) — no %w, no source err
p := q.NotNilE(ptr).Wrapf(format, args...) // fmt.Errorf(format, args...)
p := q.NotNilE(ptr).Catch(fn)              // fn() (*T, error) — computed value OR error

    q.CheckE(err).Err(constErr)            // same vocabulary, void return
    q.CheckE(err).ErrF(fn)
    q.CheckE(err).Wrap(msg)
    q.CheckE(err).Wrapf(format, args...)
    q.CheckE(err).Catch(fn)                // fn(err) error — nil suppresses, non-nil bubbles

c := q.OpenE(openCall).Err(constErr).Release(cleanup)                                  // replace the err, then defer cleanup
c := q.OpenE(openCall).Wrap(msg).Release(cleanup)                                      // wrap the err, then defer cleanup
c := q.OpenE(openCall).Catch(func(e error) (T, error) { ... }).Release(cleanup)        // recover OR bubble, then defer cleanup on whichever T wins
```

`Catch` is the union of "transform the error" and "recover with a fallback value". Returning `(value, nil)` short-circuits the bubble and uses the value; returning `(zero, err)` bubbles `err`. The simpler methods (`Err`, `ErrF`, `Wrap`, `Wrapf`) are sugar for common shapes that `Catch` could express but that read better as named operations. For `q.OpenE`, the recovered value is what the deferred cleanup fires on — not the failed resource.

### 2.3 Constraints that shaped this surface

**Why a chain instead of `q.NoErrf(call(), format, args…)`-style overloads?** Go's spread-multi-return-into-arguments rule fires only when the multi-return call is the *sole* argument. So `q.NoErrf(strconv.Atoi(s), "fmt", x)` is a compile error: once you add format args, you can no longer spread `(T, error)` into the leading two parameters. The chain side-steps this by making the multi-return call the only argument to `q.TryE`, then chaining a method whose receiver has already absorbed the spread. The same constraint is why `q.Open`'s cleanup arrives via a terminal `.Release(cleanup)` method rather than as a second argument to `q.Open(call(), cleanup)`.

**Why one bubble entry per source signature?** Go's type system can't overload — a single `q.Try` can't accept both `(T, error)` and plain `error`. Splitting by source signature is what the language allows, and it makes the call site self-documenting: `q.Check` reads as "the thing I'm calling returns error", `q.Open` reads as "I'm acquiring a resource that needs cleanup". The `E` suffix per family carries the chain variant.

The original four bubble entries cover the dominant Go signatures:

| Source                            | Entry            |
|-----------------------------------|------------------|
| `(T, error)`                      | `Try` / `TryE`   |
| `*T`                              | `NotNil` / `NotNilE` |
| `error` alone                     | `Check` / `CheckE` (void — stmt only) |
| `(T, error)` + cleanup on success | `Open` / `OpenE` |

Subsequent additions follow the same shape: each new helper picks a distinct source signature and exposes a bare + chain pair. `q.Ok` / `q.OkE` for `(T, bool)`, `q.Recv` / `q.RecvE` and `q.As` / `q.AsE` as comma-ok specialisations, `q.Await*` / `q.Recv*Ctx` / `q.Bubble*` for context cancellation and futures, and so on. The bubble shape is the constant; what varies is the *trigger* (error, nil, not-ok, ctx, channel close) that fires it.

**Why terminal `.Release` for Open (not `.WithDefer` earlier in the chain)?** `.Release(cleanup)` has to *own* both the error-bubble path and the success-defer path. Making it a modifier in the middle of the chain would mean another method comes after it — but that method can't undo the defer registration, so Release's placement relative to other chain methods would matter. As the terminal, Release's position is unambiguous: error shaping happens first, defer registration on success is the last step.

**Why different entry verbs for the two source-monads (`Try` vs `NotNil`)?** Forced symmetry — `TryNil` — parses backwards in English. Same reason `Check` and `Open` aren't `TryError` and `TryManage`. The source signatures are genuinely different in shape, so different verbs read more honestly than enforced symmetry.

**Why `.Err` / `.ErrF` / `.Catch` rather than a single variadic / overloaded method?** Each method's signature directly tells the user what is allowed: a constant error, an error transformer, or a value-or-error producer. The preprocessor pattern-matches on method name to pick the right rewrite template — one inlined `if err != nil { return zero, <expr> }` block per method, no runtime dispatch. The `.Wrap` / `.Wrapf` shortcuts are pure ergonomics for the most common case.

## 3. The link gate

`pkg/q` declares a single bodyless function bound via `//go:linkname` to an external symbol that only the `q` preprocessor's toolexec pass supplies:

```go
//go:linkname _qLink _q_atCompileTime
func _qLink()

// Force linker resolution; without this the linker may drop _qLink as unused
// and the gate disengages silently.
var _ = _qLink
```

The single package-level reference is enough to make the linker insist on resolving `_q_atCompileTime`. With `-toolexec=q`, the preprocessor's Phase 1 pass parses `pkg/q`'s sources, finds the `//go:linkname` directive, and synthesizes a companion `.go` file that supplies `_q_atCompileTime` as a no-op — link succeeds. Without the preprocessor, the symbol is undefined and the link fails:

```text
relocation target _q_atCompileTime not defined
```

This is by design: the link failure is the contract that says "you forgot the preprocessor". Both halves of the contract are tested:

- `internal/preprocessor/e2e_test.go` builds fixtures *with* `-toolexec=q` and asserts the build succeeds (and, when the fixture provides `expected_run.txt`, that the runtime stdout matches).
- `internal/preprocessor/linkgate_test.go` builds the same shape *without* `-toolexec=q` in an isolated `GOCACHE` and asserts the link fails with the expected substring.

### 3.1 Why bodies, not bodyless declarations

The natural question: why not declare `Try`, `TryE`, `NotNil`, `NotNilE` (and the chain methods) as bodyless `//go:linkname` declarations directly, with no Go body at all?

Because they are **generic**. `Try[T any]` produces one mangled symbol per type instantiation (`q.Try[int]`, `q.Try[string]`, …); `//go:linkname` redirects a *single* local symbol to a *single* external one. There is no way to spell "every instantiation of `q.Try` links to the same external stub". So the bodies must exist; the question is what they contain.

The chosen body is `panic("q: <name> call site was not rewritten by the preprocessor")` followed by `return <zero>`. The `panic` ensures any rewriter miss surfaces loudly at runtime. The `return <zero>` keeps the function type-correct so the package compiles.

The `panic` message names the helper, so when it fires the user can grep for which `q.*` form caused it and either file a bug, refactor the call site to a supported pattern, or upgrade `q`.

## 4. The rewriter

For each `q.*` call expression in a user-package source file, the preprocessor pattern-matches the expression's full shape and emits a replacement.

### 4.1 Shapes the rewriter recognises

| Source                                                    | Replacement (sketch)                                                               |
|-----------------------------------------------------------|------------------------------------------------------------------------------------|
| `v := q.Try(call())`                                      | `v, __err := call(); if __err != nil { return zero…, __err }`                      |
| `v := q.TryE(call()).Err(E)`                              | `v, __err := call(); if __err != nil { return zero…, E }`                          |
| `v := q.TryE(call()).ErrF(fn)`                            | `v, __err := call(); if __err != nil { return zero…, fn(__err) }`                  |
| `v := q.TryE(call()).Wrap("msg")`                         | `v, __err := call(); if __err != nil { return zero…, fmt.Errorf("%s: %w", "msg", __err) }` |
| `v := q.TryE(call()).Wrapf("fmt", a, b)`                  | `v, __err := call(); if __err != nil { return zero…, fmt.Errorf("fmt: %w", a, b, __err) }` |
| `v := q.TryE(call()).Catch(fn)`                           | `v, __err := call(); if __err != nil { var __new error; v, __new = fn(__err); if __new != nil { return zero…, __new } }` |
| `p := q.NotNil(expr)`                                     | `p := expr; if p == nil { return zero…, q.ErrNil }`                                |
| `p := q.NotNilE(expr).Err(E)` ... (other methods mirrored) | `p := expr; if p == nil { return zero…, E }`                                       |
| `q.Check(call())` (stmt)                                  | `__err := call(); if __err != nil { return zero…, __err }`                          |
| `q.CheckE(call()).Wrap("msg")` (stmt)                     | `__err := call(); if __err != nil { return zero…, fmt.Errorf("%s: %w", "msg", __err) }` |
| `q.CheckE(call()).Catch(fn)` (stmt)                       | `__err := call(); if __err != nil { __new := fn(__err); if __new != nil { return zero…, __new } }` |
| `c := q.Open(call()).Release(cleanup)`                    | `c, __err := call(); if __err != nil { return zero…, __err }; defer (cleanup)(c)`   |
| `c := q.OpenE(call()).Wrap("msg").Release(cleanup)`       | `c, __err := call(); if __err != nil { return zero…, fmt.Errorf("%s: %w", "msg", __err) }; defer (cleanup)(c)` |
| `c := q.OpenE(call()).Catch(fn).Release(cleanup)`         | `c, __err := call(); if __err != nil { var __new error; c, __new = fn(__err); if __new != nil { return zero…, __new } }; defer (cleanup)(c)` |

Zero values come from the enclosing `FuncDecl.Type.Results` or `FuncLit.Type.Results` field (whichever is the nearest-enclosing function scope — closures bubble to their own result list, not the outer FuncDecl's). The rewriter walks the AST, finds the nearest-enclosing function for each call site, and emits an appropriate zero value per result type via `*new(T)`. That form is universal — works for built-ins, user types, pointers, interfaces, and type parameters in generic bodies — and the Go compiler folds it to a constant zero, so the generated machine code is identical to a hand-written zero literal.

### 4.1.1 Statement forms

Every value-producing helper (Try, NotNil, Open and their `E` variants) works in five statement positions:

| Form       | Shape                                  | Notes                                             |
|------------|----------------------------------------|---------------------------------------------------|
| define     | `v := q.Try(call())`                   | LHS is a fresh identifier                         |
| assign     | `v = q.Try(call())`, `m[k] = ...`      | LHS is any addressable expression without nested q.* |
| discard    | `q.Try(call())` (ExprStmt)             | Value is dropped; bubble still fires              |
| return     | `return q.Try(call()), nil`            | q.* anywhere inside any return-result expression  |
| hoist      | `v := f(q.Try(call()))`                | q.* nested inside any non-return expression       |

`q.Check` / `q.CheckE` return void, so they only appear as expression statements. Multiple q.*s per statement compose via the hoist path: `return q.Try(a()) * q.Try(b()), nil`, `x := q.Try(Foo(q.Try(Bar())))`, `m[q.Try(k())] = v`. The rewriter orders nested q.*s innermost-first, allocates `_qTmpN` counters in render order, and rebuilds the final statement with each q.* span substituted by its temp.

### 4.2 What the rewriter must reject

Any `q.*` call that does not match one of the recognized shapes is a hard error: the preprocessor emits a `file:line:col: q: unsupported call shape: <reason>` diagnostic and exits non-zero. The rewriter must never silently leave a `q.*` call in the compiled output (the panic body would then fire at runtime, but that is the *backstop*, not the *contract*).

Examples of explicit rejections:

- `q.Try` outside any function body (e.g. as a package-level initializer).
- A `q.*` call inside a function with no return values (`func f() { q.Try(x()) }`) — the bubble has nowhere to go.
- `q.TryE(call).Method(...)` where `call` is not itself a multi-return `(T, error)` expression — the AST path needs both pieces to type-check the chain.
- A chain method that is not one of the recognized names. (Library evolution requires updating the rewriter and a fixture in the same change.)
- `q.TryE(call).Wrapf(format, args…)` where `format` is not a string literal — the rewriter splices `: %w` into the format, which requires it to be a literal.
- `q.Open(call).Release(cleanup)` missing the `.Release` terminal — the scanner surfaces this as a diagnostic because the resulting `OpenResult[T]` would be a useless intermediate value.

### 4.3 What the rewriter must preserve

- **Source positions for compile errors and debuggers.** When the user package itself has a compile error, the column / line numbers `cmd/compile` reports must point to the user's source, not to the rewritten temp file. Same for DWARF — IDE breakpoints set against the user's path must match what the binary's debug info records. `q` achieves this via `//line` directives: a file-level `//line <user-path>:1` is prepended to each rewritten file, and each per-statement rewrite is followed by a `//line <user-path>:<line-after-stmt>` directive so the extra lines the rewrite injects don't shift subsequent mappings. Debuggers, `go vet`, and the compiler all honour these directives, so DWARF / error messages show the user's path and the line of the original q.* call.
- **Imports.** If the rewriter introduces `fmt` or `errors` calls, it must add the import (deduped against existing imports).
- **Side-effect order.** The replacement must evaluate the inner call exactly once and bind its results before the if-check. Naïve textual substitution that re-evaluated `call()` would change semantics.

### 4.4 Cross-package considerations

For Phase 2, the rewriter operates on each user package's compile in isolation: it needs to know that `q.Try` resolves to `github.com/GiGurra/q/pkg/q.Try`, but it does not need to walk into `pkg/q`'s sources. The chain shapes (`q.TryE(...).Err(...)`) are syntactic — the `pkg/q` import alias is enough to disambiguate.

Cross-package cases (e.g. a user wraps `q.Try` in their own helper) are out of scope. Such helpers will trigger the runtime panic backstop until / unless a future phase adds inlining.

## 5. Phasing

- **Phase 1 — link gate + stub injection.** Done. `pkg/q` link-gates via `_qLink`, `cmd/q` injects the no-op stub into `pkg/q`'s compile. E2e harness verifies both halves of the contract.
- **Phase 2 — rewriter for the Try family.** Done. `q.Try(call)` and `q.TryE(call).Method(args)` across all five statement forms.
- **Phase 3 — rewriter for the NotNil family.** Done. Mirror of Phase 2.
- **Phase 4 — return-position + nested-in-expression.** Done. `return q.Try(call()), nil` and nested q.* inside any expression via the hoist form. Multi-q-per-statement composes (including `q.Try(Foo(q.Try(Bar())))`).
- **Phase 5 — error-only (Check) + resource-with-cleanup (Open).** Done. Adds `q.Check` / `q.CheckE` for functions returning just `error`, and `q.Open` / `q.OpenE` for defer-on-success cleanup.
- **Phase 6 — closures / anonymous functions.** Done. Scanner recurses into `*ast.FuncLit` bodies; each uses its own `FuncType.Results` for the bubble.
- **Phase 7 — typed-nil-interface guard.** Done. `internal/preprocessor/typecheck.go` runs a `go/types` pass over each user-package compile (importer backed by the compile's `-importcfg`) and requires every q.* error-slot type to be exactly the built-in `error` interface. Concrete types that satisfy `error` via method sets (e.g. `*MyErr`) are rejected with a diagnostic naming the offending type. Motivated by Go's implicit concrete-to-interface conversion: a nil `*MyErr` becomes a *non-nil* `error` interface value, so the rewritten `if err != nil` would fire for a notionally-nil error. Canonically mistake [#45 in 100 Go Mistakes](https://100go.co/#returning-a-nil-receiver-45). Ships with `q.ToErr`, a runtime adapter helper that unblocks legitimate `(T, *E)` callees by collapsing typed-nil to literal nil. See [Typed-nil guard](typed-nil-guard.md) for the user-facing spelling.

Future / deferred:

- A counterpart helper for cases where the bubble trigger is neither `(T, error)` nor `*T == nil` nor `error` alone. Possibilities: `q.IfNil(x)` for is-nil-as-failure on an interface or chan; `q.Ok(v, ok)` for the comma-ok pattern; `q.Recv(ch)` for channel close. Exact semantic to be agreed when there's a real motivating use case. Tracked as TODO #11.
- Multi-LHS where q.* itself produces multiple T values (`v, w := q.Try2(call())`) — needs new runtime helpers. Incidental multi-LHS (where q.* is nested inside a multi-result RHS call) already works via hoist. Tracked as TODO #16, parked.
- Optimisations like length-preserving rewrites if the position-drift impact in editors / CI becomes annoying.

## 6. Non-goals

- **No general monad library.** `q` is the Rust-`?` analogue; it is not `for { x <- … }` from Scala. If the project ever needed a wider effect surface, that is a separate project, not a feature of `q`.
- **No type-level guarantees.** The rewriter is purely syntactic. Whether `call()` actually returns `(T, error)` is left to the Go compiler to verify after the rewrite (which it will, because the inlined `v, __err := call()` is what the user would have written).
- **No support for outside the function body.** `q.*` calls in package-level `var` initializers, in struct field tags, etc., are outside scope. The rewriter rejects them with a diagnostic.

## 7. The golden rule: q only accepts Go-valid syntax

Everything q exposes to the user must parse and type-check as plain Go — what `go build` / `gopls` / the IDE's analyzer sees before the toolexec pass ever runs. If a proposed ergonomic improvement would require Go to accept syntax it doesn't, we reject the proposal. No exceptions.

Some shapes that would read nicely but are deliberately rejected:

- **Auto-inferring a trailing `, nil` on a return.** `return q.Try(strconv.Atoi(s)) * 2` inside a `(int, error)` function looks clean, but it is invalid Go: a `return` statement needs as many values as the function signature declares. Every editor would light it up red. We require the user to write the explicit `, nil` tail.
- **Auto-injecting a trailing `return nil` at the end of an `error`-returning function.** Same reason: a function declared to return `error` must end with an explicit return in Go's grammar (or be otherwise unreachable). Synthesising it at preprocess time would hide that requirement from gopls.
- **Omitted return values in multi-return functions.** Any shape where "q fills in the rest" would show as a type error in the editor.

We *could* implement all of the above — the rewriter sees the AST and could emit whatever the compiler accepts. But the value proposition of q is precisely that its user surface is indistinguishable from well-typed Go: completion works, go-to-definition works, refactors work, rename works, type errors point at the right places. The instant we accept non-Go input, we start fighting the tooling on behalf of the user — and we lose the exact reason we chose a toolexec rewriter over a custom parser. Tooling-native > source-density.

Counter-rule: this does NOT constrain what the *rewrite output* looks like. The generated bind + check blocks, `_qTmpN` temporaries, `*new(T)` zero values, etc., live only in temp files the compiler reads and never see an editor. They just need to compile and behave identically to hand-written error forwarding.
