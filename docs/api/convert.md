# `q.ConvertTo` / `q.ConvertToE` — Chimney-style struct conversions

`q.ConvertTo[Target](src, opts...)` is rewritten at compile time into
a struct literal that copies matching exported fields from `src` into
a fresh `Target` value. No runtime reflection, no map-of-maps, no
struct-tag scanning — the rewriter walks both struct shapes via
`go/types` and emits a plain `Target{F1: src.F1, F2: src.F2, ...}`
literal. Field gaps that the auto-derivation can't satisfy fail the
build with a per-field diagnostic; manual overrides via `q.Set` /
`q.SetFn` / `q.SetFnE` fill the gaps.

The fallible sibling `q.ConvertToE[Target](src, opts...)` returns
`(Target, error)` and lets `q.SetFnE` overrides bubble runtime
failures (database lookups, network calls, parsing) without panic.

## Signatures

```go
// Bare conversion — every override must be infallible.
func ConvertTo[Target, Source any](src Source, opts ...ConvertOption) Target

// Fallible conversion — q.SetFnE overrides may bubble.
func ConvertToE[Target, Source any](src Source, opts ...ConvertOption) (Target, error)

// Overrides — see "Manual overrides" below.
func Set[V any](targetField V, value V) ConvertOption
func SetFn[Source, V any](targetField V, fn func(Source) V) ConvertOption
func SetFnE[Source, V any](targetField V, fn func(Source) (V, error)) ConvertOption
```

