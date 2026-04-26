# `q.OneOfN` / `q.AsOneOf` / `q.OnType` — discriminated sum types

Go shipped without sum types — the headline rejected proposal. `q.OneOfN`
gives a real, type-safe discriminated union: declare the variants,
construct via `q.AsOneOf`, dispatch via `q.Match` (with `q.Case` and/or
`q.OnType` arms) or via a `q.Exhaustive` type switch. The typecheck
pass enforces every variant has a handler.

```go
type Pending struct{}
type Done    struct{ At time.Time }
type Failed  struct{ Err error }

type Status q.OneOf3[Pending, Done, Failed]

s := q.AsOneOf[Status](Done{At: time.Now()})

desc := q.Match(s,
    q.Case(Pending{}, "waiting"),
    q.OnType(func(d Done) string   { return "done at " + d.At.String() }),
    q.OnType(func(f Failed) string { return "failed: " + f.Err.Error() }),
)
```

## Surface

```go
type OneOf2[A, B any]             struct { Tag uint8; Value any }
type OneOf3[A, B, C any]          struct { Tag uint8; Value any }
type OneOf4[A, B, C, D any]       struct { Tag uint8; Value any }
type OneOf5[A, B, C, D, E any]    struct { Tag uint8; Value any }
type OneOf6[A, B, C, D, E, F any] struct { Tag uint8; Value any }

func AsOneOf[T any](v any) T

func OnType[R, T any](handler func(T) R) MatchArm[R]
```

`q.AsOneOf[T](v)` rewrites in-place to `T{Tag: <pos>, Value: v}` after
the typecheck pass validates `v`'s type matches one of `T`'s arm
type-arguments. The 1-based position becomes the runtime `Tag`.

`q.OnType` is recognised as a third arm constructor for `q.Match`,
alongside `q.Case` and `q.Default`.

## Variants are positional

```go
type Status q.OneOf3[Pending, Done, Failed]
//                   ^pos 1   ^pos 2 ^pos 3
```

`q.AsOneOf[Status](Pending{})` → `Status{Tag: 1, Value: Pending{}}`.
The position is fixed by the order of type arguments in `T`'s
underlying `q.OneOfN[…]` declaration; reordering the arms reorders the
runtime tags.

Variants must be type-distinct. `q.OneOf2[int, int]` is rejected at
build time: there's no way for `q.AsOneOf` or the dispatch to pick
between two arms of the same type.

## Construction: `q.AsOneOf`

```go
s := q.AsOneOf[Status](Done{At: time.Now()})
// → Status{Tag: 2, Value: Done{At: time.Now()}}
```

The build fails if `v`'s type isn't one of `T`'s variants:

```
q.AsOneOf[T]: value type X is not one of T's variants (accepted: Pending, Done, Failed)
```

`T` can be either:
- A defined named type whose underlying type is `q.OneOfN[…]`
  (`type Status q.OneOf3[A, B, C]`) — the typical case.
- A bare `q.OneOfN[…]` instantiation (`q.AsOneOf[q.OneOf2[A, B]](a)`)
  — useful for ad-hoc sums but reads worse at the use site.

## Dispatch: `q.Match` + `q.Case` / `q.OnType`

When `q.Match`'s value is a OneOfN-derived sum, the typecheck pass
flips into "tag dispatch" mode. Two arm shapes are accepted:

| Arm                                  | Use when                              |
|--------------------------------------|---------------------------------------|
| `q.Case(VariantZero{}, result)`      | Don't need the variant's payload      |
| `q.OnType(func(t T) R { … })`        | Need to bind the typed payload        |
| `q.Default(result)`                  | Catch-all (waives coverage check)     |

The two non-default forms can mix freely:

```go
desc := q.Match(s,
    q.Case(Pending{}, "waiting"),                                       // payload-less
    q.OnType(func(d Done) string   { return "done at " + d.At.String() }),
    q.OnType(func(f Failed) string { return "failed: " + f.Err.Error() }),
)
```

`q.Case`'s cond is the *type* selector — its value is dropped.
`Pending{}` reads as "type tag for Pending"; the `{}` is just the
shortest expression of the right type. For atom variants, `q.A[T]()`
serves the same purpose:

```go
type Idle    q.Atom
type Working q.Atom
type Activity q.OneOf2[Idle, Working]

desc := q.Match(a,
    q.Case(q.A[Idle](),    "idle"),
    q.Case(q.A[Working](), "working"),
)
```

