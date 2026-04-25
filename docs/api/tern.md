# Conditional expression: `q.Tern`

`q.Tern[T](cond, t)` is the conditional-expression sugar Go's syntax doesn't have. Returns `t` when `cond` is true; otherwise the zero value of `T`.

```go
display := q.Tern[string](user != nil, user.Name)
// → "" when user is nil; user.Name when not
```

## Signature

```go
func Tern[T any](cond bool, t T) T
```

Two args, strict types — the kind of signature gopls likes. Type-arg `T` is required.

## Why a preprocessor pass for what looks like a runtime helper

A naïve runtime implementation would always evaluate `t` (Go's standard arg-evaluation semantics). That breaks the whole point: `q.Tern[string](user != nil, user.Name)` would panic-deref on a nil `user` *before* `Tern` ever got to choose a branch.

The preprocessor rewrites every call site to an IIFE that splices each arg's source text into the chosen branch — so `t` is only evaluated when `cond` is true. The rewrite for the example above:

```go
display := (func() string {
    if user != nil {
        return user.Name
    }
    var _zero string
    return _zero
}())
```

`user.Name` lives only inside the `if` body. When `user` is nil the if-branch never runs and the zero value (`""`) is returned. No nil-deref.

## What you get

- **Lazy evaluation of `t`.** Side-effects, expensive calls, nil-deref-prone field access — only run on the true path.
- **Single-eval of `cond`.** `cond` evaluates exactly once, at the IIFE's `if`. Same evaluation point you'd get with a hand-written `if`.
- **Zero-value default.** When `cond` is false, `T`'s zero value is returned (`""`, `0`, `nil`, etc.).
- **No runtime overhead.** The IIFE is one closure call. Go's escape analysis usually inlines it.

## When to reach for `q.Tern` vs plain `if`

Use `q.Tern` when:

- You need a value-returning conditional in an expression position (struct literal field, function arg, return statement, etc.) where Go's statement-shaped `if` doesn't fit.
- The "false" path is naturally the zero value (`""` / `0` / `nil`).
- The expression is short enough to fit on one line.

Reach for plain `if` when:

- Both branches are explicit and meaningful — q.Tern's implicit zero-value-on-false is a footgun if you wanted a different default.
- The branches are large enough that the IIFE form hurts readability.
- You need any control-flow shape beyond pick-one (break/continue/return mid-branch, etc.).

## Examples

```go
// Field defaults via lazy nil-deref:
displayName := q.Tern[string](user != nil, user.Name)
maxConn     := q.Tern[int](cfg != nil, cfg.MaxConn)

// Lazy expensive computation — slowLookup() only when cache misses:
v := q.Tern[*Conn](missing, slowLookup(key))

// In a struct literal (Go's plain `if` doesn't fit here):
req := Request{
    Timeout:  q.Tern[time.Duration](opts.Timeout > 0, opts.Timeout),
    Endpoint: q.Tern[string](opts.Endpoint != "", opts.Endpoint),
}

// In a return statement:
func sign(n int) int {
    return q.Tern[int](n >= 0, 1) - q.Tern[int](n < 0, 1)
}
```

## Caveats

- **No "false" branch expression.** If you need both branches to be non-zero, use a plain `if/else` block. q.Tern intentionally avoids the three-arg shape (`q.Tern(cond, a, b)`) because that surface is harder to read at most call sites — and Go's `if/else` already handles it.
- **`cond` is always evaluated.** It has to be — that's how the if knows which branch to take. Lazy semantics apply only to `t`.
- **Type-arg `T` is required.** `q.Tern[int](...)` not `q.Tern(...)`. Go can't always infer it from `t` alone (e.g. when `t` is an untyped constant or a function returning the wrong type).

## See also

- [`q.Match`](match.md) — value-returning switch with multiple branches.
- [`q.Try`](try.md) — bubble shape; q.Tern is the value-form sibling for "yes-or-zero" choices.
