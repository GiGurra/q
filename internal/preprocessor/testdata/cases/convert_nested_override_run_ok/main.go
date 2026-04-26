// Fixture: q.Convert with multi-hop overrides — q.Set / q.SetFn
// can target a nested field directly via UserDTO{}.Address.City,
// without forcing the user to reconstruct the whole sub-struct.
// Other nested fields keep their auto-derived values from the
// source's matching sub-struct.
package main

import (
	"fmt"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

type Address struct {
	Street  string
	City    string
	Country string // dropped — no AddressDTO counterpart
}

type AddressDTO struct {
	Street string
	City   string
}

type User struct {
	ID      int
	Name    string
	Address Address
}

type UserDTO struct {
	ID      int
	Name    string
	Address AddressDTO
}

func main() {
	u := User{
		ID:   1,
		Name: "Bob",
		Address: Address{
			Street:  "100 Main",
			City:    "old city",
			Country: "ZZ",
		},
	}

	// Override one nested field; the other (Address.Street) stays
	// auto-derived from u.Address.Street.
	dto := q.Convert[UserDTO](u,
		q.Set(UserDTO{}.Address.City, "Springfield"),
	)
	fmt.Printf("set: %+v\n", dto)

	// SetFn variant: derive nested City from the whole source.
	dto2 := q.Convert[UserDTO](u,
		q.SetFn(UserDTO{}.Address.City, func(u User) string { return strings.ToUpper(u.Address.City) }),
	)
	fmt.Printf("setfn: %+v\n", dto2)
}
