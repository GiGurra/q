// Fixture: a recipe in q.Assemble returns a nil pointer. The runtime
// nil-check fires immediately after the bind, BEFORE the value
// flows into the consumer recipe — bubbles a fmt.Errorf wrapping
// q.ErrNil. Callers can errors.Is the failure against the q.ErrNil
// sentinel.
//
// The check happens BEFORE Go's implicit concrete→interface
// conversion at the consumer's call site, so a typed-nil from a
// buggy constructor can't masquerade as a non-nil interface (Go's
// typed-nil-interface pitfall — same one q.Try guards against).
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }
type DB struct{ cfg *Config }
type Server struct{ db *DB }

func newConfig() *Config       { return &Config{DB: "x"} }
func newNilDB(c *Config) *DB   { return nil } // bug: returns nil
func newServer(d *DB) *Server  { return &Server{db: d} }

func boot() (*Server, error) {
	return q.Assemble[*Server](newConfig, newNilDB, newServer).Release()
}

func main() {
	_, err := boot()
	fmt.Println("got err:", err != nil)
	fmt.Println("is q.ErrNil:", errors.Is(err, q.ErrNil))
	fmt.Println("err message:", err)
}
