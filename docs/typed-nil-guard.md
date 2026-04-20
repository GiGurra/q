# Typed-nil-interface guard

The `q` preprocessor refuses to build any call site that passes a non-`error` type to a `q.*` entry's error slot. This page explains *why* the guard exists, *what* it catches, and *how* to fix a rejected build.

## The pitfall

This is mistake **#45: Returning a nil receiver** in [100 Go Mistakes and How to Avoid Them](https://100go.co/#returning-a-nil-receiver-45) — the canonical reference if the explanation below is new to you.

Go's `f(g())` forwarding rule plus implicit concrete-to-interface assignability creates a sharp edge:

```go
type MyErr struct{ msg string }

func (e *MyErr) Error() string { return e.msg }

func Foo() (int, *MyErr) { return 42, nil }
```

`*MyErr` satisfies `error`, so `*MyErr` is assignable to `error`, so `q.Try(Foo())` type-checks under plain Go. But when `Foo` returns `(v, nil)`, the nil is a typed `(*MyErr)(nil)`. The implicit conversion to the `error` interface wraps the typed nil into a non-nil interface value — a nil concrete inside a non-nil interface. The classic typed-nil-interface pitfall.

```go
var e error = Foo()      // pseudo: the second return slot
e != nil                 // true — even though the *MyErr is nil
```

q's rewriter emits `if err != nil { return zero, err }`. With a typed nil in `err`, that check fires and q bubbles a *non-nil* `error` wrapping *nothing meaningful*. The user sees a "failed" return from an operation that, by the callee's own reckoning, succeeded.

## The guard

During the user-package compile, `q` runs a `go/types` pass (using an importer backed by the compile's `-importcfg` so no rebuild is needed) and inspects every recognised `q.*` call site. For each site, the error-slot's static type must be the built-in `error` interface. Anything else — concrete pointer, struct, named type, user-defined interface — is rejected with a `file:line:col: q: …` diagnostic and the build aborts before the compiler runs.

Error-slot positions per family:

| Family                              | Error slot                                 |
|-------------------------------------|--------------------------------------------|
| `q.Try`, `q.TryE`                   | last return value of the wrapped call      |
| `q.Open`, `q.OpenE`                 | last return value of the wrapped call      |
| `q.Check`, `q.CheckE`               | the single argument's type                 |

`q.NotNil` / `q.NotNilE` consume a `*T`, not an error, so they are out of scope for the guard.

## Example rejection

Source:

```go
func Foo() (int, *MyErr) { return 42, nil }

func run() (int, error) {
    v := q.Try(Foo())
    return v, nil
}
```

Build output:

```text
./main.go:26:7: q: q.Try requires the built-in `error` interface at the last return
value of the wrapped call, but got *main.MyErr. Implicitly converting a concrete
type to `error` triggers Go's typed-nil-interface pitfall: a nil *main.MyErr
becomes a non-nil `error` value, so the bubble check inside q.Try would fire for
a notionally-nil error. Fix by changing the callee to return `error`, or by
converting explicitly at the call site (and accepting that a typed nil will
appear non-nil).
```

## Fixing a rejected build

Two acceptable approaches — pick the one that matches your intent.

### 1. Change the callee to return `error` (preferred)

If the callee's API is yours, widen its return type:

```go
func Foo() (int, error) {
    // ... existing body ...
    return v, nil        // a literal nil error — no pitfall
}
```

Returning a typed nil pointer is the bug the guard is pointing at; fixing the signature removes it at the source.

### 2. Use the `q.ToErr` adapter

For callees whose signature is outside your control (third-party library, generated code, legacy API you cannot change yet), `q` ships a small runtime helper:

```go
// Foo() (int, *MyErr)
v := q.Try(q.ToErr(Foo()))
```

`q.ToErr` takes `(T, *E)` where `*E` satisfies `error`, and returns `(T, error)` with a nil-check that collapses a typed-nil `*E` to a literal nil `error`. Its signature:

```go
func ToErr[T any, E any, P interface{ *E; error }](v T, e P) (T, error)
```

The generic constraint forces `*E` to implement `error` at compile time, so type inference figures out `T`/`E`/`P` from the callee's return values and misuse (passing a non-error pointer) is a type error, not a runtime surprise.

Unlike the other helpers in `pkg/q`, `q.ToErr` is NOT rewritten by the preprocessor — it's a plain runtime function with a real body. That means it's also useful outside `q`, for any code that needs to safely adapt a `(T, *E)` API into `(T, error)`.

### 3. Convert explicitly at the call site

If you'd rather write the conversion inline, wrap the call in a closure so the assignability is visible:

```go
v := q.Try(func() (int, error) {
    x, e := Foo()
    return x, e           // explicit conversion — you've now seen it
}())
```

You've explicitly acknowledged that a typed nil will appear non-nil; the guard accepts that judgement call. If the concrete type is guaranteed never to produce a typed nil (rare — it's a discipline the callee's author has to uphold), the conversion becomes safe. In practice, `q.ToErr` is usually the cleaner fix.

## Why not auto-convert?

An earlier proposal was to have the rewriter emit a `if e == (*MyErr)(nil) { return zero, nil }` guard before the normal bubble check — silently recovering from the typed-nil case. We rejected that direction: it *changes the semantics* of the user's code (a value the callee explicitly returned would be discarded) for the sake of hiding a design flaw in the callee. q's design philosophy elsewhere (`panicUnrewritten` bodies, the link gate) prefers loud, early failures over silent recoveries. The guard continues that stance.

## When the guard cannot run

The guard relies on type information from the compile's `-importcfg`. If that's missing, malformed, or the importer construction fails, the guard silently skips and the build proceeds. Two reasons this is safe:

1. The guard is a lint — its job is to catch mistakes earlier than Go does. It's not a correctness-critical part of the rewrite.
2. If the guard skips and the code really does hit the typed-nil pitfall, the user sees the bogus "non-nil error" bubble at runtime and can then investigate. The failure mode is familiar Go, not q-specific weirdness.

In practice every `go build` invocation produces a valid importcfg, so the guard runs on every real compile.
