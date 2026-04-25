// Fixture: q.Assemble drops into every form position. The in-place
// IIFE substitution must work for define / assign / discard / return /
// hoist alike.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }
type DB struct{ cfg *Config }

func newConfig() *Config  { return &Config{DB: "primary"} }
func newDB(c *Config) *DB { return &DB{cfg: c} }

// formDefine: `var := q.Assemble(...)`
func boot1() *DB {
	d := q.Assemble[*DB](newConfig, newDB)
	return d
}

// formAssign: `var = q.Assemble(...)` (var pre-declared)
func boot2() *DB {
	var d *DB
	d = q.Assemble[*DB](newConfig, newDB)
	return d
}

// formReturn: directly returned
func boot3() *DB {
	return q.Assemble[*DB](newConfig, newDB)
}

// formHoist: nested inside a larger expression — the IIFE substitutes
// in place; the wrapping call sees the assembled value.
func wrap(d *DB) string { return d.cfg.DB }

func boot4() string {
	return wrap(q.Assemble[*DB](newConfig, newDB))
}

// formDiscard: drops the value (rarely useful but must still compile)
func boot5() {
	_ = q.Assemble[*DB](newConfig, newDB)
}

func main() {
	fmt.Println("define:", boot1().cfg.DB)
	fmt.Println("assign:", boot2().cfg.DB)
	fmt.Println("return:", boot3().cfg.DB)
	fmt.Println("hoist:", boot4())
	boot5()
	fmt.Println("discard ok")
}
