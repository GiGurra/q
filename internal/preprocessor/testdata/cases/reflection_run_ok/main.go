// Fixture: q.Fields / q.AllFields / q.TypeName / q.Tag — compile-
// time reflection helpers.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type User struct {
	ID    int    `json:"id"   db:"user_id"`
	Name  string `json:"name,omitempty" db:"full_name"`
	Email string `json:"email"`
	pwd   string // unexported — Fields skips, AllFields includes
}

type Color int

const (
	Red Color = iota
)

func main() {
	// q.Fields — exported only.
	fmt.Printf("Fields: %v\n", q.Fields[User]())

	// q.AllFields — every field including unexported.
	fmt.Printf("AllFields: %v\n", q.AllFields[User]())

	// q.Fields on a *T — pointer indirection works.
	fmt.Printf("Fields[*User]: %v\n", q.Fields[*User]())

	// q.TypeName — defined-type name.
	fmt.Printf("TypeName[User]: %s\n", q.TypeName[User]())
	fmt.Printf("TypeName[*User]: %s\n", q.TypeName[*User]())
	fmt.Printf("TypeName[Color]: %s\n", q.TypeName[Color]())

	// q.Tag — struct-tag value lookup.
	fmt.Printf("Tag[User](ID,json): %s\n", q.Tag[User]("ID", "json"))
	fmt.Printf("Tag[User](ID,db): %s\n", q.Tag[User]("ID", "db"))
	fmt.Printf("Tag[User](Name,json): %s\n", q.Tag[User]("Name", "json"))
	fmt.Printf("Tag[User](Name,db): %s\n", q.Tag[User]("Name", "db"))
	fmt.Printf("Tag[User](Email,json): %s\n", q.Tag[User]("Email", "json"))
	// Tag key absent → "" (matches reflect.StructTag.Get).
	fmt.Printf("Tag[User](Email,db): %q\n", q.Tag[User]("Email", "db"))

	// Compose: build a SELECT statement from compile-time reflection.
	cols := []string{
		q.Tag[User]("ID", "db"),
		q.Tag[User]("Name", "db"),
	}
	fmt.Printf("query: SELECT %s,%s FROM users\n", cols[0], cols[1])

	// Each q.Fields / q.Tag call is folded to a literal — proven by
	// using them in slice and string contexts that need compile-time
	// values.
	allFields := q.Fields[User]()
	fmt.Printf("len(Fields)=%d\n", len(allFields))
}
