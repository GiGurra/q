// Fixture: a recipe in pure q.Assemble returns a nil pointer. The
// rewriter's runtime nil-check fires immediately after the bind and
// panics — pure q.Assemble has no error path so the bug surfaces
// loudly at the recipe site rather than propagating into the call
// graph as a typed-nil interface.
package main

import (
	"fmt"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }
type DB struct{ cfg *Config }
type Server struct{ db *DB }

func newConfig() *Config      { return &Config{DB: "x"} }
func newNilDB(c *Config) *DB  { return nil } // bug: returns nil
func newServer(d *DB) *Server { return &Server{db: d} }

func main() {
	defer func() {
		r := recover()
		msg := fmt.Sprint(r)
		fmt.Println("recovered:", strings.Contains(msg, "newNilDB") && strings.Contains(msg, "returned nil"))
	}()
	_ = q.Assemble[*Server](newConfig, newNilDB, newServer)
}
