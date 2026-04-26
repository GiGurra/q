// Fixture: q.ConvertTo recurses into nested struct fields when the
// same-named source field is itself a struct of a different type
// whose fields can be auto-derived for the target field's struct.
//
// Source.Address is type Address; Target.Address is type AddressDTO
// — both structs, all of AddressDTO's fields exist on Address with
// assignable types, so the conversion auto-derives recursively.
package main

import (
	"fmt"

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
	Address AddressDTO // auto-derived from User.Address
}

func main() {
	u := User{
		ID:   1,
		Name: "Bob",
		Address: Address{
			Street:  "100 Main",
			City:    "Springfield",
			Country: "ZZ",
		},
	}
	dto := q.ConvertTo[UserDTO](u)
	fmt.Printf("dto: %+v\n", dto)
}
