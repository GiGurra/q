# Injection-safe SQL: `q.SQL`, `q.PgSQL`, `q.NamedSQL`

A specialised cousin of [`q.F`](format.md): same `{expr}` interpolation surface, but rewrites to placeholder-style **parameterised SQL**. User-supplied values are never inlined into the query string — the rewriter physically can't produce that shape, so the safety guarantee is structural, not advisory.

The format string MUST be a Go string literal. Allowing a dynamic format would re-open the SQL-injection hole the helper exists to close.

## Signatures

```go
type SQLQuery struct {
    Query string
    Args  []any
}

func SQL(format string) SQLQuery       // ?, ?, ?     — SQLite, MySQL, plain database/sql
func PgSQL(format string) SQLQuery     // $1, $2, $3  — lib/pq, pgx
func NamedSQL(format string) SQLQuery  // :name1, :name2, :name3 — sqlx, named-param drivers
```

## At a glance

```go
id, status := 42, "active"
name := "alice'; DROP TABLE users; --"  // injection attempt

// q.SQL — `?` placeholders (most portable)
s := q.SQL("SELECT * FROM users WHERE id = {id} AND status = {status}")
// s.Query → "SELECT * FROM users WHERE id = ? AND status = ?"
// s.Args  → []any{42, "active"}
db.QueryRowContext(ctx, s.Query, s.Args...)

// q.PgSQL — Postgres-style `$N`
s2 := q.PgSQL("SELECT * FROM users WHERE id = {id} AND status = {status}")
// s2.Query → "SELECT * FROM users WHERE id = $1 AND status = $2"

// q.NamedSQL — named-param style
s3 := q.NamedSQL("SELECT * FROM users WHERE id = {id} AND status = {status}")
// s3.Query → "SELECT * FROM users WHERE id = :name1 AND status = :name2"

// Crucial: injection attempts stay parameterised. The user's value
// goes into Args, never inlined into Query.
s4 := q.SQL("DELETE FROM cache WHERE key = {name}")
// s4.Query → "DELETE FROM cache WHERE key = ?"
// s4.Args  → []any{"alice'; DROP TABLE users; --"}  (single arg, never SQL)
```

## What you can put in `{expr}`

Anything that parses as a Go expression: identifiers, selectors, function calls, arithmetic, indexing. Same rules as [`q.F`](format.md):

```go
q.SQL("SELECT * FROM events WHERE user_id = {user.ID}")
q.SQL("SELECT * FROM logs WHERE created_at > NOW() - INTERVAL '{minutes} minutes'")
q.SQL("INSERT INTO audit (event, payload) VALUES ({event}, {json.RawMessage(payload)})")
```

The expression's runtime value lands in `Args` as `any`. Drivers handle the type-specific bind (string, int, time.Time, json.RawMessage, …) the same way they would for hand-written placeholders.

## Why a struct return?

A `(string, []any)` tuple return would pair nicely with Go's f(g()) rule (`db.QueryRow(q.SQL("..."))` would auto-spread)... except `db.QueryRow` takes `(string, ...any)`, and the variadic spread doesn't compose with multi-value return — Go's spec restricts `f(g())` to single-arg-position uses. So an explicit struct + `s.Args...` spread is the cleanest shape:

```go
s := q.SQL("...")
db.QueryRowContext(ctx, s.Query, s.Args...)
```

It's one extra symbol per query, in exchange for type safety and a clear name for the parameterised query handle.

## Brace escapes

`{{` is a literal `{`, `}}` is a literal `}`. Rare in SQL but supported:

```go
q.SQL("INSERT INTO docs (data) VALUES ('{{json}}') WHERE id = {id}")
// s.Query → "INSERT INTO docs (data) VALUES ('{json}') WHERE id = ?"
```

`%` is NOT escaped — unlike `q.F`, the SQL helpers don't pipe through `fmt.Sprintf`, so `%` is a plain character (handy for `LIKE` patterns).

## Composing with the rest of q

Standard q composition rules apply:

```go
s := q.Try(loadQuery())          // q.Try with a (SQLQuery, error)-returning loader
row := db.QueryRowContext(ctx, s.Query, s.Args...)

// Or built inline:
return q.Try(db.ExecContext(ctx,
    q.SQL("UPDATE cache SET hit_count = hit_count + 1 WHERE key = {key}").Query,
    q.SQL("UPDATE cache SET hit_count = hit_count + 1 WHERE key = {key}").Args...,
))
```

The latter shape is awkward — for clarity, bind to a local first.

## Stretch ideas (not yet implemented)

- **`{values...}` for IN-list expansion.** A bare `{xs}` for a slice is ambiguous (one placeholder vs N), so a dedicated form is clearer. Tracked as a stretch goal in TODO #77.
- **Compile-time SQL syntax check.** Could lint the rewritten Query for obvious issues. Out of scope for the current pass; users should run their own SQL linter on the output.

## Tradeoffs

- **Identifiers inside the literal aren't IDE-visible.** Same as `q.F`: rename / go-to-def don't see `{name}`. Compiler still catches typos.
- **Driver compatibility is your responsibility.** `q.SQL` produces `?` placeholders — if you're on a Postgres-only stack, prefer `q.PgSQL` so the query reads natively.
- **Named placeholders auto-generate names.** `q.NamedSQL` emits `:name1`, `:name2`, … in source order. There's no facility yet to use the source identifier as the placeholder name (`:id` for `{id}`); add a stretch ticket if you need that.

## See also

- [`q.F` / `q.Ferr` / `q.Fln`](format.md) — the same `{expr}` syntax, but rewriting to `fmt.Sprintf` for free-form string formatting.
