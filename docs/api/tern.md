# Conditional expression: `q.Tern`

`q.Tern(cond, ifTrue, ifFalse)` is the conditional-expression sugar Go's syntax doesn't have. Returns `ifTrue` when `cond` is true; otherwise `ifFalse`. Only the matching branch is evaluated.

```go
display := q.Tern(user != nil, user.Name, "anonymous")
// → "anonymous" when user is nil; user.Name when not
```

## Signature

```go
func Tern[T any](cond bool, ifTrue, ifFalse T) T
```

Three args, strict types — the kind of signature gopls likes.

## Why a preprocessor pass for what looks like a runtime helper

A naïve runtime implementation would always evaluate BOTH branches (Go's standard arg-evaluation semantics). That breaks the whole point: `q.Tern(user != nil, user.Name, "anonymous")` would panic-deref on a nil `user` *before* `Tern` ever got to choose a branch.

The preprocessor rewrites every call site to an IIFE that splices each branch's source text into its own arm — so a branch is only evaluated when its arm is taken. The rewrite for the example above:

```go
display := (func() string {
    if user != nil {
        return user.Name
    }
    return "anonymous"
}())
```

`user.Name` lives only inside the `if` body. When `user` is nil the if-branch never runs and `"anonymous"` is returned. No nil-deref.

## What you get

- **Lazy evaluation of both branches.** Side-effects, expensive calls, nil-deref-prone field access — only the taken branch runs.
- **Single-eval of `cond`.** `cond` evaluates exactly once, at the IIFE's `if`. Same evaluation point you'd get with a hand-written `if`.
- **Chains naturally.** Nested `q.Tern` calls rewrite cleanly because the inner call's IIFE becomes the outer's branch text. Use this for multi-way picks where a `switch` would be heavier than the call site warrants.
- **No runtime overhead.** The IIFE is one closure call. Go's escape analysis usually inlines it.

## When to reach for `q.Tern` vs plain `if`

Use `q.Tern` when:

- You need a value-returning conditional in an expression position (struct literal field, function arg, return statement, etc.) where Go's statement-shaped `if` doesn't fit.
- The expression is short enough to fit on one line — or chains of `q.Tern` give a cleaner multi-way pick than a `switch`.

Reach for plain `if/else` when:

- The branches are large enough that the IIFE form hurts readability.
- You need any control-flow shape beyond pick-one (break/continue/return mid-branch, etc.).

For a multi-arm value-returning conditional with predicates and exhaustive checks, [`q.Match`](match.md) is purpose-built.

## Examples

```go
// Field defaults via lazy nil-deref:
displayName := q.Tern(user != nil, user.Name, "anonymous")
maxConn     := q.Tern(cfg != nil, cfg.MaxConn, defaultMaxConn)

// Lazy expensive computation — slowLookup() only when cache misses:
v := q.Tern(cached, fast(), slowLookup(key))

// In a struct literal (Go's plain `if` doesn't fit here):
req := Request{
    Timeout:  q.Tern(opts.Timeout > 0, opts.Timeout, defaultTimeout),
    Endpoint: q.Tern(opts.Endpoint != "", opts.Endpoint, defaultEndpoint),
}

// In a return statement:
func sign(n int) int {
    return q.Tern(n > 0, 1, q.Tern(n < 0, -1, 0))
}

// Chained for multi-way pick — nested terns nest cleanly:
tier := q.Tern(score >= 90, "A",
         q.Tern(score >= 80, "B",
          q.Tern(score >= 70, "C", "F")))

// Explicit T when you want to widen the result type:
var iface fmt.Stringer = q.Tern[fmt.Stringer](ok, concreteImpl, fallbackImpl)
```

## Caveats

- **`cond` is always evaluated.** It has to be — that's how the if knows which branch to take. Lazy semantics apply only to the branch values.
- **Both branches must agree on type.** Go's type inference resolves `T` from the branches; mismatched types fail at compile time.
- **Untyped constants follow Go's default-type rule.** `q.Tern(cond, 1, 2)` infers `T` as `int`. If you need a different type, widen at the call site (`q.Tern(cond, int64(1), int64(2))`) or use the explicit form (`q.Tern[int64](cond, 1, 2)`).

## See also

- [`q.Match`](match.md) — value-returning switch with multiple branches and exhaustiveness.
- [`q.Try`](try.md) — bubble shape; q.Tern is the value-form sibling for binary picks.
