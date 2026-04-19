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
v := q.Try(call)        // call returns (T, error); on err, return zero…, err
p := q.NotNil(ptr)      // ptr is *T;             on nil, return zero…, q.ErrNil
```

These are the 90% case. The bubble is unconditional: the source error (or sentinel) is forwarded unchanged to the enclosing function's return.

### 2.2 Chain — custom error handling

When the call site needs to wrap, transform, or recover from the failure, the chain entries `q.TryE` / `q.NotNilE` carry the captured `(T, error)` or `*T` into a `Result` value with method options:

```go
v := q.TryE(call).Err(constErr)            // replace the source err with constErr
v := q.TryE(call).ErrF(fn)                 // fn(err) error — transform
v := q.TryE(call).Wrap(msg)                // fmt.Errorf("<msg>: %w", err)
v := q.TryE(call).Wrapf(format, args...)   // fmt.Errorf("<format>: %w", args…, err)
v := q.TryE(call).Catch(fn)                // fn(err) (T, error) — transform OR recover

p := q.NotNilE(ptr).Err(constErr)
p := q.NotNilE(ptr).ErrF(fn)               // fn() error — computed
p := q.NotNilE(ptr).Wrap(msg)              // errors.New(msg)
p := q.NotNilE(ptr).Wrapf(format, args...) // fmt.Errorf(format, args...)  — no %w, no source err
p := q.NotNilE(ptr).Catch(fn)              // fn() (*T, error) — computed value OR error
```

`Catch` is the union of "transform the error" and "recover with a fallback value". Returning `(value, nil)` short-circuits the bubble and uses the value; returning `(zero, err)` bubbles `err`. The simpler methods (`Err`, `ErrF`, `Wrap`, `Wrapf`) are sugar for common shapes that `Catch` could express but that read better as named operations.

### 2.3 Constraints that shaped this surface

**Why a chain instead of `q.NoErrf(call(), format, args…)`-style overloads?** Go's spread-multi-return-into-arguments rule fires only when the multi-return call is the *sole* argument. So `q.NoErrf(strconv.Atoi(s), "fmt", x)` is a compile error: once you add format args, you can no longer spread `(T, error)` into the leading two parameters. The chain side-steps this by making the multi-return call the only argument to `q.TryE`, then chaining a method whose receiver has already absorbed the spread.

**Why two distinct entry families per source-monad (`Try` vs `TryE`, `NotNil` vs `NotNilE`)?** The bare form returns `T` directly; the chain form returns a `Result[T]` carrying methods. A single function cannot do both. Splitting by suffix (`E` for "with custom Error handling") keeps the bare path one call long for the common case and makes the chain visible at a glance.

**Why different entry verbs for the two source-monads (`Try` vs `NotNil`)?** Forced symmetry — `TryNil` — parses backwards in English. The two source monads are genuinely different in shape (one carries an error to forward, the other carries no error at all), so different verbs read more honestly than enforced symmetry.

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

## 4. The rewriter (Phase 2 — not yet implemented)

For each `q.*` call expression in a user-package source file, the preprocessor pattern-matches the expression's full shape and emits a replacement.

### 4.1 Shapes the rewriter must recognise

| Source                                                    | Replacement (sketch)                                                               |
|-----------------------------------------------------------|------------------------------------------------------------------------------------|
| `v := q.Try(call())`                                      | `v, __err := call(); if __err != nil { return zero…, __err }`                      |
| `v := q.TryE(call()).Err(E)`                              | `v, __err := call(); if __err != nil { return zero…, E }`                          |
| `v := q.TryE(call()).ErrF(fn)`                            | `v, __err := call(); if __err != nil { return zero…, fn(__err) }`                  |
| `v := q.TryE(call()).Wrap("msg")`                         | `v, __err := call(); if __err != nil { return zero…, fmt.Errorf("msg: %w", __err) }` |
| `v := q.TryE(call()).Wrapf("fmt", a, b)`                  | `v, __err := call(); if __err != nil { return zero…, fmt.Errorf("fmt: %w", a, b, __err) }` |
| `v := q.TryE(call()).Catch(fn)`                           | `v, __err := call(); if __err != nil { var __new error; v, __new = fn(__err); if __new != nil { return zero…, __new } }` |
| `p := q.NotNil(expr)`                                     | `p := expr; if p == nil { return zero…, q.ErrNil }`                                |
| `p := q.NotNilE(expr).Err(E)`                             | `p := expr; if p == nil { return zero…, E }`                                       |
| ... mirrored across the NotNilE methods ...               | ... same shape ...                                                                 |

Zero values come from the enclosing `FuncDecl.Type.Results` field. The rewriter walks the AST, finds the parent function for each call site, and emits an appropriate zero value per result type.

### 4.2 What the rewriter must reject

Any `q.*` call that does not match one of the recognized shapes is a hard error: the preprocessor emits a `file:line:col: q: unsupported call shape: <reason>` diagnostic and exits non-zero. The rewriter must never silently leave a `q.*` call in the compiled output (the panic body would then fire at runtime, but that is the *backstop*, not the *contract*).

Examples of explicit rejections expected in early phases:

- `q.Try` outside any function body (e.g. as a package-level initializer).
- `q.Try` / `q.TryE` inside a function whose last result is not `error`.
- `q.TryE(call).Method(...)` where `call` is not itself a multi-return `(T, error)` expression — the AST path needs both pieces to type-check the chain.
- A chain method that is not one of the recognized names. (Library evolution requires updating the rewriter and a fixture in the same change.)

### 4.3 What the rewriter must preserve

- **Source positions for compile errors.** When the user package itself has a compile error, the column / line numbers `cmd/compile` reports must point to the user's source, not to the rewritten temp file. `proven` solves this with length-preserving rewrites (replacing call spans with same-width whitespace + sentinel); `q` will need a similar approach for the parts of files it modifies, or accept some position drift inside rewritten regions.
- **Imports.** If the rewriter introduces `fmt` or `errors` calls, it must add the import (deduped against existing imports).
- **Side-effect order.** The replacement must evaluate the inner call exactly once and bind its results before the if-check. Naïve textual substitution that re-evaluated `call()` would change semantics.

### 4.4 Cross-package considerations

For Phase 2, the rewriter operates on each user package's compile in isolation: it needs to know that `q.Try` resolves to `github.com/GiGurra/q/pkg/q.Try`, but it does not need to walk into `pkg/q`'s sources. The chain shapes (`q.TryE(...).Err(...)`) are syntactic — the `pkg/q` import alias is enough to disambiguate.

Cross-package cases (e.g. a user wraps `q.Try` in their own helper) are out of scope. Such helpers will trigger the runtime panic backstop until / unless a future phase adds inlining.

## 5. Phasing

- **Phase 1 — link gate + stub injection.** Done. `pkg/q` link-gates via `_qLink`, `cmd/q` injects the no-op stub into `pkg/q`'s compile. E2e harness verifies both halves of the contract.
- **Phase 2 — rewriter for `Try` family.** Pattern-match `q.Try(call)` and `q.TryE(call).Method(args)` in user-package compiles, emit inlined replacements.
- **Phase 3 — rewriter for `NotNil` family.** Mirror of Phase 2 for the nil source-monad.
- **Phase 4 — diagnostic polish.** Position-preserving rewrites for editor / CI consumption; better diagnostics for unsupported shapes.

Future / deferred:

- A counterpart helper for cases where the bubble trigger is neither `(T, error)` nor `*T == nil`. Possibilities: `q.IfNil(x)` for is-nil-as-failure on an interface or chan; `q.Ok(v, ok)` for the comma-ok pattern; `q.Recv(ch)` for channel close. Exact semantic to be agreed when there's a real motivating use case.
- Optimisations like length-preserving rewrites if the position-drift impact in editors / CI becomes annoying.

## 6. Non-goals

- **No general monad library.** `q` is the Rust-`?` analogue; it is not `for { x <- … }` from Scala. If the project ever needed a wider effect surface, that is a separate project, not a feature of `q`.
- **No type-level guarantees.** The rewriter is purely syntactic. Whether `call()` actually returns `(T, error)` is left to the Go compiler to verify after the rewrite (which it will, because the inlined `v, __err := call()` is what the user would have written).
- **No support for outside the function body.** `q.*` calls in package-level `var` initializers, in struct field tags, etc., are outside scope. The rewriter rejects them with a diagnostic.