`Target` is the explicit type-arg; `Source` is inferred from `src`
(Go's partial type-arg inference). All three override constructors
return the same `ConvertOption` type — the rewriter discriminates by
the call's selector name and enforces "SetFnE only inside ConvertToE"
at compile time.

## Why a single function instead of a chain

The natural Chimney surface is the chain `q.From(src).To[Target]()`,
but Go forbids type parameters on methods. The single-function
`q.ConvertTo[Target](src)` is the closest legal Go shape. Reads as
"convert *to* Target *from* src", matching the type-arg-first call
shape.

## Resolution order, per target field

For each exported field on `Target`, in declaration order:

1. **Override** — `q.Set(Target{}.Field, value)`,
   `q.SetFn(Target{}.Field, fn)`, or
   `q.SetFnE(Target{}.Field, fn)` supplies the value explicitly.
   Wins over auto-derivation per-field — there's no "auto-derive
   first, then patch" pass; the resolver branches on each target
   field once and the override short-circuits the auto path.
2. **Direct copy** — same-named source field whose type is
   `types.AssignableTo` the target field's type.
3. **Nested derivation** — same-named source field that is itself a
   struct, recursively converted using the same algorithm. Overrides
   pulled from the user's variadic list pass into the recursion if
   their path traverses this field; otherwise the nested derivation
   is parameterless and entirely auto.
4. **Diagnostic** — target field has no source counterpart, no
   assignable copy, no nested derivation. Build fails with a
   per-field message that shows the target field's path and the
   source's shape.

Source fields with no Target counterpart are silently dropped
(target-driven, like Chimney). Source/Target type cycles in the
nested-derivation path are detected and rejected.

## Auto-derivation — the happy path

```go
type User    struct { ID int; Name string; Internal bool; Notes string }
type UserDTO struct { ID int; Name string }

dto := q.ConvertTo[UserDTO](user)
// → UserDTO{ID: user.ID, Name: user.Name}
// User.Internal and User.Notes are silently dropped.
```

The source struct can have arbitrarily many extra fields — the
rewriter only consults the target's field set. Useful when the source
is a wide internal record and the target is an outward-facing DTO
that intentionally exposes a subset.

```go
type WideRecord struct {
    ID, Score, Internal int
    Name, Email, Token string
    CreatedAt, UpdatedAt time.Time
}

type PublicView struct {
    ID    int
    Name  string
    Email string
}

view := q.ConvertTo[PublicView](rec)
// → PublicView{ID: rec.ID, Name: rec.Name, Email: rec.Email}
// Six wide-record fields drop silently.
```

## Nested derivation

When the same-named source field is a *different* struct type but
each of the target sub-struct's fields can be auto-derived from the
source sub-struct, the rewriter recurses:

```go
type Address    struct { Street, City, Country string }
type AddressDTO struct { Street, City string }

type User    struct { ID int; Name string; Address Address }
type UserDTO struct { ID int; Name string; Address AddressDTO }

dto := q.ConvertTo[UserDTO](user)
// → UserDTO{
//       ID:      user.ID,
//       Name:    user.Name,
//       Address: AddressDTO{Street: user.Address.Street, City: user.Address.City},
//   }
```

`Address.Country` is dropped at the nested level for the same reason
extras drop at the top level. Recursion is depth-bounded by Go's
struct nesting; cycles are detected and rejected.

## Manual overrides — Set / SetFn / SetFnE

`q.Set` / `q.SetFn` / `q.SetFnE` cover the three reasons
auto-derivation can't satisfy a target field:

- **`q.Set`** — supply a constant or arbitrary expression.
- **`q.SetFn`** — derive the value from the source (concat, format,
  case-fold, lookup-against-an-in-memory-map, etc.) using a
  guaranteed-infallible function.
- **`q.SetFnE`** — derive the value from the source via a fallible
  function (`func(Source) (V, error)`) — for database calls, remote
  fetches, parser invocations, anything that can fail at runtime. Only
  legal inside `q.ConvertToE`.

```go
type User struct { ID int; First, Last, Email string }
type UserDTO struct {
    ID       int     // auto: matches User.ID
    Email    string  // SetFn: lowercase
    FullName string  // SetFn: concat
    Source   string  // Set:   constant
}

dto := q.ConvertTo[UserDTO](user,
    q.Set(UserDTO{}.Source, "v1"),
    q.SetFn(UserDTO{}.Email,    func(u User) string { return strings.ToLower(u.Email) }),
    q.SetFn(UserDTO{}.FullName, func(u User) string { return u.First + " " + u.Last }),
)
// → UserDTO{
//       ID:       user.ID,
//       Email:    (func(u User) string { return strings.ToLower(u.Email) })(user),
//       FullName: (func(u User) string { return u.First + " " + u.Last })(user),
//       Source:   "v1",
//   }
```

### Nested-field overrides

Multi-hop paths target a nested field directly without forcing the
user to spell out the whole intermediate struct:

```go
type Address    struct { Street, City string }
type AddressDTO struct { Street, City string }
type User       struct { Name string; Address Address }
type UserDTO    struct { Name string; Address AddressDTO }

dto := q.ConvertTo[UserDTO](user,
    q.Set(UserDTO{}.Address.City, "Springfield"),
)
// → UserDTO{
//       Name:    user.Name,
//       Address: AddressDTO{Street: user.Address.Street, City: "Springfield"},
//   }
```

Other fields under the same nested struct (here `Address.Street`)
keep their auto-derived values. Mix-and-match works at any depth.
Conflicting overrides — `q.Set(UserDTO{}.Address, ...)` plus a
deeper `q.Set(UserDTO{}.Address.City, ...)` — fail the build because
the ancestor override would shadow the descendant.

## Fallible conversion — `q.ConvertToE`

`q.ConvertToE` returns `(Target, error)` so individual fields can
fail at runtime without panicking the whole call site. Use it when
some derivation calls something that may fail — a database, a
remote service, a parser — and you'd rather bubble the error than
swallow it.

```go
type User struct { ID int; Name, Token string }
type UserDTO struct {
    ID    int
    Name  string
    Email string  // looked up from a remote source
}

dto, err := q.ConvertToE[UserDTO](user,
    q.SetFnE(UserDTO{}.Email, func(u User) (string, error) {
        return db.LookupEmail(ctx, u.ID)
    }),
)
if err != nil { return err }
```

The rewrite emits an IIFE returning `(Target, error)`. Each `q.SetFnE`
override pre-binds its result before the literal:

```go
dto, err := func() (UserDTO, error) {
    _qSrcN := user
    _qV0, _qErr := (func(u User) (string, error) {
        return db.LookupEmail(ctx, u.ID)
    })(_qSrcN)
    if _qErr != nil { return *new(UserDTO), _qErr }
    return UserDTO{
        ID:    _qSrcN.ID,
        Name:  _qSrcN.Name,
        Email: _qV0,
    }, nil
}()
```

Multiple `q.SetFnE` overrides bind in target-field declaration order;
the first error wins (subsequent fallible calls don't fire). Mixing
`q.Set` / `q.SetFn` / `q.SetFnE` in the same call works — the
infallible ones still render inline.

For the bubble-flat shape, pair with `q.Try`:

```go
dto := q.Try(q.ConvertToE[UserDTO](user, q.SetFnE(...)))
```

`q.SetFnE` inside `q.ConvertTo` (no error slot) is rejected at
compile time with a diagnostic pointing at `q.ConvertToE`.

## Source-evaluation discipline

A bare-identifier source splices directly into the literal:

```go
q.ConvertTo[UserDTO](user)
// → UserDTO{ID: user.ID, Name: user.Name}
```

A non-trivial source (call expression, selector chain with side
effects) binds to a per-call `_qSrcN` inside an IIFE so the source
expression evaluates exactly once:

```go
q.ConvertTo[UserDTO](loadUser())
// → func() UserDTO { _qSrcN := loadUser(); return UserDTO{ID: _qSrcN.ID, Name: _qSrcN.Name} }()
```

`q.ConvertToE` is always an IIFE because it threads error returns,
so the source binds to a temp regardless of shape.

## Mixing everything — a complete example

```go
type User struct {
    ID    int
    First string
    Last  string
    Email string
    Address Address
}
type UserDTO struct {
    ID       int       // auto-derived
    FullName string    // SetFn — concat
    Email    string    // SetFnE — remote lookup
    Source   string    // Set    — constant
    Address  AddressDTO // nested auto-derived; sub-field overridden
}

dto, err := q.ConvertToE[UserDTO](user,
    q.Set(UserDTO{}.Source, "v1"),
    q.SetFn(UserDTO{}.FullName, func(u User) string {
        return u.First + " " + u.Last
    }),
    q.SetFnE(UserDTO{}.Email, func(u User) (string, error) {
        return mailer.Lookup(ctx, u.ID)
    }),
    q.Set(UserDTO{}.Address.City, "Reykjavík"), // override one nested field
)
```

That single statement covers every shape:

- ID auto-derives.
- FullName concatenates from the source via SetFn.
- Email is fetched remotely via SetFnE; failure bubbles as `err`.
- Source is a literal constant via Set.
- Address.Street auto-derives from `user.Address.Street`; Address.City
  is overridden to a fixed string at the nested path.

## What's intentionally not in scope

- **Implicit lifting.** `int → int64`, `*T → T` (deref), `T →
  Option[T]`, etc. require either a runtime helper or an explicit
  override. We stay strict on `types.AssignableTo` so surprises are
  loud.
- **Field renames.** `q.Rename(Target{}.ID, "FooID")` is a clean
  follow-up if needed; for now use `q.SetFn(Target{}.ID, func(s
  Source) int { return s.FooID })`.
- **Slice / map / iter conversions.** Recursive auto-derivation
  works on direct struct fields only. For `[]Foo → []Bar` where
  `Foo → Bar` is derivable, write the loop yourself or wait for the
  follow-up.
- **Cross-package targets with unexported fields.** Auto-derivation
  considers only exported fields, so unexported targets in another
  package can't be assembled.

## See also

- [Scala Chimney](https://chimney.readthedocs.io/) — the inspiration.
- [`q.Try`](try.md) — the bubble-flat wrapper for `q.ConvertToE`.
- [`q.Match`](match.md) — value-returning switch when the source's
  shape is a sum type rather than a struct.
- [`q.Fields`](reflection.md) — exported field names of a struct,
  in case you need them at runtime.
