// example/sql mirrors docs/api/sql.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/sql
package main

import (
	"encoding/json"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type User struct{ ID int }

// ---------- "At a glance" ----------
func atAGlance() {
	id, status := 42, "active"
	name := "alice'; DROP TABLE users; --" // injection attempt
	_, _, _ = id, status, name             // referenced via q.SQL {expr} below; compiler can't see the use

	s := q.SQL("SELECT * FROM users WHERE id = {id} AND status = {status}")
	fmt.Printf("SQL.Query=%q\n", s.Query)
	fmt.Printf("SQL.Args=%v\n", s.Args)

	s2 := q.PgSQL("SELECT * FROM users WHERE id = {id} AND status = {status}")
	fmt.Printf("PgSQL.Query=%q\n", s2.Query)
	fmt.Printf("PgSQL.Args=%v\n", s2.Args)

	s3 := q.NamedSQL("SELECT * FROM users WHERE id = {id} AND status = {status}")
	fmt.Printf("NamedSQL.Query=%q\n", s3.Query)
	fmt.Printf("NamedSQL.Args=%v\n", s3.Args)

	// Crucial: injection attempts stay parameterised.
	s4 := q.SQL("DELETE FROM cache WHERE key = {name}")
	fmt.Printf("Inj.Query=%q\n", s4.Query)
	fmt.Printf("Inj.Args=%v\n", s4.Args)
}

// ---------- "What you can put in `{expr}`" ----------
func exprForms() {
	user := User{ID: 7}
	minutes := 15
	event := "login"
	payload := []byte(`{"who":"ada"}`)
	_, _, _, _ = user, minutes, event, payload // see comment above

	a := q.SQL("SELECT * FROM events WHERE user_id = {user.ID}")
	fmt.Printf("a.Query=%q a.Args=%v\n", a.Query, a.Args)

	b := q.SQL("SELECT * FROM logs WHERE created_at > NOW() - INTERVAL '{minutes} minutes'")
	fmt.Printf("b.Query=%q b.Args=%v\n", b.Query, b.Args)

	c := q.SQL("INSERT INTO audit (event, payload) VALUES ({event}, {json.RawMessage(payload)})")
	fmt.Printf("c.Query=%q c.Args[0]=%v c.Args[1]=%s\n", c.Query, c.Args[0], string(c.Args[1].(json.RawMessage)))
}

// ---------- "Brace escapes" ----------
func braceEscapes() {
	id := 1
	_ = id
	s := q.SQL("INSERT INTO docs (data) VALUES ('{{json}}') WHERE id = {id}")
	fmt.Printf("braces.Query=%q braces.Args=%v\n", s.Query, s.Args)
}

func main() {
	atAGlance()
	fmt.Println("---")
	exprForms()
	fmt.Println("---")
	braceEscapes()
}
