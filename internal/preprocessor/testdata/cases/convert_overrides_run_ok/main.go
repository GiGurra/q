// Fixture: q.ConvertTo with manual overrides via q.Set / q.SetFn.
// The override field reference is a typed Go selector expression —
// UserDTO{}.<Field> — so the rewriter extracts the field name from
// the AST and Go's own type-checker validates the field exists and
// the value/fn return type matches. Refactor-safe: rename a target
// field and every override site fails to compile.
package main

import (
	"fmt"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

type User struct {
	ID    int
	First string
	Last  string
	Email string
}

type UserDTO struct {
	ID       int    // auto: matches User.ID
	Email    string // overridden by q.SetFn (lowercased)
	FullName string // overridden by q.SetFn (concat)
	Source   string // overridden by q.Set (constant)
}

func main() {
	u := User{ID: 7, First: "Ada", Last: "Lovelace", Email: "ADA@EXAMPLE.COM"}

	dto := q.ConvertTo[UserDTO](u,
		q.Set(UserDTO{}.Source, "v1"),
		q.SetFn(UserDTO{}.Email, func(u User) string { return strings.ToLower(u.Email) }),
		q.SetFn(UserDTO{}.FullName, func(u User) string { return u.First + " " + u.Last }),
	)
	fmt.Printf("dto: %+v\n", dto)
}
