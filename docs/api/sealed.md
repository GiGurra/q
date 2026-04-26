# `q.Sealed` — interface-based sealed sum types

`q.Sealed` is the interface-based sibling of [`q.OneOfN`](oneof.md):
each variant lives as its own type at runtime (no `Tag` / `Value`
boxing) and the carrier is a marker interface that the variants
implement via auto-synthesised marker methods. The closed set is
declared at package level via a single directive call, and the q
preprocessor synthesises one `func (Variant) markerName() {}` per
variant in a companion file.

This is the right tool for **message-passing systems**: variants flow
through `chan Message` as themselves, type switches dispatch
naturally, and `q.Exhaustive` enforces coverage at build time.

## At a glance

```go
type Message interface{ message() }   // 1-line marker — name is yours

type Ping       struct{ ID int }
type Pong       struct{ ID int }
type Disconnect struct{ Reason string }

var _ = q.Sealed[Message](Ping{}, Pong{}, Disconnect{})
```

After preprocessing, each variant satisfies `Message` via the
synthesised `func (V) message() {}` in a companion file. Producer and
consumer code reads as plain Go:

```go
// Producer:
ch <- Ping{ID: 1}                         // implicit conversion to Message
ch <- Pong{ID: 2}                         // (Go's type system enforces it
ch <- Disconnect{Reason: "timeout"}       //  via the synthesised marker)

// Consumer (statement form):
for m := range ch {
    switch v := q.Exhaustive(m).(type) {  // coverage-checked at build
    case Ping:       handlePing(v)
    case Pong:       handlePong(v)
    case Disconnect: handleDisconnect(v)
    }
}

// Consumer (expression form):
desc := q.Match(m,
    q.OnType(func(p Ping) string       { return fmt.Sprintf("ping %d", p.ID) }),
    q.OnType(func(p Pong) string       { return fmt.Sprintf("pong %d", p.ID) }),
    q.OnType(func(d Disconnect) string { return fmt.Sprintf("dc: %s", d.Reason) }),
)
```

## Surface

```go
// In pkg/q:
func Sealed[I any](variants ...any) GenMarker
```

`I` is the marker interface. The variadic args are zero-value type
carriers — only their **types** matter; the values themselves are
discarded. Each `Variant{}` reads as "register this type as a member
of the sealed set." The directive sits at package level:

```go
var _ = q.Sealed[Message](Ping{}, Pong{}, Disconnect{})
```

The variadic value-args design was chosen over arity-suffixed types
(`q.Sealed3`, `q.Sealed4`, …) so there's no ceiling on the number of
variants.

## What the preprocessor does

For each `var _ = q.Sealed[I](v1, v2, …)`:

1. **Inspects `I`** via go/types: it must be an interface with
   exactly one method, no args, no results. The method name and
   signature are extracted (the user picks the name; q doesn't
   impose a convention).
2. **Validates each variant**: each must be a defined named type
   declared in the same package as the q.Sealed call (Go disallows
   method declarations on types defined in another package).
3. **Synthesises** `func (V) markerName() {}` for each variant in a
   companion `_q_gen.go` file (same machinery as q.GenStringer).
4. **Registers** the closed set so q.Exhaustive type-switch coverage
   and q.Match dispatch can consult it.

After step 3, each variant satisfies `I` — the type system enforces
"only declared variants flow through I-typed slots."

## Construction

Direct, no helpers needed. The variant value already implements the
marker interface:

```go
var m Message = Ping{ID: 1}            // direct assignment
ch <- Ping{ID: 1}                       // channel send
return Pong{ID: 2}                      // function return
```

Compare with the struct-flavour [`q.OneOfN`](oneof.md):

```go
// q.OneOfN — variants are wrapped in a Tag/Value struct:
s := q.AsOneOf[Status](Pending{})        // wraps to {Tag: 1, Value: Pending{}}

// q.Sealed — variants ARE themselves; no wrapper:
var m Message = Ping{}                   // implements Message via synthesised marker
```

## Dispatch — statement form

```go
switch v := q.Exhaustive(m).(type) {
case Ping:       handlePing(v)
case Pong:       handlePong(v)
case Disconnect: handleDisconnect(v)
}
```

`q.Exhaustive(m)` is the identity (returns m unchanged). The
preprocessor walks the type-switch's case clauses and validates that
every variant in the closed set has a case. Build fails otherwise:

