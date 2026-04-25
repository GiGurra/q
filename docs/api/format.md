# String interpolation: `q.F`, `q.Ferr`, `q.Fln`

Compile-time `{expr}` placeholder interpolation. Each call site folds to a `fmt.Sprintf` (or `fmt.Errorf` / `fmt.Fprintln`) with the placeholders lifted out as positional `%v` arguments. No runtime parsing, no template engine, no reflection.

The format string MUST be a Go string literal — dynamic format strings are rejected at scan time. For runtime-built formats, use `fmt.Sprintf` directly.

## Signatures

```go
func F(format string) string
func Ferr(format string) error
func Fln(format string)
```

## At a glance

```go
name := "world"
age  := 42

q.F("hello {name}")              // → fmt.Sprintf("hello %v", name)              → "hello world"
q.F("hi {name}, {age+1} next")   // → fmt.Sprintf("hi %v, %v next", name, age+1) → "hi world, 43 next"
q.F("upper: {strings.ToUpper(name)}") // → fmt.Sprintf("upper: %v", strings.ToUpper(name))

q.Ferr("user {id} not found")    // → fmt.Errorf("user %v not found", id) — type `error`
q.Fln("processing {len(items)}") // → fmt.Fprintln(q.DebugWriter, fmt.Sprintf(...))
```

## Brace escapes

`{{` is a literal `{`, `}}` is a literal `}`:

```go
q.F("literal {{ and }} braces")           // "literal { and } braces"
q.F("{{name}} stays literal")             // "{name} stays literal"
q.F("{{ {name} }}")                       // "{ world }"
```

Percent escapes the same way Go's fmt does — but you don't need to think about it because the rewriter handles it. Source `%` becomes `%%` in the rewritten format:

```go
age := 42
q.F("100% complete, {age}% done")        // "100% complete, 42% done"
```

## What goes inside `{expr}`

Anything that parses as a Go expression in the caller's scope: identifiers, selector chains, arithmetic, function calls, slice indexing — even nested string literals:

```go
q.F("name: {u.Name}")
q.F("sum: {a + b}")
q.F("upper: {strings.ToUpper(s)}")
q.F("first: {items[0]}")
q.F("got: {fmt.Sprintf(\"[%s]\", name)}")  // string literal inside placeholder
```

Inside a placeholder, Go string literals (`"..."`, `'...'`, `` `...` ``) are honoured — braces and quotes inside them don't terminate the placeholder. So `q.F("got {f(\"}\")}")` extracts `f("}")` cleanly.

If a placeholder doesn't parse as a Go expression, the build aborts with a diagnostic naming the offending text.

## `q.Ferr` — error shaped

```go
return q.Ferr("user {id} not found")
// → return fmt.Errorf("user %v not found", id)
```

When there are no placeholders, `q.Ferr` rewrites to `errors.New` instead — saving the `fmt.Errorf` overhead for the trivial case.

`q.Ferr` produces a *fresh* error — it does not wrap one. To wrap, use `q.TryE(...).Wrapf(...)` or `q.F` inline:

```go
err := q.Ferr("loading {id}: %w (note: not actually wrapped)")  // %w not interpreted
err := fmt.Errorf("loading %d: %w", id, baseErr)                 // wrap explicitly
```

## `q.Fln` — debug print

Writes the interpolated string + newline to `q.DebugWriter` (defaults to `os.Stderr`). Route for ad-hoc diagnostics that don't warrant a full `slog` setup.

```go
q.Fln("processing {len(items)} items for user {user.Name}")
// → fmt.Fprintln(q.DebugWriter, fmt.Sprintf("processing %v items for user %v", len(items), user.Name))
```

Tests can redirect `q.DebugWriter` to a `bytes.Buffer` to capture output deterministically.

## Tradeoff: identifiers inside the literal aren't IDE-visible

`q.F("hi {name}")` — the IDE doesn't see `name` as a referenced identifier. Go-to-definition, rename, and unused-variable detection don't apply to identifiers that exist only inside the format literal. The compiler still catches typos (the rewritten `fmt.Sprintf(..., name)` references `name` directly, so a missing variable fails the build), but the editor experience is degraded vs. plain `fmt.Sprintf` with explicit args.

If you rely heavily on rename refactoring across a codebase, keep performance-critical or naming-volatile code on plain `fmt.Sprintf`. Reach for `q.F` for log lines and error messages where the ergonomic win is largest.

## Statement forms

Every helper works in any expression position the rest of q supports — define, assign, discard, return, hoist:

```go
msg := q.F("hi {name}")                              // define
msg  = q.F("hi {name}")                              // assign
       q.F("hi {name}")                              // discard (rare; result wasted)
return q.F("hi {name}"), nil                         // return
log.Println(q.F("hi {name}"))                        // hoist (nested in any expression)
```

## See also

- [`q.SQL`](sql.md) — same `{expr}` syntax but rewrites to placeholder-style parameterised SQL (`?`, `$1`, or `:name`) so user-supplied values never get inlined into the query string. *(Coming next.)*
- [`q.DebugPrintln` / `q.DebugSlogAttr`](debug.md) — `dbg!`-style prints that auto-capture the source text of the value as the label, instead of taking a format.
