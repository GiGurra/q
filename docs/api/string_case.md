# Compile-time string case: `q.Upper` / `q.Lower` / `q.Snake` / `q.Kebab` / `q.Camel` / `q.Pascal` / `q.Title`

Take a string literal, fold to a string literal at compile time. Useful for the codegen-adjacent stuff Go forces you to type out: column names, env var keys, URL slugs, JSON field names. No runtime cost — each call site is a constant after the rewrite.

## Signatures

```go
func Upper(s string) string
func Lower(s string) string
func Snake(s string) string
func Kebab(s string) string
func Camel(s string) string
func Pascal(s string) string
func Title(s string) string
```

The argument must be a Go string literal — dynamic strings are rejected at scan time. There's no point folding a runtime value at compile time; reach for the standard `strings` package for those.

## At a glance

```go
q.Upper("hello")            // "HELLO"
q.Lower("HELLO")            // "hello"
q.Snake("HelloWorld")       // "hello_world"
q.Snake("XMLHttpRequest")   // "xml_http_request"
q.Snake("hello-world")      // "hello_world"
q.Kebab("HelloWorld")       // "hello-world"
q.Camel("hello_world")      // "helloWorld"
q.Camel("XMLHttpRequest")   // "xmlHttpRequest"
q.Pascal("hello_world")     // "HelloWorld"
q.Pascal("XML_HTTP_REQUEST") // "XmlHttpRequest"
q.Title("hello world")      // "Hello World"
```

## Tokenisation rules

The `Snake` / `Kebab` / `Camel` / `Pascal` family splits the input into words, then joins them with the chosen separator and capitalisation. Word boundaries:

- Runs of separator characters (`_`, `-`, ` `, `.`, `/`) split.
- Lowercase → uppercase starts a new word: `helloWorld` → `hello`, `World`.
- Uppercase-run-followed-by-lowercase ends the run: `XMLHttp` → `XML`, `Http`. So `XMLHttpRequest` becomes three words: `XML`, `Http`, `Request`.
- Digits stick with the adjacent letter cluster: `v2Beta` → `v2`, `Beta`.

`Title` is the special case: it splits **only on space** and capitalises the first letter of each word, preserving everything else. Useful for human-readable headings; not appropriate for identifier-shaped output.

## Use cases

```go
// Generated SQL column names from a Go field name:
const userIDColumn = q.Snake("UserID")  // "user_id"

// Env vars from a config struct field:
const dbHostEnv = q.Upper(q.Snake("DBHost"))  // "DB_HOST"

// URL slugs from a title:
url := "/posts/" + q.Kebab("My First Post")   // "/posts/my-first-post"

// JSON field names from Go identifiers:
fmt.Println(q.Camel("user_id"))              // "userId"

// Matching a Stringer's output:
fmt.Println(q.Pascal(q.EnumName[Color](c)))  // already PascalCase
```

## Why compile-time?

The transformations are deterministic and the inputs are known at compile time, so there's no reason to do the work at runtime. The rewriter:

1. Validates the argument is a `*ast.BasicLit` of kind `STRING`.
2. Unquotes the literal text.
3. Runs the family-specific transform.
4. Re-quotes and substitutes at the call site.

The result is a `string` constant the Go compiler can use anywhere — including in `const` declarations (since q rewrites at the AST level before const evaluation):

```go
const dbColumn = q.Snake("UserID")  // valid: q.Snake folds to "user_id" before const-checking
```

Wait, no — `const` initializers in plain Go cannot contain function calls. But the rewriter happens BEFORE the const-evaluation pass, so by the time the compiler sees the file, `q.Snake("UserID")` has already become `"user_id"` — the const is valid. (The IDE will still highlight it as an error pre-rewrite, since gopls runs before toolexec. Same caveat as the rest of q's helpers.)

## Implementation notes

See `internal/preprocessor/strings.go` for the tokeniser. The split rules above are encoded in `splitWords`, which is shared across `Snake`, `Kebab`, `Camel`, and `Pascal`. `Title` uses its own simpler split (space-only) since it preserves intra-word case.

## See also

- [`q.F`](format.md) — string interpolation, also folds at compile time.
- [`q.SQL`](sql.md) — pairs nicely with `q.Snake` for `column = ?`-style queries built from Go field names.
