// Fixture: q.SQL / q.PgSQL / q.NamedSQL — placeholder-style
// parameterised SQL builders. Each {expr} segment lifts out as a
// driver-appropriate placeholder and the corresponding Args entry.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	id := 42
	status := "active"
	name := "alice'; DROP TABLE users; --"

	// q.SQL with `?` placeholders
	s1 := q.SQL("SELECT * FROM users WHERE id = {id}")
	fmt.Println("s1.Query:", s1.Query)
	fmt.Println("s1.Args:", s1.Args)

	s2 := q.SQL("SELECT * FROM users WHERE id = {id} AND status = {status}")
	fmt.Println("s2.Query:", s2.Query)
	fmt.Println("s2.Args:", s2.Args)

	// Crucial: user-supplied value with embedded SQL stays parameterised.
	// The injection attempt becomes a single literal arg, never inlined.
	s3 := q.SQL("SELECT * FROM users WHERE name = {name}")
	fmt.Println("s3.Query:", s3.Query)
	fmt.Println("s3.Args:", s3.Args)

	// Same shape but PostgreSQL-style placeholders.
	s4 := q.PgSQL("SELECT * FROM users WHERE id = {id} AND status = {status}")
	fmt.Println("s4.Query:", s4.Query)
	fmt.Println("s4.Args:", s4.Args)

	// Named-param style.
	s5 := q.NamedSQL("SELECT * FROM users WHERE id = {id} AND status = {status}")
	fmt.Println("s5.Query:", s5.Query)
	fmt.Println("s5.Args:", s5.Args)

	// Brace escapes (rare in SQL but supported).
	s6 := q.SQL("SELECT '{{json}}' as label WHERE id = {id}")
	fmt.Println("s6.Query:", s6.Query)
	fmt.Println("s6.Args:", s6.Args)

	// No placeholders → constant query, nil Args.
	s7 := q.SQL("SELECT 1")
	fmt.Println("s7.Query:", s7.Query)
	fmt.Println("s7.Args:", s7.Args)

	// Complex expressions in placeholders: arithmetic, function calls, selectors.
	user := struct{ ID int }{ID: 7}
	s8 := q.PgSQL("SELECT * FROM events WHERE user_id = {user.ID} AND created_at > NOW() - INTERVAL '{id*2} days'")
	fmt.Println("s8.Query:", s8.Query)
	fmt.Println("s8.Args:", s8.Args)

	// Composition: q.SQL nested in another expression.
	dump := dumpQuery(q.SQL("DELETE FROM cache WHERE key = {name}"))
	fmt.Println("dump:", dump)

	// Typed Go literals as placeholders — anything parser.ParseExpr
	// accepts works: int / float / bool / string / composite literals.
	s9 := q.SQL("SELECT * FROM x WHERE y = {1}")
	fmt.Println("s9:", s9.Query, s9.Args)
	s10 := q.SQL("SELECT * FROM x WHERE z = {3.14} AND active = {true}")
	fmt.Println("s10:", s10.Query, s10.Args)
	s11 := q.SQL("SELECT * FROM x WHERE label = {\"hello\"}")
	fmt.Println("s11:", s11.Query, s11.Args)
	s12 := q.PgSQL("SELECT * FROM x WHERE tag = ANY({[]string{\"a\", \"b\"}})")
	fmt.Println("s12:", s12.Query, s12.Args)
}

func dumpQuery(s q.SQLQuery) string {
	return fmt.Sprintf("[query=%q args=%v]", s.Query, s.Args)
}
