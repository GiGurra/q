# `q.Atom` / `q.A` / `q.AtomOf` — typed-string atoms with type-derived values

Erlang-flavoured atoms, adapted to Go's type system: ad-hoc symbolic
constants whose identity *is* their name, with no central declaration
block to maintain.

```go
type Pending q.Atom        // each atom is its own type — no const decl needed
type Done    q.Atom
type Failed  q.Atom

// In package "github.com/me/proj":
p := q.A[Pending]()        // p has type Pending; value is "github.com/me/proj.Pending"
d := q.A[Done]()           // d has type Done;    value is "github.com/me/proj.Done"
```

## Surface

```go
type Atom string

func A[T ~string]() T          // typed instance: returns T("<importPath>.<TypeName>")
func AtomOf[T ~string]() Atom  // q.Atom-typed instance: returns Atom("<importPath>.<TypeName>")
```

The preprocessor rewrites both call sites in place at compile time
to a fully-qualified typed-string cast — the import path of the
declaring package plus the bare type name:

```go
// In package "github.com/me/proj":
q.A[Pending]()       →  Pending("github.com/me/proj.Pending")
q.AtomOf[Pending]()  →  q.Atom("github.com/me/proj.Pending")
```

So the rewritten code is just a typed-string constant cast. Zero
runtime cost; nothing reflective; nothing that runs at startup.

The fully-qualified value is the **load-bearing wire identity** for
each atom — see [Atom values are import-path-qualified](#atom-values-are-import-path-qualified)
below for the design rationale and the tradeoffs around
serialization.

## What you get

- **Each atom is its own type.** Go's type system protects against
  mixing — you can't pass a `Pending` where a `Done` is expected.
- **No declaration boilerplate.** `type Pending q.Atom` is the entire
  declaration; the value is auto-derived.
- **Decentralized.** Different files / packages can introduce new
  atoms without touching a shared list.
- **Cross-package equality is automatic.** Two atoms with the same
  name in different packages compare equal via plain string equality
  (modulo the type-distinction safety net — see below).

## When to use which

| Scenario | Reach for |
|---|---|
| Typed value to pass / store / return | `q.A[T]()` — preserves type distinction |
| Switch-case expression on a `q.Atom`-typed value | `q.AtomOf[T]()` — pre-cast to the parent type |
| Map key (typed) | `q.A[T]()` |
| Constant comparison with a `q.Atom` | `q.AtomOf[T]()` |

Both helpers use the same underlying value — `q.A[Pending]()` and
`q.AtomOf[Pending]()` produce the same fully-qualified string
(`"github.com/me/proj.Pending"`). The difference is the static type
each returns.

## Examples

```go
type Pending q.Atom
type Done    q.Atom
type Failed  q.Atom

// Type-distinct values — function only accepts Pending atoms:
func ack(p Pending) string {
    if p == q.A[Pending]() {
        return "still pending"
    }
    return "unexpected"
}

// In package "github.com/me/proj":
p := q.A[Pending]()           // p: Pending = "github.com/me/proj.Pending"
ack(p)                        // OK
// ack(q.A[Done]())           // compile error — type mismatch

// Switch over the parent q.Atom type:
func classify(a q.Atom) string {
    switch a {
    case q.AtomOf[Pending]():
        return "p"
    case q.AtomOf[Done]():
        return "d"
    case q.AtomOf[Failed]():
        return "f"
    }
    return "?"
}

// Atoms as map keys:
counts := map[Pending]int{q.A[Pending](): 0}
counts[q.A[Pending]()]++
```

## Atom values are import-path-qualified

Every atom's runtime value is its declaring package's full import
path + the bare type name, separated by a dot:

```go
// In package "github.com/me/proj":
type Status q.Atom
s := q.A[Status]()
fmt.Println(string(s))         // "github.com/me/proj.Status"
```

This is the load-bearing identity. Two packages defining `type Foo q.Atom`
produce distinct atom strings — `"github.com/me/a.Foo"` vs
`"github.com/me/b.Foo"` — and stay distinct at the q.Atom (parent)
level. The qualified value uses the canonical import path from
`go/types`, so import aliases at the use site
(`import x "github.com/me/a"`) don't affect the atom's identity — the
canonical underlying type always wins.

This is what enables atoms to be **decentralized but globally unique**:
any package can declare new atoms without coordinating with a central
registry, and equality is well-defined across the whole binary.

### Implications for serialization (JSON / wire formats)

Atoms work well as in-process tags. They are **not** designed as
wire-format identifiers without explicit thought:

- **JSON marshalling is automatic** because atoms have a string
  underlying type — `json.Marshal` writes the atom as a quoted
  string. But the string the wire sees is the fully-qualified path:
  `"github.com/me/proj.Pending"`, not `"Pending"`. Producers and
  consumers of that JSON must agree on the import path of the
  declaring package, including its version.
- **Refactoring breaks the wire.** Renaming the package, moving the
  type to a sibling package, or moving the module to a different
  module path changes the atom's value — and therefore the JSON
  serialization. If a consumer hard-coded the previous string, it
  breaks silently.
- **Cross-language interop is awkward.** A Python client that wants
  to send `Pending` would have to know to send
  `"github.com/me/proj.Pending"` — surprising and version-coupled.

For wire-format use cases, two patterns work better than raw
atom strings:

1. **Wrap the atom in a custom MarshalJSON.** Define `MarshalJSON` /
   `UnmarshalJSON` on each atom type to translate to/from the bare
   name (`"Pending"`) at the wire boundary. Decentralised internally,
   centralised at the wire.

   ```go
   func (Pending) MarshalJSON() ([]byte, error)   { return []byte(`"Pending"`), nil }
   func (*Pending) UnmarshalJSON(b []byte) error  { /* validate b == "Pending" */ }
   ```

2. **Use plain typed-string constants for wire-bound enums.**
   Closed-set enums + `q.GenEnumJSONStrict` / `q.GenEnumJSONLax`
   give marshallers, parser, exhaustive coverage. See
   [`q.Exhaustive`](exhaustive.md) and [`q.GenEnumJSON*`](gen.md) for
   the closed-set toolchain. Atoms are the open-set sibling; reach
   for the closed-set tools when wire identity is part of the contract.

The verbose value also shows up in `fmt.Print(atom)` output:
`string(q.A[Status]())` returns `"github.com/me/proj.Status"`, not
`"Status"`. For short readable atom values where the import-path
identity isn't needed, plain typed-string constants are the right
tool.

## Caveats

- **Open set.** There's no compile-time check that you've handled
  every atom in a switch — atoms are ad-hoc by design. For
  *closed-set* enums use a typed const block + `q.Exhaustive` instead.
- **Constraint is `~string`, not `~Atom`.** Go's type-name unwrapping
  means `type Pending q.Atom` has underlying type `string` (not
  `Atom`); a `~Atom` constraint would only match the bare `Atom`
  type, not user-derived ones. `~string` is the next-best option.
  The preprocessor compensates: it validates at compile time that T
  is a *named* type with `string` underlying, rejecting bare
  `q.A[string]()` or non-string types with directed diagnostics.
  Strict "T transitively derives from q.Atom in its TypeSpec chain"
  validation is a future extension — for now any named string-typed
  type is accepted.

## See also

- [`q.FnParams` / `q.ValidatedStruct`](fnparams.md) — required-by-default
  struct literals; another way to bring more compile-time discipline
  into the call site.
- [`q.Exhaustive`](exhaustive.md) — for closed-set enums where
  exhaustive switch coverage matters.
- [`q.Match`](match.md) — value-returning switch with multiple branches.
