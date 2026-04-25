# Method generators: `q.GenStringer`, `q.GenEnumJSONStrict`, `q.GenEnumJSONLax`

Package-level "directives" — `var _ = q.GenX[T]()` — that synthesize companion methods on T at compile time. The methods land in a generated `_q_gen.go` file alongside your package; the directive itself rewrites to a no-op so the runtime cost is zero. No `go generate` step, no `stringer` invocation, no method-list to maintain by hand.

## Signatures

```go
// All return q.GenMarker (an empty struct) so `var _ = q.GenX[T]()`
// is type-checkable Go.

func GenStringer[T comparable]() GenMarker
func GenEnumJSONStrict[T comparable]() GenMarker
func GenEnumJSONLax[T comparable]() GenMarker
```

`T` must be a defined named type with constants declared in the same package — same restriction as `q.EnumValues` and friends.

## At a glance

```go
type Color int
const (Red Color = iota; Green; Blue)

type Status string
const (Pending Status = "pending"; Done Status = "done"; Failed Status = "failed")

var _ = q.GenStringer[Color]()
var _ = q.GenEnumJSONStrict[Color]()
var _ = q.GenEnumJSONLax[Status]()
```

After the toolexec pass, the user's package has these methods auto-generated:

```go
// From q.GenStringer[Color]:
func (v Color) String() string { switch v { case Red: return "Red" /* ... */ } return "" }

// From q.GenEnumJSONStrict[Color]:
func (v Color) MarshalJSON() ([]byte, error)   // "Red" / "Green" / "Blue"; errors on unknown
func (v *Color) UnmarshalJSON([]byte) error    // accepts known names; errors on others

// From q.GenEnumJSONLax[Status]:
func (v Status) MarshalJSON() ([]byte, error)  // pass-through to json.Marshal(string(v))
func (v *Status) UnmarshalJSON([]byte) error   // pass-through; preserves any string
```

## Strict vs. Lax — which to pick

| Generator               | Wire format                | Unknown value on unmarshal     | Pairs with `q.Exhaustive`?               |
|-------------------------|----------------------------|--------------------------------|------------------------------------------|
| `GenEnumJSONStrict`     | constant name              | error wrapping `q.ErrEnumUnknown` | Yes (every parsed value is a declared constant) |
| `GenEnumJSONLax`        | underlying value (string/int) | preserved (cast to T verbatim)  | Yes — but `default:` is required to handle the genuinely-unknown values |

**Strict** when wire-drift should crash loudly (your service's contract is the closed enum; anything new is a bug).

**Lax** when forward-compat matters (newer producers shouldn't crash older consumers). Pair with `q.Exhaustive`'s `default:` arm to handle unrecognised values explicitly:

```go
type Status string
const (Pending Status = "pending"; Done Status = "done")
var _ = q.GenEnumJSONLax[Status]()

func handle(s Status) string {
    switch q.Exhaustive(s) {
    case Pending: return "wait"
    case Done:    return "ok"
    default:      return forwardOpaque(s)  // unknown wire value — don't drop it
    }
}
```

Adding a new constant later still triggers the missing-case diagnostic at compile time, even though `default:` is present.

## How the synthesis works

1. **Scanner** walks each file's top-level `*ast.GenDecl` (kind=`var`) for `var _ = q.GenX[T]()` initializers. Each match becomes a `callShape` with form=`formDiscard` and a synthetic `*ast.ExprStmt` wrapping the call.
2. **Typecheck pass** resolves `T` to a `*types.Named`, walks T's declaring package for `*types.Const` of identical type, and populates the shape's `EnumConsts` (names), `EnumConstValues` (constant values as Go-source text), and `EnumUnderlyingKind` (e.g. `"string"`, `"int"`).
3. **Rewriter** substitutes each `q.GenX[T]()` call's source span with `q.GenMarker{}` so the runtime initializer is a no-op (rather than the panic stub the unrewritten body would hit).
4. **File-synthesis pass** (`internal/preprocessor/gen.go`) collects all Gen directives across the package, deduplicates by (family, type), and emits a single `_q_gen.go` to `$TMPDIR`. The file is appended to the compile argv. Imports (`encoding/json`, `fmt`) are also added to the importcfg via the existing `importsToInject` plumbing.

The generated file uses `package <userPkg>`, declares methods on the unqualified type names, and references constants by name — same scope restrictions as `q.EnumName`. Cross-package T is rejected for the same reason.

## What if I forget the directive?

Without `var _ = q.GenX[T]()` somewhere in the package, no methods are synthesized. The user's code that depends on (say) `c.String()` will fail to compile with the standard "type Color has no method String" error from Go. q has no runtime side-effects beyond the rewrites.

## What if the preprocessor doesn't run?

The directive's stub-body calls `panicUnrewritten("q.GenStringer")`, so `var _ = q.GenStringer[T]()` panics at package init. q's link gate catches the missing preprocessor first (the `_q_atCompileTime` symbol fails to resolve at link time), so this panic isn't reachable in practice — but it's the same loud-failure-on-miss principle as every other q.* helper.

## See also

- [`q.EnumValues` / `q.EnumName` / …](enums.md) — value-level enum helpers; `GenStringer`'s output is essentially a method form of `q.EnumName`.
- [`q.Exhaustive`](exhaustive.md) — pairs with `GenEnumJSONLax` for the forward-compat-friendly switch shape (cover all known + `default:` for unknowns).
