# `q.Convert` — Chimney-style struct conversions

`q.Convert[Target](src, opts...)` is rewritten at compile time into a
struct literal that copies matching exported fields from `src` into a
fresh `Target` value. No runtime reflection. Field gaps that the
auto-derivation can't satisfy fail the build with a per-field
diagnostic — fix them with `q.Set` / `q.SetFn` overrides or by adding
the missing field on the source.

## Signatures

```go
func Convert[Target, Source any](src Source, opts ...ConvertOption) Target

// Overrides — see "Manual overrides" below.
func Set[V any](targetField V, value V) ConvertOption
func SetFn[Source, V any](targetField V, fn func(Source) V) ConvertOption
```

## Surface caveat — why it's not a chain

The natural Chimney surface is the chain `q.From(src).To[Target]()`,
but Go forbids type parameters on methods. The single-function
`q.Convert[Target](src)` is the closest legal Go shape — Target as
the explicit type-arg, Source inferred from the argument.

## Resolution order, per target field

For each exported field on `Target`:

1. **Override** — `q.Set(Target{}.Field, value)` or
   `q.SetFn(Target{}.Field, fn)` supplies the value explicitly. Wins
   over auto-derivation per-field — there's no "auto-derive first,
   then patch" pass; the resolver branches on each target field once
   and the override short-circuits the auto path.
2. **Direct copy** — same-named source field whose type is
   `types.AssignableTo` the target field's type.
3. **Nested derivation** — same-named source field that is itself a
   struct, recursively converted using the same algorithm. Overrides
   do *not* propagate into nested derivations (they apply only at the
   top-level Target type).
4. **Diagnostic** — target field has no source counterpart, no
   assignable copy, no nested derivation. Build fails.

Source fields with no Target counterpart are silently dropped
(target-driven, like Chimney). Source/Target type cycles are
detected and rejected.

## Auto-derivation — the happy path

```go
type User    struct { ID int; Name string; Internal bool; Notes string }
type UserDTO struct { ID int; Name string }

dto := q.Convert[UserDTO](user)
// → UserDTO{ID: user.ID, Name: user.Name}
// (User.Internal and User.Notes are silently dropped)
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

dto := q.Convert[UserDTO](user)
// → UserDTO{ID: user.ID, Name: user.Name, Address: AddressDTO{Street: user.Address.Street, City: user.Address.City}}
```

## Manual overrides

For target fields the auto-derivation can't satisfy — no source
counterpart, an incompatibly-typed counterpart, or a value that
needs explicit transformation — supply a `q.Set` (constant /
expression) or `q.SetFn` (function-of-source) override:

```go
type User struct { ID int; First, Last, Email string }
type UserDTO struct {
    ID       int
    Email    string  // needs transformation (lowercase)
    FullName string  // needs construction (concat)
    Source   string  // needs constant
}

dto := q.Convert[UserDTO](user,
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

`targetField` MUST be a struct-literal selector expression of the
form `Target{}.<FieldName>` — the rewriter extracts the field path
from the AST, and Go's own type-checker validates both the field
reference (rename `Source` → `SourceTag` and the compiler flags
every override site) AND the value/fn return type via the unified
generic param `V`. Strings would fail silently on rename; that's why
we don't accept them.

### Nested-field overrides

Multi-hop paths target a nested field directly without forcing the
user to spell out the whole intermediate struct:

```go
type Address    struct { Street, City string }
type AddressDTO struct { Street, City string }
type User       struct { Name string; Address Address }
type UserDTO    struct { Name string; Address AddressDTO }

dto := q.Convert[UserDTO](user,
    q.Set(UserDTO{}.Address.City, "Springfield"), // override one nested field
)
// → UserDTO{
//       Name:    user.Name,
//       Address: AddressDTO{Street: user.Address.Street, City: "Springfield"},
//   }
```

Other nested fields (here `Address.Street`) keep their auto-derived
values from `user.Address`. Mix-and-match works at any depth.

## Source-evaluation discipline

A bare-identifier source splices directly into the literal:

```go
q.Convert[UserDTO](user)
// → UserDTO{ID: user.ID, Name: user.Name}
```

A non-trivial source (call expression, selector chain with side
effects) binds to a per-call `_qSrcN` inside an IIFE so the source
expression evaluates exactly once:

```go
q.Convert[UserDTO](loadUser())
// → func() UserDTO { _qSrcN := loadUser(); return UserDTO{ID: _qSrcN.ID, Name: _qSrcN.Name} }()
```

## What's intentionally not in v2

- **Implicit lifting.** `int → int64`, `*T → T` (deref), `T →
  Option[T]`, etc. require either a runtime helper or an explicit
  override today. v2 stays strict on `types.AssignableTo` so
  surprises are loud.
- **Field renames.** `q.Rename("FooID", "ID")` is a clean follow-up
  if needed; for now use `q.SetFn("ID", func(s Source) int { return
  s.FooID })`.
- **Slice / map / iter conversions.** Recursive auto-derivation
  works on direct struct fields only. For `[]Foo → []Bar` where
  `Foo → Bar` is derivable, write the loop yourself or wait for the
  follow-up.
- **Cross-package targets with unexported fields.** Auto-derivation
  considers only exported fields, so unexported targets in another
  package can't be assembled.

## See also

- [Scala Chimney](https://chimney.readthedocs.io/) — the inspiration.
- [`q.Match`](match.md) — value-returning switch when the source's
  shape is a sum type rather than a struct.
- [`q.Fields`](reflection.md) — exported field names of a struct,
  in case you need them at runtime.
