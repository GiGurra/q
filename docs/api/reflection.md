# Compile-time reflection: `q.Fields`, `q.AllFields`, `q.TypeName`, `q.Tag`

Replace runtime `reflect` for the common "give me the field names / type name / struct tag" cases. Each call site folds to a literal at compile time. Useful for codegen-free JSON / CSV / SQL row mappers, schema-derived helpers, and other small cases where pulling in `reflect` is overkill.

## Signatures

```go
func Fields[T any]() []string
func AllFields[T any]() []string
func TypeName[T any]() string
func Tag[T any](field, key string) string
```

`T` is a type parameter. For struct-shaped helpers (`Fields`, `AllFields`, `Tag`), pointer indirection is followed — `Fields[*User]()` and `Fields[User]()` produce the same result.

## At a glance

```go
type User struct {
    ID    int    `json:"id"   db:"user_id"`
    Name  string `json:"name,omitempty" db:"full_name"`
    Email string `json:"email"`
    pwd   string // unexported
}

q.Fields[User]()          // []string{"ID", "Name", "Email"} — exported only
q.AllFields[User]()       // []string{"ID", "Name", "Email", "pwd"} — every field
q.TypeName[User]()        // "User"
q.TypeName[*User]()       // "User"
q.Tag[User]("ID", "json") // "id"
q.Tag[User]("ID", "db")   // "user_id"
q.Tag[User]("Name", "db") // "full_name"
q.Tag[User]("Email", "db")// "" — key absent (matches reflect.StructTag.Get)
```

## How it folds

The typecheck pass resolves `T` via `go/types`, walks the struct's `*types.Struct.Field(i)`/`Tag(i)`, and stores the result on the call's `qSubCall` (`StructFields` for the field-listing helpers, `ResolvedString` for `TypeName` / `Tag`). The rewriter splices the resolved value as a literal at the call site:

```go
// Source:
cols := q.Fields[User]()

// Rewritten:
cols := []string{"ID", "Name", "Email"}
```

The Go compiler treats the result like any other slice literal — it can be folded to read-only memory, range-over-without-allocation, etc.

## Use cases

### Codegen-free SQL row mapper

```go
type User struct {
    ID    int    `db:"user_id"`
    Name  string `db:"full_name"`
    Email string `db:"email"`
}

func selectUser(id int) (User, error) {
    cols := []string{
        q.Tag[User]("ID", "db"),
        q.Tag[User]("Name", "db"),
        q.Tag[User]("Email", "db"),
    }
    query := q.PgSQL("SELECT " + cols[0] + ", " + cols[1] + ", " + cols[2] + " FROM users WHERE id = {id}")
    // ... db.QueryRowContext(ctx, query.Query, query.Args...).Scan(...)
}
```

### Type-aware error messages

```go
err := decode(b)
return q.Ferr("decoding {q.TypeName[User]()}: {err}")
// → "decoding User: <err>"
```

### Auto-generated marshaller

Pair with `q.Fields` to walk a struct without `reflect`:

```go
func encode[T any](v T) map[string]any {
    out := map[string]any{}
    for _, name := range q.Fields[T]() {
        // ... build out[name] using a per-field switch
    }
    return out
}
```

(In practice you'd want `q.Tag` for the JSON name and a per-field switch on the value path. The full pattern lives in your code; q just hands you the names.)

## Restrictions

- **Field-listing helpers require a struct type.** Calling `q.Fields[int]()` is a build error.
- **`q.Tag`'s arguments must be Go string literals.** Dynamic field/key strings would need runtime resolution — defeats the compile-time fold.
- **The named field must exist.** `q.Tag[User]("Pwd", "json")` (typo for `pwd`) fails the build with `field "Pwd" not found on the struct`.
- **Cross-package types are supported via the type system.** Unlike enums, `q.Fields[otherpkg.Foo]()` works fine — the type-arg expression resolves through `go/types` regardless of package, and the rewritten output is just a string-literal slice.

## See also

- [`q.EnumValues` / `q.EnumName` / …](enums.md) — value-level reflection for enum types (constant lists, name lookup, parse).
- [`q.Snake` / `q.Camel` / …](string_case.md) — pair with `q.Fields` to derive column / tag names from Go field names.
- [`q.SQL` / `q.PgSQL` / …](sql.md) — pair with `q.Tag` to build parameterised queries from struct metadata.
