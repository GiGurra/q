# `q.FnParams` — required-by-default parameter structs

Go's `func F(opts MyOpts)` pattern is the standard alternative to
named arguments — but Go can't tell whether a caller "explicitly set
this field to its zero value" or "didn't set it at all," so the
"required field" check that languages with named arguments give you
for free has no clean shape in pure Go.

`q.FnParams` is the opt-in marker that flips the polarity: every
field is required-by-default; mark the optional ones with a
`q:"optional"` tag.

```go
type LoadOptions struct {
    _       q.FnParams
    Path    string                                 // required
    Format  string                                 // required
    Timeout time.Duration `q:"optional"`           // optional
    Logger  *slog.Logger  `q:"optional"`           // optional
}

Load(LoadOptions{Path: "/etc", Format: "yaml"})    // OK
Load(LoadOptions{Path: "/etc"})                     // build error: Format required
Load(LoadOptions{})                                 // build error: Path, Format required
```

## Surface

```go
type FnParams struct{}
```

A zero-size empty struct. Use it as the type of a blank field
(`_ q.FnParams`) on any struct you want validated. The field adds no
bytes to the struct layout; it exists only to mark the type at the
go/types level.

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

## See also

- [Design](../design.md) — the rewriter's contract and why parse-not-template.
- `golangci-lint`'s `exhaustruct` — strictly enforces "every field
  must be set" for tagged types. `q.FnParams` is the selective
  variant: only truly required fields fail-loud, optionals get an
  explicit opt-out.
