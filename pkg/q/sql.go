package q

// sql.go — injection-safe SQL via compile-time `{expr}`
// interpolation. Each call site rewrites to a SQLQuery literal whose
// Query string carries placeholders (`?`, `$1`, or `:name1`) and
// whose Args slice carries the extracted Go expressions in
// left-to-right order. The user passes Query + Args... to their
// driver; user-supplied values are never inlined into the SQL text,
// so the helper enforces the parameterised pattern at the syntactic
// level.

// SQLQuery pairs a parameterised SQL query string with its
// corresponding ordered argument list. Produced by q.SQL, q.PgSQL,
// and q.NamedSQL at compile time. Drop directly into the driver:
//
//	s := q.SQL("SELECT * FROM users WHERE id = {id} AND status = {status}")
//	row := db.QueryRowContext(ctx, s.Query, s.Args...)
type SQLQuery struct {
	Query string
	Args  []any
}

// SQL builds a parameterised query using `?` placeholders for each
// `{expr}` segment in the format. The format MUST be a Go string
// literal — dynamic format strings are rejected at scan time
// (allowing them would re-open the SQL-injection hole the helper
// exists to close).
//
//	s := q.SQL("SELECT name FROM users WHERE id = {id}")
//	// s.Query → "SELECT name FROM users WHERE id = ?"
//	// s.Args  → []any{id}
//
// `?` placeholders are the most portable form — SQLite, MySQL, and
// every database/sql driver that accepts plain positional binds.
// For Postgres-style numbered placeholders, use q.PgSQL. For named-
// param drivers (sqlx, etc.), use q.NamedSQL.
//
// Brace-escape `{{` for literal `{` and `}}` for literal `}`.
func SQL(format string) SQLQuery {
	panicUnrewritten("q.SQL")
	return SQLQuery{}
}

// PgSQL is q.SQL with `$1`, `$2`, … (1-indexed) placeholders for
// PostgreSQL drivers (lib/pq, pgx).
//
//	s := q.PgSQL("SELECT * FROM users WHERE id = {id} AND status = {status}")
//	// s.Query → "SELECT * FROM users WHERE id = $1 AND status = $2"
//	// s.Args  → []any{id, status}
func PgSQL(format string) SQLQuery {
	panicUnrewritten("q.PgSQL")
	return SQLQuery{}
}

// NamedSQL is q.SQL with `:nameN` placeholders for drivers that
// support named parameters (sqlx, jmoiron/sqlx-style query
// helpers). The placeholder names are auto-generated as `:name1`,
// `:name2`, … in left-to-right order; if you need stable names tied
// to the source identifiers, hand-write the query.
//
//	s := q.NamedSQL("SELECT * FROM users WHERE id = {id}")
//	// s.Query → "SELECT * FROM users WHERE id = :name1"
//	// s.Args  → []any{id}
func NamedSQL(format string) SQLQuery {
	panicUnrewritten("q.NamedSQL")
	return SQLQuery{}
}
