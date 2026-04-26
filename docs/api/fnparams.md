# `q.FnParams` / `q.ValidatedStruct` — named arguments for constructors

**The goal: named parameters in Go.** Languages with named arguments
give you two wins for free — readability ("I can see at the call site
what each value means") and safety ("the compiler tells me if I
forgot a required argument"). Go's "options struct" pattern delivers
the readability win but loses the safety one — Go can't distinguish
"explicitly set this field to its zero value" from "didn't set it at
all," so a typo or omission silently becomes a zero-valued default.

`q.FnParams` (and its general-purpose sibling `q.ValidatedStruct`)
closes the gap. Add the marker as a blank field, and every field is
required-by-default; mark the optional ones with a `q:"optional"`
(or `q:"opt"`) tag. With the marker in place, omitting a required
field becomes a compile-time error — the same thing a `func
F(name: required, age: optional)` named-argument syntax would buy
you, achieved through the standard struct-literal-options pattern
plus a preprocessor pass.

```go
type LoadOptions struct {
    _       q.FnParams
    Path    string                                 // required
    Format  string                                 // required
    Timeout time.Duration `q:"optional"`           // optional
    Logger  *slog.Logger  `q:"opt"`                // optional (short form)
}

Load(LoadOptions{Path: "/etc", Format: "yaml"})    // OK
Load(LoadOptions{Path: "/etc"})                     // build error: Format required
Load(LoadOptions{})                                 // build error: Path, Format required
```

## Surface

```go
type FnParams        struct{}    // marker for function-parameter structs
type ValidatedStruct struct{}    // marker for any struct (DTOs, configs, models, …)
```

Both are zero-size empty structs with **identical** validation
semantics; pick whichever name reads best at the use site. Use
`q.FnParams` when the struct is a function parameter; use
`q.ValidatedStruct` for any other required-by-default struct (DTO,
configuration object, model, builder internals).

The marker field adds no bytes to the struct layout; it exists only
to flag the type at the go/types level.

## Optional-field tag spelling

`q:"optional"` and `q:"opt"` are interchangeable. Pick whichever
reads better in the surrounding tag stack.

```go
type Mixed struct {
    _ q.ValidatedStruct
    A string                       // required
    B int    `q:"opt"`             // optional
    C bool   `q:"optional"`        // optional, equivalent to q:"opt"
}
```

## What the preprocessor does

For every `*ast.CompositeLit` in your package, the preprocessor:

1. Resolves the literal's static type via go/types.
2. Walks the type's struct fields, looking for a blank field whose
   type is `q.FnParams`.
3. If the marker is present, builds the required-fields set: every
   *named* (non-blank) field that does *not* carry a `q:"optional"`
   tag.
4. Checks the literal's keyed fields against the required set. Each
   missing required field becomes a preprocess-time diagnostic; the
   build fails before code generation.

## Properties

- **Opt-in.** Plain structs (no marker) are not validated. Existing
  code keeps its existing behaviour.
- **Required-by-default.** Once a struct opts in, fields are required
  unless explicitly tagged optional. Adding a new field to the
  struct without a tag immediately requires every call site to
  include it — exactly what you want for "I added this and forgot
  to update callers" detection.
- **Compile-time only.** The validation runs at preprocess time;
  zero runtime cost. The marker type is genuinely a zero-size empty
  struct.
- **Cross-package types work.** go/types resolves struct fields and
  tags transparently across module / package boundaries.
- **Nested literals are validated.** A marked struct used as a
  field type, pointer field, slice element, or map value is checked
  the same way as a top-level literal — the preprocessor walks every
  `CompositeLit` in the package, so nesting falls out for free.
- **Lower tag noise than alternatives.** In real param structs most
  fields are required; tagging the minority (optionals) is shorter
  than tagging the majority (the "mark required" alternative).

## Hard limit (deliberately accepted)

Only struct *literals* at their construction site are checked. Code
like:

```go
p := LoadOptions{Path: "/etc"}   // missing Format — diagnostic fires HERE
Load(p)                          // not re-checked at the call site
```

is checked at the literal's construction (the `:=` line). Code that
constructs a struct via flow that the preprocessor can't see —
function returns, multi-statement assembly, deserialization,
`reflect`-based construction — bypasses the check. That's
intentional: we don't do data-flow analysis. Layer your own runtime
check or `golangci-lint`'s `exhaustruct` if you need stricter
coverage.

## Examples

### Mixed required and optional

```go
type ConnectOptions struct {
    _              q.FnParams
    Host           string                          // required
    Port           int                             // required
    Database       string                          // required
    DialTimeout    time.Duration `q:"optional"`    // optional, default 0
    EnableTLS      bool          `q:"optional"`    // optional, default false
    ConnectionPool int           `q:"optional"`    // optional, default 0
}

// Required-only call site:
db := connect(ConnectOptions{
    Host: "db.example.com", Port: 5432, Database: "prod",
})

// With optionals:
db := connect(ConnectOptions{
    Host: "db.example.com", Port: 5432, Database: "prod",
    DialTimeout: 10 * time.Second,
    EnableTLS:   true,
})
```

### Positional literals pass through

```go
ConnectOptions{"db", 5432, "prod", 0, false, 0}  // every field set; OK
```

Positional literals set every field by construction, so the
keyed-field check doesn't apply. (The Go compiler already complains
if you omit positional values for visible fields, so this is
naturally safe.)

