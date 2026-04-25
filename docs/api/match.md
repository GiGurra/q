# Value-returning switch: `q.Match`, `q.Case`, `q.Default`

Go's `switch` is a statement — it doesn't return a value. Most other modern languages have a value-returning `match` / `when` / `match` expression; Go forces an IIFE wrap or a temp variable when you actually want "compute X based on cases." `q.Match` ships that pattern as a single expression. When the matched value's type is an enum, the typecheck pass also enforces coverage.

## Signatures

```go
func Match[V comparable, R any](value V, cases ...MatchCase[V, R]) R

// Eager — result evaluates at the q.Match call site:
func Case[V, R any](value V, result R) MatchCase[V, R]
func Default[V comparable, R any](result R) MatchCase[V, R]

// Lazy — fn runs only when this arm matches:
func CaseFn[V, R any](value V, fn func() R) MatchCase[V, R]
func DefaultFn[V comparable, R any](fn func() R) MatchCase[V, R]
```

`V` must be `comparable` (Go's `switch` requirement). `R` is the result type. Both are usually inferred from the cases; you rarely need to spell them.

## At a glance

```go
type Color int
const (Red Color = iota; Green; Blue)

description := q.Match(c,
    q.Case(Red,   "warm"),
    q.Case(Green, "natural"),
    q.Case(Blue,  "cool"),
)

// Forget Blue:
description := q.Match(c,
    q.Case(Red,   "warm"),
    q.Case(Green, "natural"),
)
// → build fails: q.Match on Color is missing case(s) for: Blue. Add the missing case(s), or add a q.Default(...) arm.
```

## What gets generated

```go
result := q.Match(c,
    q.Case(Red,   "warm"),
    q.Case(Green, "natural"),
    q.Case(Blue,  "cool"),
)
// Rewritten:
result := (func() string {
    switch c {
    case Red:   return "warm"
    case Green: return "natural"
    case Blue:  return "cool"
    }
    var _zero string
    return _zero
}())
```

The IIFE pattern is what you'd write by hand; q just hides the boilerplate. The Go compiler optimises away the closure for direct switches.

## Lazy arms — `q.CaseFn` / `q.DefaultFn`

By default each `q.Case` result evaluates eagerly at the `q.Match` call site (Go's argument-evaluation rules: every arg is evaluated before the call). For expensive computations or side effects you only want on the matching arm, use `q.CaseFn` / `q.DefaultFn`:

```go
desc := q.Match(c,
    q.CaseFn(Red,   func() string { return loadRedDescription() }),    // only runs if c == Red
    q.CaseFn(Green, func() string { return loadGreenDescription() }),  // only runs if c == Green
    q.CaseFn(Blue,  func() string { return loadBlueDescription() }),   // only runs if c == Blue
)
```

Mix freely with the eager forms in the same `q.Match`:

```go
desc := q.Match(c,
    q.Case(Red,    "warm"),                                             // cheap, eager
    q.CaseFn(Green, func() string { return analyseGreenAtRuntime() }), // expensive, lazy
    q.Case(Blue,   "cool"),
)
```

The rewriter emits `case <val>: return (<fn>)()` for lazy arms and `case <val>: return <result>` for eager. The function is invoked exactly once on the matching branch — Go's compiler typically inlines anonymous closures, so the cost is the same as a hand-written switch.

## `q.Default` opts out of coverage

Adding a `q.Default(...)` arm catches anything not explicitly cased and disables the missing-case check:

```go
result := q.Match(c,
    q.Case(Red,   "red-only"),
    q.Default("anything else"),
)
// → build passes. Default catches Green / Blue / future variants.
```

When forward-compat with `q.GenEnumJSONLax` matters, this is the right shape: `q.Default` handles wire values your code doesn't recognise. New constants added later still trigger the missing-case diagnostic — see [`q.Exhaustive`](exhaustive.md) for the same pattern at the statement level.

## Composes with non-enum values

`V` doesn't have to be an enum. Any `comparable` Go value works — `int`, `string`, custom types, etc. With no enum to drive coverage, `q.Default` is required (otherwise the IIFE returns `R`'s zero value on no-match, which is rarely what you want).

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

## Why a function-shaped form

Considered alternatives:

- **`q.Switch(v, q.Case(...), ...)`** — same shape, different keyword. Decided on `Match` for the Scala/Rust/Swift overlap.
- **`switch q.MatchExpr(v) { case ...: return ... }`** — would need a real switch in expression position, which Go doesn't support. Adding it would require new syntax that gopls couldn't parse.
- **A statement form: `q.MatchStmt(&result, v, q.Case(...))`** — works without IIFE wrapping but loses the value-returning shape that makes the helper appealing.

The IIFE form was chosen because it's the only way to get a value-returning switch in Go without new syntax.

## Caveats

- **The case results must all share a type.** Standard Go switch rules — `q.Match(x, q.Case(0, "a"), q.Case(1, 2))` won't type-check because `"a"` and `2` disagree.
- **`V` must be `comparable`.** Slices, maps, and functions can't be used as the matched value (Go's switch requirement, not q's).
- **Cross-package enum types** — coverage check is same-package only (matches the rest of the q.Enum* family).

## See also

- [`q.Exhaustive`](exhaustive.md) — statement-level switch coverage. Same coverage rules.
- [`q.GenEnumJSONLax`](gen.md) — enable forward-compat JSON; pair with `q.Match` + `q.Default` for the unknown-arm.
