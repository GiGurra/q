// Fixture: q.ValidatedStruct as a general-purpose validation marker,
// plus q:"opt" as a short alias for q:"optional".
//
// Demonstrates that both markers (q.FnParams and q.ValidatedStruct)
// have identical semantics, and that q:"opt" / q:"optional" are
// interchangeable on a per-field basis.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// q.ValidatedStruct flavour, q:"opt" short tags.
type Config struct {
	_       q.ValidatedStruct
	Name    string
	Version int
	Logger  any `q:"opt"`
	Debug   bool `q:"opt"`
}

// q.FnParams flavour, q:"optional" long tags.
type LoadOptions struct {
	_       q.FnParams
	Path    string
	Format  string
	Timeout int `q:"optional"`
}

// Mixed: q.ValidatedStruct + both tag spellings on different fields.
type Mixed struct {
	_     q.ValidatedStruct
	A     string
	B     int  `q:"opt"`
	C     bool `q:"optional"`
}

func main() {
	c := Config{Name: "app", Version: 1}
	fmt.Println("config:", c)

	o := LoadOptions{Path: "/etc", Format: "yaml"}
	fmt.Println("load:", o)

	m := Mixed{A: "x"}
	fmt.Println("mixed:", m)
}
