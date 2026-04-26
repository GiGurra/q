# `q.OneOfN` / `q.AsOneOf` — discriminated sum types

Go shipped without sum types — the headline rejected proposal.
`q.OneOfN` gives a real, type-safe discriminated union: declare the
variants once, construct via `q.AsOneOf`, dispatch via a regular Go
type switch with `q.Exhaustive` coverage. The typecheck pass enforces
every variant is handled.

## Declare a sum

```go
type Pending struct{}
type Done    struct{ At time.Time }
type Failed  struct{ Err error }

type Status q.OneOf3[Pending, Done, Failed]
```

`Status` is a defined type whose underlying type is the generic
`q.OneOf3`. The type-arg position fixes each variant's runtime tag:
`Pending` is tag 1, `Done` is tag 2, `Failed` is tag 3.

## Function output: producing a sum value

```go
func currentStatus() Status {
    if pending {
        return q.AsOneOf[Status](Pending{})
    }
    if completed {
        return q.AsOneOf[Status](Done{At: time.Now()})
    }
    return q.AsOneOf[Status](Failed{Err: errors.New("timeout")})
}
```

`q.AsOneOf[T](v)` rewrites in place to `T{Tag: <pos>, Value: v}` after
the typecheck pass validates `v`'s type matches one of `T`'s variants.
A wrong variant type fails the build with a directed diagnostic.

## Function input: accepting + dispatching a sum

The natural Go shape is a type switch on `.Value`. `q.Exhaustive`
enforces coverage at build:

```go
func describe(s Status) {
    switch v := q.Exhaustive(s.Value).(type) {
    case Pending:
        fmt.Println("waiting")
    case Done:
        fmt.Println("done at", v.At)
    case Failed:
        fmt.Println("failed:", v.Err)
    }
}
```

Forgetting any variant is a build failure:

```
q.Exhaustive type switch on q.OneOfN-derived value is missing case(s) for: Failed.
Add the missing case(s), or use `default:` to opt out.
```

`default:` opts out of the missing-case rule but doesn't substitute
for covering declared variants — same semantics as the const-enum
form. See [`q.Exhaustive`](exhaustive.md).

This is the everyday usage. The rest of this page covers expression-
form dispatch, atom variants, and the construction surface in detail.

## Surface

```go
type OneOf2[A, B any]             struct { Tag uint8; Value any }
type OneOf3[A, B, C any]          struct { Tag uint8; Value any }
type OneOf4[A, B, C, D any]       struct { Tag uint8; Value any }
type OneOf5[A, B, C, D, E any]    struct { Tag uint8; Value any }
type OneOf6[A, B, C, D, E, F any] struct { Tag uint8; Value any }

func AsOneOf[T any](v any) T

// Optional, advanced — payload-binding arms in q.Match expressions:
func OnType[R, T any](handler func(T) R) MatchArm[R]
```