### Coverage

When at least one `q.OnType` or tag-`q.Case` arm exists and there's no
`q.Default`, every variant must have at least one arm. Build fails
otherwise:

```
q.Match on q.OneOfN-derived value is missing arm(s) for: Failed.
Add q.Case(<variant>{}, …) / q.OnType(func(<variant>) …),
or add a q.Default(…) arm.
```

`q.Default(...)` waives the missing-variant check.

### Output shape

The rewriter emits an IIFE-wrapped switch on `_v.Tag`:

```go
desc := q.Match(s,
    q.Case(Pending{}, "waiting"),
    q.OnType(func(d Done) string { return "done at " + d.At.String() }),
    q.OnType(func(f Failed) string { return "failed: " + f.Err.Error() }),
)
// →
desc := (func() string {
    _v := s
    switch _v.Tag {
    case 1: return "waiting"
    case 2: return (func(d Done) string { return "done at " + d.At.String() })(_v.Value.(Done))
    case 3: return (func(f Failed) string { return "failed: " + f.Err.Error() })(_v.Value.(Failed))
    }
    var _zero string; return _zero
}())
```

## Statement-level dispatch: `q.Exhaustive` type switch

For the statement-level dispatch — when you want to bind the typed
variant in scope and run multiple statements per arm — pair `q.Exhaustive`
with a Go type switch on the unwrapped `.Value`:

```go
switch v := q.Exhaustive(s.Value).(type) {
case Pending:
    // payload-less variant
case Done:
    fmt.Println(v.At)
case Failed:
    fmt.Println(v.Err)
}
```

`q.Exhaustive(s.Value)` is plain Go to gopls (`q.Exhaustive` is the
identity on type `T`); the `.(type)` assertion type-checks because
`s.Value`'s static type is `any`. The typecheck pass spots the OneOfN
ancestor of `s.Value` and walks the variant list to drive the
coverage check:

```
q.Exhaustive type switch on q.OneOfN-derived value is missing case(s) for: Failed.
Add the missing case(s), or use `default:` to opt out.
```

`default:` opts out of the missing-case rule but doesn't substitute
for covering declared variants — same semantics as the const-enum
form.

## Direct construction is unsafe

The `Tag` and `Value` fields are exported only because the
preprocessor must construct instances at the user's call site
(composite literals can't reach unexported fields across package
boundaries). Direct construction bypasses variant validation:

```go
s := Status{Tag: 9, Value: 42}     // well-typed but malformed
                                    // — q.Match.OnType will panic
                                    //   on the wrong type assertion
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
| Coverage check (q.Match)       | ✓ via typecheck pass          | n/a (no q.Match arm for it yet)      |
| Coverage check (q.Exhaustive)  | ✓ via typecheck pass          | n/a                                   |
| Marker-method boilerplate      | None                          | `func (T) isM() {}` per variant      |
| Variants pass as themselves    | Through `.Value.(T)`          | Naturally (each is its own type)     |
| Single concrete carrier type   | `T` (the alias) — just one    | `M` (the interface) — just one       |

A future `q.Sealed` family will give the interface-based form with
auto-synthesized markers + the same `q.Exhaustive` coverage; tracked
in TODO.

## Caveats

- **Same-package OneOfN-derived types only for the alias form.** A
  `type Status q.OneOf3[…]` declared in package A can be used from
  package B for construction, dispatch, etc.; only the *declaration*
  must be in A. (The typecheck pass walks the package's TypeSpecs to
  discover the variant list.)
- **Variants must be type-distinct.** `q.OneOf2[int, int]` is rejected.
- **Bare `q.OneOfN[...]` (no alias) works** but the type spelling
  reads worse at the use site. Prefer `type X q.OneOfN[...]`.
- **Mixing predicate / value-equality `q.Case` arms with OneOfN
  dispatch** is not supported — the cond's type drives whether a
  q.Case arm is value-equality or tag dispatch, and predicates have
  no analogue in OneOf mode. Use `q.Default` for catch-all behaviour.

## See also

- [`q.Match`](match.md) — the value-returning switch this integrates with.
- [`q.Exhaustive`](exhaustive.md) — statement-level coverage on enum
  constants and (via `.(type)`) on OneOfN variants.
- [`q.Atom`](atom.md) — Erlang-flavoured typed-string atoms; pairs
  cleanly as a OneOfN variant for atom-only sums.
