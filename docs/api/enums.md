# Enums: `q.EnumValues` and family

Compile-time helpers that make Go's de-facto enum pattern (`type X int; const A, B, C X = iota, …`) actually pleasant to use. Every helper rewrites at compile time to a literal slice or an inline switch — no runtime reflection, no companion code-generator step, no method-set bloat unless you opt in.

Works for **any const-able comparable type**: `int` enums (with or without `iota`), `string` enums, `byte` enums, custom-sized integer enums, even bool enums if you're feeling adventurous.

## Signatures

```go
func EnumValues[T comparable]() []T
func EnumNames[T comparable]() []string
func EnumName[T comparable](v T) string
func EnumParse[T comparable](s string) (T, error)
func EnumValid[T comparable](v T) bool
func EnumOrdinal[T comparable](v T) int

var ErrEnumUnknown = errors.New("q: unknown enum value")
```

The type parameter `T` is explicit — Go can't infer it from the value-or-string argument because the helpers also work in zero-arg form (`EnumValues[T]()`).

## At a glance

```go
type Color int
const (
    Red Color = iota
    Green
    Blue
)

q.EnumValues[Color]()      // []Color{Red, Green, Blue}
q.EnumNames[Color]()       // []string{"Red", "Green", "Blue"}

q.EnumName[Color](Green)   // "Green"
q.EnumName[Color](Color(99)) // "" — not a known constant

q.EnumParse[Color]("Green") // (Green, nil)
q.EnumParse[Color]("Pink")  // (Color(0), `"Pink": q: unknown enum value`)

q.EnumValid[Color](Red)         // true
q.EnumValid[Color](Color(99))   // false

q.EnumOrdinal[Color](Blue)      // 2
q.EnumOrdinal[Color](Color(99)) // -1
```

The string form works identically — declaration order is preserved:

```go
type Status string
const (
    Pending Status = "pending"
    Done    Status = "done"
    Failed  Status = "failed"
)

q.EnumValues[Status]()    // []Status{Pending, Done, Failed}
q.EnumNames[Status]()     // []string{"Pending", "Done", "Failed"}
q.EnumName[Status](Done)  // "Done"
```

## What gets generated

Each call site rewrites to one of two shapes:

**Literal slices** (`EnumValues`, `EnumNames`):

```go
colors := q.EnumValues[Color]()
// → colors := []Color{Red, Green, Blue}
```

**Inline IIFE-wrapped switches** (everything else):

```go
name := q.EnumName[Color](c)
// → name := (func(_v Color) string {
//       switch _v {
//       case Red: return "Red"
//       case Green: return "Green"
//       case Blue: return "Blue"
//       }
//       return ""
//   }(c))
```

The Go compiler folds these to direct switches — no closure allocation, no map allocation, no reflection. Generated machine code is identical to a hand-written switch.

## NAME-based parsing

`q.EnumParse[T]` looks up by the **identifier name**, not the underlying value:

```go
q.EnumParse[Status]("Done")    // (Done, nil)         — the constant is Done
q.EnumParse[Status]("done")    // unknown — "done" is the value, not the name
```

This pairs cleanly with `q.EnumName` as a round-trip:

```go
name := q.EnumName[Status](s)        // "Done"
parsed, _ := q.EnumParse[Status](name) // Done
```

For value-based parsing of string-typed enums, write a one-line wrapper:

```go
func ParseStatusValue(s string) (Status, error) {
    v := Status(s)
    if !q.EnumValid[Status](v) {
        return "", q.ErrEnumUnknown
    }
    return v, nil
}
```

## Rolling your own Stringer

Pair `q.EnumName` with a method declaration:

```go
func (c Color) String() string { return q.EnumName[Color](c) }
```

That's it — `Color` now satisfies `fmt.Stringer` and `slog.LogValuer`-friendly contexts, and the call site folds to a direct switch.

For graceful fallback on string-typed enums (where the underlying value may already be readable):

