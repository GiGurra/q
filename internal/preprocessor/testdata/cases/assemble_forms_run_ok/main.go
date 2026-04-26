// Fixture: q.Assemble drops into every form position. The in-place
// IIFE substitution must work for define / assign / return / hoist.
// Returns (T, error) so each call site receives a tuple.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }
type DB struct{ cfg *Config }

func newConfig() *Config  { return &Config{DB: "primary"} }
func newDB(c *Config) *DB { return &DB{cfg: c} }

// formDefine: `var, err := q.Assemble(...)`
func boot1() (*DB, error) {
	d, err := q.Assemble[*DB](newConfig, newDB).DeferCleanup()
	return d, err
}

// formAssign: `var, err = q.Assemble(...)` (vars pre-declared)
func boot2() (*DB, error) {
	var d *DB
	var err error
	d, err = q.Assemble[*DB](newConfig, newDB).DeferCleanup()
	return d, err
}

// formReturn: directly returned (the (T, error) tuple lines up).
func boot3() (*DB, error) {
	return q.Assemble[*DB](newConfig, newDB).DeferCleanup()
}

// formHoist: nested inside a larger expression — the IIFE substitutes
// in place; the wrapping call sees the (T, error) tuple via q.Try.
func wrap(d *DB) string { return d.cfg.DB }

func boot4() (string, error) {
	d := q.Try(q.Assemble[*DB](newConfig, newDB).DeferCleanup())
	return wrap(d), nil
}

func main() {
	d := q.Unwrap(boot1())
	fmt.Println("define:", d.cfg.DB)
	d = q.Unwrap(boot2())
	fmt.Println("assign:", d.cfg.DB)
	d = q.Unwrap(boot3())
	fmt.Println("return:", d.cfg.DB)
	s := q.Unwrap(boot4())
	fmt.Println("hoist:", s)
}
