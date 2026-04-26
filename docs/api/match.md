# Value-returning switch: `q.Match`, `q.Case`, `q.Default`

Go's `switch` is a statement — it doesn't return a value. Most other modern languages have a value-returning `match` / `when` expression; Go forces an IIFE wrap or a temp variable when you actually want "compute X based on cases." `q.Match` ships that pattern as a single expression. When the matched value's type is an enum, the typecheck pass also enforces coverage.

## Signatures

```go
func Match[R any](value any, arms ...MatchArm[R]) R

func Case[R any](cond any, result R) MatchArm[R]
func Default[R any](result R) MatchArm[R]
```

`q.Match`'s `value` is `any`-typed at the Go signature level; the preprocessor recovers the actual matched-value type via `go/types` and validates each arm's `cond` against it. `R` is inferred from the first arm's `result`.

A single arm constructor — `q.Case(cond, result)` — covers every dispatch shape. The preprocessor inspects `cond`'s resolved type at compile time and chooses how to emit the rewritten branch. No `q.CaseFn` / `q.WhereFn` / `q.Where` family — one knob does it all.

## Cond dispatch

The first argument to `q.Case` is whatever decides whether the arm fires. Four shapes are accepted:

| `cond` resolved type        | Behaviour                              |
|-----------------------------|----------------------------------------|
| matched value's type (`V`)  | Value-equality match (`v == cond`)     |
| `bool`                      | Predicate match (`if cond`)            |
| `func() V`                  | Lazy value match — call cond, compare |
| `func() bool`               | Lazy predicate — call cond            |

Anything else fails the build with a clear diagnostic.

```go
result := q.Match(n,
    q.Case(0, "zero"),                       // value match (cond is int, V is int)
    q.Case(n > 0, "positive"),               // predicate (cond is bool)
    q.Case(getThreshold, "matches t"),       // lazy value match (cond is func() int)
    q.Case(slowPositive(n), "complex pos"),  // lazy predicate (cond is func() bool)
    q.Default("other"),
)
```

## Source-rewriting (laziness for free)

Both `cond` and `result` are captured by the preprocessor as **source text** and re-emitted inside the rewritten if/case body. Neither runs as a Go argument at the `q.Case` call site — they only run when the arm matches. Same trick `q.F` / `q.SQL` use with their format args.

```go
result := q.Match(n,
    q.Case(0, expensive(n)),     // expensive(n) only runs when n == 0
    q.Case(n > 0, log(n)),       // log(n) only runs when n > 0
    q.Default(fallback()),       // fallback() only runs when no arm matched
)
```

This collapses the eager/lazy distinction the old `q.CaseFn` made explicit: every result expression is naturally lazy via source rewrite. To pass a *function value* as a result (rather than the function's call result), spell the call: `q.Case(0, makeFallback())` not `q.Case(0, makeFallback)`.

## Output shape: switch vs if-chain

The rewriter chooses between two shapes based on the arms:

- **All value-equality arms** (no predicate `cond`) → IIFE-wrapped Go `switch`.
- **Any predicate arm** (`bool` or `func() bool` cond) → IIFE-wrapped if/else-if chain (Go's `switch` can't carry predicate cases).

Switch shape:

```go
result := q.Match(c,
    q.Case(Red,   "warm"),
    q.Case(Green, "natural"),
    q.Case(Blue,  "cool"),
)
// → (func() string {
//        switch c {
//        case Red:   return "warm"
//        case Green: return "natural"
//        case Blue:  return "cool"
//        }
//        var _zero string; return _zero
//    }())
```

If-chain shape:

```go
result := q.Match(n,
    q.Case(0, "zero"),
    q.Case(n > 0, "positive"),
    q.Default("negative"),
)
// → (func() string {
//        _v := n
//        if _v == 0 { return "zero" }
//        if n > 0   { return "positive" }
//        return "negative"
//    }())
```

The `_v` binding is only emitted when at least one value-match arm exists; otherwise the matched value is consumed via `_ = value` (so its side effects still fire) and not bound.

## Coverage check

When the matched value's type is an enum (a defined type with declared constants) AND every non-default arm is a value match AND no `q.Default` is provided, the typecheck pass validates that every constant has a case:

```go
result := q.Match(c,
    q.Case(Red,   "warm"),
    q.Case(Green, "natural"),
    // Forgot Blue:
)
// → build fails: q.Match on Color is missing case(s) for: Blue
```

Predicate arms can't be statically counted toward coverage — they encode arbitrary conditions, not equality on a constant. So when **any** arm is a predicate, a `q.Default` arm is **required**. Building without one is a compile-time diagnostic.

When V is opted into [`q.GenEnumJSONLax`](gen.md), a `q.Default(...)` arm is also required — the wire format admits unknown values, so runtime drift / forward-compat values must be handled explicitly even when every declared constant has a case. See [`q.Exhaustive`](exhaustive.md) for the same rule at the statement level.

## Composes with non-enum values

`V` doesn't have to be an enum. Any `comparable` Go value works — `int`, `string`, custom types, structs, etc. With no enum to drive coverage, a `q.Default` arm is generally what you want (otherwise the IIFE returns `R`'s zero value on no-match).

```go
status := q.Match(httpCode,
    q.Case(200, "ok"),
    q.Case(404, "not found"),
    q.Case(500, "internal error"),
    q.Default("unknown"),
)
```

## Rich result types

`R` can be any type — struct, slice, map, function — anything that all arms agree on:

```go
type Coords struct{ X, Y int }

vec := q.Match(direction,
    q.Case("up",    Coords{0, -1}),
    q.Case("down",  Coords{0, 1}),
    q.Case("left",  Coords{-1, 0}),
    q.Case("right", Coords{1, 0}),
    q.Default(Coords{0, 0}),
)
```

The typecheck pass infers `R` from the first arm's result expression and emits the IIFE with that exact type spelling.

## Caveats

- **All arms must agree on `R`.** Standard Go typing — mixed result types fail at the q.Match-arms inference step.
- **Matched value must be `comparable` for value-match arms.** Slices, maps, and functions can't be value-compared (Go's switch requirement, not q's). Predicate-only matching works for any matched value type.
- **Cross-package enum types** — coverage check is same-package only (matches the rest of the q.Enum* family).

## Discriminated-sum dispatch

When the matched value is a [`q.OneOfN`](oneof.md)-derived sum type,
`q.Match` switches into tag-dispatch mode: each `q.Case`'s cond *type*
selects the variant (the value is dropped), and a third arm constructor
`q.OnType(func(T) R)` binds the typed payload:

```go
type Status q.OneOf3[Pending, Done, Failed]

desc := q.Match(s,
    q.Case(Pending{}, "waiting"),
    q.OnType(func(d Done) string   { return "done at " + d.At.String() }),
    q.OnType(func(f Failed) string { return "failed: " + f.Err.Error() }),
)
```

The typecheck pass enforces every variant has an arm (or `q.Default`).
See [`q.OneOfN`](oneof.md) for the construction surface and the
statement-level `q.Exhaustive` form.

## See also

- [`q.Exhaustive`](exhaustive.md) — statement-level switch coverage. Same coverage rules.
- [`q.OneOfN`](oneof.md) — discriminated sum types. Integrates here via `q.Case` + `q.OnType`.
- [`q.GenEnumJSONLax`](gen.md) — enable forward-compat JSON; pair with `q.Match` + `q.Default` for the unknown-arm.
