// Fixture: q.FnParams negative — missing required fields.
//
// LoadOptions opts in via `_ q.FnParams`, declares Path and Format
// as required (untagged), Timeout as optional. The two literals below
// each omit a required field; the preprocessor must reject the build
// with diagnostics naming the missing fields.
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
}

func load(opts LoadOptions) string { return opts.Path }

func main() {
	// MISSING: Format (required).
	out1 := load(LoadOptions{Path: "/etc"})
	fmt.Println(out1)

	// MISSING: both Path and Format.
	out2 := load(LoadOptions{Timeout: 5 * time.Second})
	fmt.Println(out2)
}