```go
func (s Status) String() string {
    if name := q.EnumName[Status](s); name != "" {
        return name
    }
    return string(s)
}
```

## JSON / text marshalling

Same pattern, three lines:

```go
func (c Color) MarshalText() ([]byte, error) {
    return []byte(q.EnumName[Color](c)), nil
}

func (c *Color) UnmarshalText(b []byte) error {
    parsed, err := q.EnumParse[Color](string(b))
    if err != nil {
        return err
    }
    *c = parsed
    return nil
}
```

`encoding/json` calls these automatically when present, so `Color`-typed fields now serialize as `"Red"`/`"Green"`/`"Blue"` and deserialize the same way.

## How constants are discovered

The preprocessor's typecheck pass walks T's declaring package for `*types.Const` objects whose type is identical to T, in source declaration order. The constant set is computed once at compile time and baked into the rewritten call site.

**Same-package T only.** Cross-package T (e.g. `q.EnumName[other.Color](v)`) surfaces a diagnostic asking for a thin local wrapper:

```go
// In the package that defines Color:
func ColorName(c Color) string { return q.EnumName[Color](c) }

// Cross-package callers use the wrapper.
import other "github.com/x/y"
name := other.ColorName(other.Red)
```

This restriction exists because the rewriter currently writes unqualified constant names; lifting it requires the rewriter to emit qualified identifiers, tracked as a future improvement.

## Exhaustive switches

Wrap the switch tag in `q.Exhaustive` to enforce at compile time that every constant of T appears in some case clause:

```go
switch q.Exhaustive(c) {
case Red:
    return "warm"
case Green:
    return "natural"
case Blue:
    return "cool"
}
```

If you forget a case (say, `Blue`), the build fails with:

```
main.go:42:12: q: q.Exhaustive switch on Color is missing case(s) for: Blue. Add the missing case(s), or use `default:` to opt out.
```

A `default:` clause opts out — the catch-all covers anything missing:

```go
switch q.Exhaustive(c) {
case Red:
    return "red only"
default:
    return "anything else"
}
```

Multi-value cases work: `case Red, Blue:` covers two constants. Switch-with-init works: `switch c := pick(); q.Exhaustive(c) { … }`. The wrapper is stripped at rewrite time, leaving a plain `switch v { … }` — zero runtime overhead.

`q.Exhaustive` is **only** legal as the tag of a switch statement. Found anywhere else (assignment RHS, function arg, return value, …) the scanner surfaces a diagnostic explaining the correct placement.

Cross-package T is rejected for the same reason `q.EnumName` is — declare a thin local wrapper in the enum's home package.

See **[`q.Exhaustive`](exhaustive.md)** for the full reference: how the check resolves constants, what gets enforced, why this shape, and how `default:` opts out.

## Statement forms

Every helper works in any expression position the rest of q supports — define, assign, discard, return, hoist:

```go
v := q.EnumName[Color](c)                                    // define
v  = q.EnumName[Color](c)                                    // assign
       q.EnumName[Color](c)                                  // discard (rare; result wasted)
return q.EnumName[Color](c), nil                             // return
fmt.Printf("%s\n", q.EnumName[Color](c))                     // hoist (nested in any expression)
```

## Caveats

- **No constants of T → diagnostic.** `q.EnumValues[T]()` on a type with no declared constants surfaces `q: q.EnumValues found no constants of type T in package …`. Add at least one `const X T = …` first.
- **Cross-package T → diagnostic.** See above; declare a wrapper in the enum's home package.
- **Built-in types not supported.** `q.EnumValues[int]()` is rejected: `int` has no package scope to walk for constants. Wrap your enum in a defined type (`type Color int`).

## See also

- [`q.As` / `q.AsE`](as.md) — also uses explicit type-arg dispatch.
- The [TODO entry for `q.GenStringer` / `q.GenEnum`](../planning/TODO.md) — opt-in directives that generate the Stringer / JSON methods automatically, layered on top of the helpers above.
