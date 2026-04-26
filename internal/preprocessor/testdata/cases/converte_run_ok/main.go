// Fixture: q.ConvertToE — fallible variant of q.ConvertTo. SetFnE
// overrides may return (V, error); a non-nil error short-circuits
// the conversion and propagates out. Demonstrates both happy and
// error paths.
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type User struct {
	ID    int
	Name  string
	Token string
}

type UserDTO struct {
	ID       int
	Name     string
	Email    string // overridden via SetFnE — call lookupEmail
	Source   string // overridden via Set — constant
}

func lookupEmail(u User) (string, error) {
	if u.ID < 0 {
		return "", errors.New("invalid id")
	}
	return fmt.Sprintf("u%d@example.com", u.ID), nil
}

func main() {
	good := User{ID: 7, Name: "Ada", Token: "secret"}
	dto, err := q.ConvertToE[UserDTO](good,
		q.SetFnE(UserDTO{}.Email, lookupEmail),
		q.Set(UserDTO{}.Source, "v1"),
	)
	fmt.Printf("ok dto:%+v err:%v\n", dto, err)

	bad := User{ID: -1, Name: "x"}
	dto2, err2 := q.ConvertToE[UserDTO](bad,
		q.SetFnE(UserDTO{}.Email, lookupEmail),
		q.Set(UserDTO{}.Source, "v1"),
	)
	// dto2 is the zero value when the conversion bubbles.
	fmt.Printf("err dto:%+v err:%v\n", dto2, err2)
}
