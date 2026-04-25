# Exhaustive switches: `q.Exhaustive`

Compile-time enforcement that a `switch` covers every constant of the matched type. Wrap the switch tag in `q.Exhaustive(v)`; the typecheck pass walks `v`'s defined type for `*types.Const` declarations and walks the switch's case clauses, then aborts the build if any constant is missing. The wrapper is stripped at rewrite time, so the runtime is a plain `switch v { … }` with **zero overhead**.

## Signature

```go
func Exhaustive[T any](v T) T
```

The function is a pass-through at the type level (`func[T](T) T`), so the IDE and `go vet` see ordinary Go: the switch's tag has the same type whether you write `switch v` or `switch q.Exhaustive(v)`. The compile-time check is a build-pass, not a type-system feature.

## At a glance

```go
type Color int
const (Red Color = iota; Green; Blue)

func describe(c Color) string {
    switch q.Exhaustive(c) {
    case Red:   return "warm"
    case Green: return "natural"
    case Blue:  return "cool"
    }
    return "unknown"
}
```

If you forget a case (say, `Blue`), the build fails:

```
main.go:42:12: q: q.Exhaustive switch on Color is missing case(s) for: Blue. Add the missing case(s), or use `default:` to opt out.
```

## How the check works

1. Resolve the type of the wrapped expression `v` via `go/types`. Must be a defined named type with constants declared in the same package — built-ins (`int`, `string`) and cross-package types are rejected.
2. Walk the package's scope for `*types.Const` whose type is identical to `v`'s type. That's the **expected set** of constants.
3. Walk the switch's case clauses, resolving each case expression to a `*types.Const` via `info.Uses` (handles bare `Red`, qualified `pkg.Red`, parenthesised `(Red)`).
4. The **covered set** is the union of all such constants across every case clause (multi-value cases like `case A, B, C:` count as three).
5. Any constant in the expected set that's not in the covered set is reported in a single diagnostic, sorted alphabetically.

### `default:` does not replace coverage

A `default:` clause **catches values outside the declared set** — runtime drift, forward-compat with Lax-JSON-opted types, future enum additions a downstream service hasn't adopted yet. It does **not** substitute for covering the known constants:

```go
switch q.Exhaustive(c) {
case Red:   return "red"
case Green: return "green"
default:    return "fallback"  // ← does NOT cover Blue
}
// → build fails: missing case(s) for: Blue
```

To pass, every declared constant needs its own case (or a multi-value `case A, B:`); `default:` is then optional, additive, and recommended for any type that can carry unknown values:

```go
switch q.Exhaustive(c) {
case Red:   return "red"
case Green: return "green"
case Blue:  return "blue"
default:    return "unknown"  // for forward-compat with newer producers
}
```

This keeps the promise honest: "every declared constant has a dedicated arm; unknown drift goes through default."

## What gets enforced

| Source                                                       | Behaviour                                                                 |
|--------------------------------------------------------------|---------------------------------------------------------------------------|
| `switch q.Exhaustive(c) { … all declared cases … }`          | Build passes.                                                             |
| `switch q.Exhaustive(c) { … missing one … }`                 | Build fails: `missing case(s) for: <names>`.                              |
| `switch q.Exhaustive(c) { … missing one …; default: … }`     | Build still fails — `default:` is for unknown values, not declared ones.  |
| `switch q.Exhaustive(c) { … all declared …; default: … }`    | Build passes; default catches values outside the declared set.            |
| `switch q.Exhaustive(c) { case A, B: …; case C: … }`         | Multi-value cases count as covering each value.                           |
| `switch x := f(); q.Exhaustive(x) { … }`                     | Switch-with-init works.                                                   |
| `q.Exhaustive(c)` outside a switch tag                       | Build fails: `q.Exhaustive can only be used as the tag of a switch`.      |
| `q.Exhaustive(123)`, `q.Exhaustive("foo")`                   | Build fails: type isn't a defined named type.                             |
| `q.Exhaustive(otherpkg.Color(c))`                            | Build fails: cross-package type — declare a wrapper in the home package.  |

## Why this shape (and not the alternatives)

- **`switch q.Exhaustive(v) { … }`** ✓ — parses as plain Go (a function call in the tag position). Type-checks under `gopls`. The wrapper is a real generic function the user can read in the source. The check is a single AST pattern the rewriter recognises.
- **`switch v.exhaustive() { … }`** ✗ — would require methods on every enum type (or interface satisfaction). Doesn't work for built-in-int-backed enums without method declarations. Adds noise.
- **`//q:exhaustive` comment above the switch** ✗ — comments are easy to miss, hard for the user to spot in reviews. Function calls are loud.
- **Type-system enforcement (sealed types)** ✗ — Go doesn't have sealed types; can't be added without a language change.

The function-wrapper shape was the user's suggestion and is the cleanest of the candidates considered.

## Cross-package types

A switch on a type declared in another package must be declared inside that package:

```go
// in package colors:
package colors

type Color int
const (Red Color = iota; Green; Blue)

func Describe(c Color) string {
    switch q.Exhaustive(c) {
    case Red:   return "warm"
    case Green: return "natural"
    case Blue:  return "cool"
    }
    return "unknown"
}

// elsewhere:
import "your/colors"
fmt.Println(colors.Describe(colors.Red))
```

The rewriter currently writes case names unqualified; lifting the cross-package restriction would require it to emit `colors.Red`, `colors.Green`, etc. Tracked as a future enhancement.

## Forward-compatibility (Lax JSON / wire drift)

When a type is opted into a Lax JSON marshaller (so the wire can carry values outside the declared set — e.g. a service that hasn't adopted a new enum value yet), a `default:` arm is required to handle the genuinely-unknown values. The declared cases still must each be present:

```go
type Color int
const (Red Color = iota; Green; Blue)
var _ = q.GenEnumJSONLax[Color]()  // (planned, see TODO)

switch q.Exhaustive(c) {
case Red:   return "red"
case Green: return "green"
case Blue:  return "blue"
default:
    // c carries a value outside Red/Green/Blue (e.g. an unfamiliar
    // wire value from a newer producer). Log, forward, or fall back.
    return forwardUnknown(c)
}
```

This is the "open type at the boundary, closed type internally" pattern made compile-time-checked. New constants added later still trigger the missing-case diagnostic — `default:` doesn't silently swallow them.

## See also

- [`q.EnumValues` / `q.EnumName` / …](enums.md) — the value-level enum helpers `q.Exhaustive` is a sibling of.