`q.AsOneOf[T](v)` is the construction surface. `q.OnType` is a third
arm constructor for [`q.Match`](match.md), used only when you want
the match to *return a value* AND need to bind the typed payload —
see [Expression-form dispatch](#expression-form-dispatch) below.

## Atom variants

A sum-of-atoms reads cleanly when the variants don't carry data:

```go
type Idle    q.Atom
type Working q.Atom

type Activity q.OneOf2[Idle, Working]

a := q.AsOneOf[Activity](q.A[Idle]())

switch v := q.Exhaustive(a.Value).(type) {
case Idle:    fmt.Println("idle")
case Working: fmt.Println("working")
}
_ = v  // unused — atoms have no payload to bind
```

See [`q.Atom`](atom.md) for the atom surface.

## Expression-form dispatch

`q.Match` works on OneOfN values when you want a value-returning
dispatch (the analogue of Scala / Rust's `match`). Two arm shapes are
accepted:

| Arm                                  | Use when                                |
|--------------------------------------|------------------------------------------|
| `q.Case(Variant{}, result)`          | Don't need the variant's payload         |
| `q.OnType(func(t T) R { … })`        | Need to bind the typed payload           |
| `q.Default(result)`                  | Catch-all (waives coverage check)        |

```go
// Tag-only arms — the cleanest reading when payloads aren't needed:
desc := q.Match(s,
    q.Case(Pending{}, "waiting"),
    q.Case(Done{},    "done"),
    q.Case(Failed{},  "failed"),
)

// Mix tag-only and payload-binding arms freely:
desc := q.Match(s,
    q.Case(Pending{}, "waiting"),
    q.OnType(func(d Done) string   { return "done at " + d.At.String() }),
    q.OnType(func(f Failed) string { return "failed: " + f.Err.Error() }),
)
```

`q.Case`'s cond is the *type* selector — its value is dropped. `Pending{}`
reads as "type tag for Pending"; the `{}` is just the shortest
expression of the right type. For atom variants, `q.A[T]()` serves
the same purpose:

```go
desc := q.Match(a,
    q.Case(q.A[Idle](),    "idle"),
    q.Case(q.A[Working](), "working"),
)
```

Coverage check fires the same way as `q.Exhaustive`: every variant
must have an arm or the call must include `q.Default`. The output
is an IIFE-wrapped switch on `_v.Tag`.

## Construction surface in detail

`q.AsOneOf[T](v)` accepts:

- A defined named type whose underlying type is `q.OneOfN[…]`
  (`type Status q.OneOf3[A, B, C]`) — the typical case.
- A bare `q.OneOfN[…]` instantiation (`q.AsOneOf[q.OneOf2[A, B]](a)`)
  — useful for ad-hoc sums but reads worse at the use site.

Build-time errors:

- `T` isn't a OneOfN-derived type → diagnostic.
- `v`'s type isn't identical to any of `T`'s arm types → diagnostic
  listing the accepted arms.
- `T` has duplicate arm types (e.g. `q.OneOf2[int, int]`) → diagnostic
  (variant dispatch couldn't disambiguate).

## Output shape

The rewriter folds `q.AsOneOf[Status](Done{...})` to a composite
literal:

```go
return q.AsOneOf[Status](Done{At: now})
// → return Status{Tag: 2, Value: Done{At: now}}
```

`q.Match` on a OneOfN value emits an IIFE-wrapped switch:

```go
desc := q.Match(s,
    q.Case(Pending{}, "waiting"),
    q.OnType(func(d Done) string { return "done at " + d.At.String() }),
)
// →
desc := (func() string {
    _v := s
    switch _v.Tag {
    case 1: return "waiting"
    case 2: return (func(d Done) string { return "done at " + d.At.String() })(_v.Value.(Done))
    }
    var _zero string; return _zero
}())
```

## Direct construction is unsafe

`Tag` and `Value` are exported only because the preprocessor must
construct instances at the user's call site (composite literals can't
reach unexported fields across package boundaries). Direct construction
bypasses variant validation:

```go
s := Status{Tag: 9, Value: 42}     // well-typed but malformed
                                    // — q.OnType / type-switch arm
                                    //   will panic on the wrong type
                                    //   assertion
```

Always go through `q.AsOneOf`.

## Runtime cost

One `any` interface box per construction (the wrapped variant value)
plus the `uint8` tag. The dispatch is a switch on a uint8 followed
by a type assertion in handler arms — comparable to a hand-written
type switch.

Specialised non-`any` storage for primitive variants (e.g. inlining
the value into a per-arm field of the struct) is an open optimisation
tracked under TODO #74. Worth it only if profiles show the box
allocation as a measurable cost.

## Comparison: `q.OneOfN` vs Go interfaces

| Aspect                         | `q.OneOfN`                    | Plain interface                     |
|--------------------------------|-------------------------------|--------------------------------------|
| Construction                   | `q.AsOneOf[T](v)`             | `var m M = ConcreteType{...}`        |
| Closed-set declaration         | At type definition            | At sealed-marker convention          |
| Coverage (q.Match / q.Exh.)    | ✓ via typecheck pass          | n/a                                   |
| Marker-method boilerplate      | None                          | `func (T) isM() {}` per variant      |
| Variants pass as themselves    | Through `.Value.(T)`          | Naturally (each is its own type)     |
| Single concrete carrier type   | `T` (the alias) — just one    | `M` (the interface) — just one       |

For the interface-based sibling — variants flow as themselves through
`chan Message`, no `.Value.(T)` unwrap, with the same `q.Exhaustive` /
`q.Match` coverage — see [`q.Sealed`](sealed.md).

## Caveats

- **Same-package OneOfN-derived alias declarations** for the typecheck
  to discover variants from the TypeSpec walk. (The alias can be
  *used* from any package; only the *declaration* must be in the
  package whose typecheck pass runs.)
- **Variants must be type-distinct.** `q.OneOf2[int, int]` is rejected.
- **Mixing predicate / value-equality `q.Case` arms with OneOfN
  dispatch** is not supported — the cond's type drives whether a
  q.Case arm is value-equality or tag dispatch, and predicates have
  no analogue in OneOf mode. Use `q.Default` for catch-all behaviour.

## See also

- [`q.Sealed`](sealed.md) — interface-based sibling. Variants live as
  themselves at runtime; pick this for message-passing systems where
  variants flow through `chan Message` directly.
- [`either.Either`](either.md) — Scala-flavoured 2-arm sibling
  (Left / Right + Fold / Map / FlatMap). Structurally a 2-arm OneOf;
  reuses every integration point here.
- [`q.Exhaustive`](exhaustive.md) — statement-level coverage on enum
  constants and (via `.(type)`) on OneOfN variants.
- [`q.Match`](match.md) — the value-returning switch that integrates
  with q.OneOfN via q.Case + q.OnType arms.
- [`q.Atom`](atom.md) — Erlang-flavoured typed-string atoms; pairs
  cleanly as a OneOfN variant for atom-only sums.
