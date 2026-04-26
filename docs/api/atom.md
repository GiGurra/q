# `q.Atom` / `q.A` / `q.AtomOf` — typed-string atoms with type-derived values

Erlang-flavoured atoms, adapted to Go's type system: ad-hoc symbolic
constants whose identity *is* their name, with no central declaration
block to maintain.

```go
type Pending q.Atom        // each atom is its own type — no const decl needed
type Done    q.Atom
type Failed  q.Atom

p := q.A[Pending]()        // p has type Pending; value is "Pending"
d := q.A[Done]()           // d has type Done;    value is "Done"
```

## Surface

```go
type Atom string

func A[T ~string]() T          // typed instance: returns T("name-of-T")
func AtomOf[T ~string]() Atom  // q.Atom-typed instance: returns Atom("name-of-T")
```

The preprocessor rewrites both call sites in place at compile time:

```go
q.A[Pending]()       →  Pending("Pending")
q.AtomOf[Pending]()  →  q.Atom("Pending")
```

So the rewritten code is just a typed-string constant cast. Zero
runtime cost; nothing reflective; nothing that runs at startup.

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

Both helpers use the same underlying name — `q.A[Pending]()` and
`q.AtomOf[Pending]()` produce the string `"Pending"`. The difference
is the static type each returns.

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

p := q.A[Pending]()           // p: Pending = "Pending"
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
- **Cross-package collision safety.** The rewriter populates each
  atom's value with its **fully-qualified import path + type name**,
  so two packages defining `type Foo q.Atom` produce distinct atom
  strings (`"github.com/me/a.Foo"` vs `"github.com/me/b.Foo"`) and
  stay distinct at the q.Atom (parent) level. The qualified value
  uses the canonical import path from `go/types`, so import aliases
  at the use site (`import x "github.com/me/a"`) don't affect the
  atom's identity — the canonical underlying type wins.

  The cost is verbosity in `string(atom)` / `fmt.Print(atom)`
  output: `string(q.A[Status]())` returns `"github.com/me/proj.Status"`,
  not `"Status"`. For short readable atom values, plain typed-string
  constants are the right tool.

## See also

- [`q.FnParams` / `q.ValidatedStruct`](fnparams.md) — required-by-default
  struct literals; another way to bring more compile-time discipline
  into the call site.
- [`q.Exhaustive`](exhaustive.md) — for closed-set enums where
  exhaustive switch coverage matters.
- [`q.Match`](match.md) — value-returning switch with multiple branches.
