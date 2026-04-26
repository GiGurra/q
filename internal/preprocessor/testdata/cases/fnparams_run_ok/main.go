// Fixture: q.FnParams happy paths.
//
// A struct that opts in via `_ q.FnParams` becomes required-by-default.
// Per-field opt-out via `q:"optional"` tag. The preprocessor checks
// each marked struct literal at its construction site.
//
// All literals here pass the check — every required field is present,
// optional fields may be omitted.
package main

import (
	"fmt"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

type LoadOptions struct {
	_       q.FnParams
	Path    string
	Format  string
	Timeout time.Duration `q:"optional"`
	Logger  any           `q:"optional"`
}

// Plain (un-marked) struct — q.FnParams does NOT apply, no validation.
type PlainOptions struct {
	A int
	B string
}

func load(opts LoadOptions) string {
	return fmt.Sprintf("path=%s format=%s timeout=%v", opts.Path, opts.Format, opts.Timeout)
}

func main() {
	// (1) All required fields present, optional omitted.
	out1 := load(LoadOptions{Path: "/etc", Format: "yaml"})
	fmt.Println("out1:", out1)

	// (2) All required fields present, optional explicitly set.
	out2 := load(LoadOptions{Path: "/etc", Format: "json", Timeout: 5 * time.Second})
	fmt.Println("out2:", out2)

	// (3) Both optionals set.
	out3 := load(LoadOptions{Path: "/etc", Format: "toml", Timeout: time.Second, Logger: "log"})
	fmt.Println("out3:", out3)

	// (4) Plain (un-marked) struct — empty literal allowed; no FnParams
	//     validation applies.
	plain := PlainOptions{}
	fmt.Println("plain:", plain)

	// (5) Positional literal — every field set by construction; no
	//     keyed-field check required.
	pos := PlainOptions{1, "x"}
	fmt.Println("pos:", pos)
}
