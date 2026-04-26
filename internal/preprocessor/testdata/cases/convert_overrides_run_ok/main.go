// Fixture: q.Convert with manual overrides via q.Set / q.SetFn.
// Demonstrates the v2 surface — target fields that the auto-derive
// pass can't satisfy (no source counterpart, or incompatible type)
// get filled in explicitly. Auto-derived fields still copy directly.
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

	dto := q.Convert[UserDTO](u,
		q.Set("Source", "v1"),
		q.SetFn("Email", func(u User) string { return strings.ToLower(u.Email) }),
		q.SetFn("FullName", func(u User) string { return u.First + " " + u.Last }),
	)
	fmt.Printf("dto: %+v\n", dto)
}