```
q.Exhaustive type switch on sealed sum is missing case(s) for: Disconnect.
Add the missing case(s), or use `default:` to opt out.
```

`default:` opts out of the missing-case rule but doesn't substitute
for covering declared variants — same semantics as the const-enum
form. See [`q.Exhaustive`](exhaustive.md).

## Dispatch — expression form (`q.Match`)

When you want a value-returning dispatch, use `q.Match` with
`q.OnType` arms (binds the typed payload) or `q.Case` arms
(payload-discarding):

```go
desc := q.Match(m,
    q.OnType(func(p Ping) string       { return fmt.Sprintf("ping %d", p.ID) }),
    q.OnType(func(p Pong) string       { return fmt.Sprintf("pong %d", p.ID) }),
    q.OnType(func(d Disconnect) string { return fmt.Sprintf("dc: %s", d.Reason) }),
)
```

The rewriter emits an IIFE-wrapped Go type switch — same shape as the
statement form but inside a closure that returns the result. Coverage
is enforced the same way as `q.Exhaustive`.

`q.Default` waives the missing-variant rule:

```go
desc := q.Match(m,
    q.OnType(func(p Ping) string { return fmt.Sprintf("only-ping %d", p.ID) }),
    q.Default("not a ping"),
)
```

## Constraints

### Marker interface must be 1-method, no args, no results

`q.Sealed` is the marker pattern, not a general impl-injector. If `I`
has more than one method, embedded interfaces, or a method with args
or results, the build fails with a directed diagnostic. For richer
interfaces, write the impls yourself on each variant.

```go
type Message interface { message() }            // ✓ valid marker
type Message interface { message(); other() }   // ✗ build fails: must have exactly one method
type Message interface { Process(int) error }   // ✗ build fails: marker takes no args, no results
```

### Same-package variants only

Go disallows method declarations on types defined in another package.
Since q synthesises the marker method on each variant, every variant
must live in the same package as the `q.Sealed` declaration:

```go
var _ = q.Sealed[Message](Ping{}, otherpkg.Foo{})  // ✗ build fails: Foo is foreign
```

For cross-package variants, fall back to writing the marker yourself
in the foreign type's package (or wrap it in a same-package newtype).

### Variants are zero-value type carriers

The variadic args are read for their static types — values are
discarded. Pass zero values for clarity:

```go
var _ = q.Sealed[Message](Ping{}, Pong{}, Disconnect{})        // recommended
var _ = q.Sealed[Message](Ping{ID: 9}, Pong{ID: 9}, Disconnect{Reason: "x"})  // also works
                                                                                // (values discarded)
```

## When to use `q.Sealed` vs `q.OneOfN`

| Pick `q.Sealed` (interface) when…                       | Pick `q.OneOfN` (struct) when…                              |
|---------------------------------------------------------|-------------------------------------------------------------|
| All variants are same-package structs                   | Variants live in different packages                         |
| You want variants to flow as themselves                 | You need a single concrete carrier type                     |
| Channel-typed-as-the-union (`chan Message`) is the use case | Variants include primitives (`int`, `string`, atoms)    |
| The "marker interface" idiom is what you'd write by hand | You want an explicit `Tag` field for tag-based serialization |

Both enforce coverage at build via `q.Exhaustive` and `q.Match`. They
differ purely in the runtime representation.

## Caveats

- **Companion-file synthesis**. q.Sealed adds a new `_q_gen.go` file
  to the user's package compile (or extends an existing one for
  packages that also use `q.GenStringer` etc.). Method-name
  collisions with hand-written code in the same package would be a
  compile error — pick a marker name that isn't already in use.
- **The marker name is whatever the user picked**. The preprocessor
  inspects `I` and synthesises that exact method on each variant.
  No conventions imposed; pick whatever reads cleanly.
- **`q.AsOneOf` does not work on Sealed types**. The build fails
  with a directed diagnostic pointing the user at direct
  construction.

## See also

- [`q.OneOfN`](oneof.md) — struct-based sum sibling. Different runtime
  carrier; same `q.Match` / `q.Exhaustive` integration.
- [`either.Either`](either.md) — Scala-flavoured 2-arm sum (struct
  form, named arms).
- [`q.Match`](match.md) — value-returning dispatch; integrates here
  via `q.OnType` arms.
- [`q.Exhaustive`](exhaustive.md) — statement-level coverage on enum
  constants and (via `.(type)`) on Sealed variants.
