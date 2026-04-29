// example/convert mirrors docs/api/convert.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/convert
package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- "Auto-derivation — the happy path" ----------
//
//	type User    struct { ID int; Name string; Internal bool; Notes string }
//	type UserDTO struct { ID int; Name string }
//	dto := q.ConvertTo[UserDTO](user)
type User struct {
	ID       int
	Name     string
	Internal bool
	Notes    string
}

type UserDTO struct {
	ID   int
	Name string
}

func happyPath() UserDTO {
	user := User{ID: 1, Name: "Ada", Internal: true, Notes: "n/a"}
	return q.ConvertTo[UserDTO](user)
}

// ---------- "Auto-derivation — wide source" ----------
type WideRecord struct {
	ID, Score, Internal int
	Name, Email, Token  string
	CreatedAt, UpdatedAt time.Time
}

type PublicView struct {
	ID    int
	Name  string
	Email string
}

func wideToPublic() PublicView {
	rec := WideRecord{ID: 7, Score: 99, Internal: 1, Name: "Linus", Email: "l@x", Token: "t"}
	return q.ConvertTo[PublicView](rec)
}

// ---------- "Nested derivation" ----------
type Address struct{ Street, City, Country string }
type AddressDTO struct{ Street, City string }

type Person struct {
	ID      int
	Name    string
	Address Address
}
type PersonDTO struct {
	ID      int
	Name    string
	Address AddressDTO
}

func nestedDerivation() PersonDTO {
	p := Person{ID: 1, Name: "Ada", Address: Address{Street: "Main", City: "Cph", Country: "DK"}}
	return q.ConvertTo[PersonDTO](p)
}

// ---------- "Manual overrides — Set / SetFn" ----------
type FullUser struct {
	ID    int
	First string
	Last  string
	Email string
}

type FullUserDTO struct {
	ID       int
	Email    string
	FullName string
	Source   string
}

func manualOverrides() FullUserDTO {
	user := FullUser{ID: 1, First: "Ada", Last: "Lovelace", Email: "ADA@X"}
	return q.ConvertTo[FullUserDTO](user,
		q.Set(FullUserDTO{}.Source, "v1"),
		q.SetFn(FullUserDTO{}.Email, func(u FullUser) string { return strings.ToLower(u.Email) }),
		q.SetFn(FullUserDTO{}.FullName, func(u FullUser) string { return u.First + " " + u.Last }),
	)
}

// ---------- "Nested-field overrides" ----------
func nestedOverride() PersonDTO {
	p := Person{ID: 2, Name: "Linus", Address: Address{Street: "Helsinki", City: "HEL"}}
	return q.ConvertTo[PersonDTO](p,
		q.Set(PersonDTO{}.Address.City, "Springfield"),
	)
}

// ---------- "Fallible conversion — q.ConvertToE" ----------
type LookupUser struct {
	ID    int
	Name  string
	Token string
}

type LookupUserDTO struct {
	ID    int
	Name  string
	Email string
}

// fakeMailer.Lookup stands in for the doc's `db.LookupEmail`.
type fakeMailer struct{ failOn int }

func (m fakeMailer) Lookup(id int) (string, error) {
	if id == m.failOn {
		return "", errors.New("not found")
	}
	return fmt.Sprintf("user%d@example.com", id), nil
}

var mailer = fakeMailer{failOn: 99}

func fallibleConvert(u LookupUser) (LookupUserDTO, error) {
	dto, err := q.ConvertToE[LookupUserDTO](u,
		q.SetFnE(LookupUserDTO{}.Email, func(s LookupUser) (string, error) {
			return mailer.Lookup(s.ID)
		}),
	)
	if err != nil {
		return LookupUserDTO{}, err
	}
	return dto, nil
}

// q.Try-flat shape:
//
//	dto := q.Try(q.ConvertToE[UserDTO](user, q.SetFnE(...)))
func fallibleConvertViaTry(u LookupUser) (LookupUserDTO, error) {
	dto := q.Try(q.ConvertToE[LookupUserDTO](u,
		q.SetFnE(LookupUserDTO{}.Email, func(s LookupUser) (string, error) {
			return mailer.Lookup(s.ID)
		}),
	))
	return dto, nil
}

// ---------- "Mixing everything — a complete example" ----------
type CompleteUser struct {
	ID      int
	First   string
	Last    string
	Email   string
	Address Address
}

type CompleteUserDTO struct {
	ID       int
	FullName string
	Email    string
	Source   string
	Address  AddressDTO
}

func mixingAll(u CompleteUser) (CompleteUserDTO, error) {
	dto, err := q.ConvertToE[CompleteUserDTO](u,
		q.Set(CompleteUserDTO{}.Source, "v1"),
		q.SetFn(CompleteUserDTO{}.FullName, func(s CompleteUser) string {
			return s.First + " " + s.Last
		}),
		q.SetFnE(CompleteUserDTO{}.Email, func(s CompleteUser) (string, error) {
			return mailer.Lookup(s.ID)
		}),
		q.Set(CompleteUserDTO{}.Address.City, "Reykjavík"),
	)
	if err != nil {
		return CompleteUserDTO{}, err
	}
	return dto, nil
}

func main() {
	fmt.Printf("happyPath: %+v\n", happyPath())
	fmt.Printf("wideToPublic: %+v\n", wideToPublic())
	fmt.Printf("nestedDerivation: %+v\n", nestedDerivation())
	fmt.Printf("manualOverrides: %+v\n", manualOverrides())
	fmt.Printf("nestedOverride: %+v\n", nestedOverride())

	if dto, err := fallibleConvert(LookupUser{ID: 1, Name: "Ada"}); err != nil {
		fmt.Printf("fallibleConvert(1): err=%s\n", err)
	} else {
		fmt.Printf("fallibleConvert(1): %+v\n", dto)
	}
	if _, err := fallibleConvert(LookupUser{ID: 99, Name: "Missing"}); err != nil {
		fmt.Printf("fallibleConvert(99): err=%s\n", err)
	}

	if dto, err := fallibleConvertViaTry(LookupUser{ID: 2, Name: "L"}); err != nil {
		fmt.Printf("fallibleConvertViaTry(2): err=%s\n", err)
	} else {
		fmt.Printf("fallibleConvertViaTry(2): %+v\n", dto)
	}

	if dto, err := mixingAll(CompleteUser{
		ID: 3, First: "Yann", Last: "L", Email: "y@x",
		Address: Address{Street: "Rue", City: "Paris", Country: "FR"},
	}); err != nil {
		fmt.Printf("mixingAll: err=%s\n", err)
	} else {
		fmt.Printf("mixingAll: %+v\n", dto)
	}
}