### Nested marked structs

```go
type DBOptions struct {
    _    q.FnParams
    Host string
    Port int
    TLS  bool `q:"optional"`
}

type ServerOptions struct {
    _        q.FnParams
    Bind     string
    DB       DBOptions             // value-nested marked struct
    Backup   *DBOptions            `q:"optional"`
    Replicas []DBOptions           `q:"optional"`
    NamedDBs map[string]DBOptions  `q:"optional"`
}

// Every inner literal is validated, including elided-type forms in
// slice / map / pointer positions:
Server(ServerOptions{
    Bind: ":8080",
    DB:   DBOptions{Host: "primary", Port: 5432},
    Replicas: []DBOptions{
        {Host: "r1", Port: 5440},          // checked
        {Host: "r2", Port: 5441, TLS: true},
    },
    NamedDBs: map[string]DBOptions{
        "prod": {Host: "p", Port: 5500},   // checked (elided type)
    },
})

// MISSING the inner Port — fails the build:
Server(ServerOptions{
    Bind: ":8080",
    DB:   DBOptions{Host: "primary"},  // diagnostic: Port required
})
```

### Migrating an existing struct

When you add the marker to an existing struct that has callers, the
build will immediately fail at every call site that omitted any field.
Tag fields that were *meant* to be optional with `q:"optional"`, then
audit and update the rest. This is the migration path: the
preprocessor surfaces the gap; you decide what was an oversight vs.
what was an intentional default.

## Caveats

- **Reflection-based construction skips the check.**
  `reflect.New(T)` then field-by-field setting bypasses go/types
  visibility. Use `q.FnParams` for the literal-construction discipline,
  not for runtime invariant enforcement.
- **Struct embedding.** Embedding a struct that has the marker does
  *not* propagate required-by-default semantics to the outer struct.
  The marker is type-level — only the struct that *directly* contains
  `_ q.FnParams` is validated.
- **Generic structs.** Each instantiation is a distinct type; the
  marker on the generic struct definition applies to every
  instantiation. Validation runs against the instantiated field set.

## Why "named arguments for constructors" is the right framing

Go's "options struct" pattern is a workaround for the absence of
named arguments. It gets you the readability win (each field is
named at the call site) but loses the safety win (the compiler
can't tell which fields the caller actually meant to supply).

`q.FnParams` / `q.ValidatedStruct` close that gap: with the marker
in place, omitting a required field becomes a compile-time error,
not a silent zero-value default. That's the "named args for ctors"
shape Go's syntax has never supported directly.

## Future directions

The current pass enforces a single rule — "every named field must
be keyed unless tagged optional." The same machinery (walk every
CompositeLit, read tags via go/types, emit diagnostics) generalises
to a richer set of literal-construction invariants:

- **Bounds and shape constraints.** `q:"min=0,max=100"`,
  `q:"len>0"`, `q:"nonzero"`. Constant-folded literals get checked
  at compile time; non-constant values fall through to runtime guards
  (or stay un-checked if the user prefers).
- **Enum / set membership.** `q:"in:foo,bar,baz"` on a string
  field, validated against literal values at compile time. Unifies
  with `q.Exhaustive` for the named-constant case.
- **Mutual exclusion / co-dependency.**
  `q:"oneof:FieldA,FieldB,FieldC"` (exactly one must be keyed),
  `q:"requires:OtherField"` (if this is keyed, OtherField must be
  too). Useful for variant-shape options structs that don't quite
  warrant a sum type.
- **Format hints.** `q:"url"`, `q:"email"`, `q:"uuid"`, `q:"regex=..."`.
  Compile-time validation when the value is a literal constant;
  runtime check otherwise.
- **Cross-field comparisons.** `q:"lessThan:OtherField"`,
  `q:"after:StartTime"`. Same compile-time vs runtime split as
  bounds checks.
- **Conditional requiredness.** `q:"required-if=OtherField"` —
  this field is required only when OtherField is also set. Captures
  the common "if you opted into feature X, you must supply Y too"
  pattern.

The goal is the same throughout: surface the constraint at the
literal site so a misuse fails the build instead of slipping through
to runtime. Each addition is a self-contained pass extension; ship
them one at a time as users ask.

## See also

- [Design](../design.md) — the rewriter's contract and why parse-not-template.
- `golangci-lint`'s `exhaustruct` — strictly enforces "every field
  must be set" for tagged types. The q markers are the selective
  variant: only truly required fields fail-loud, optionals get an
  explicit opt-out.
