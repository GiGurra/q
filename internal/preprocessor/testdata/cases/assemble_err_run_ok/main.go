// Fixture: q.AssembleErr — auto-derived DI with errored recipes. Each
// errored recipe gets a bind-and-bubble check inside the IIFE; the
// IIFE itself returns (T, error), composing naturally with q.Try at
// the outer call site.
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }
type DB struct{ cfg *Config }
type Server struct {
	db  *DB
	cfg *Config
}

func newConfig() *Config { return &Config{DB: "primary"} }

func newDB(c *Config) (*DB, error) {
	if c.DB == "fail-db" {
		return nil, errors.New("dial failed")
	}
	return &DB{cfg: c}, nil
}

func newServer(d *DB, c *Config) (*Server, error) {
	return &Server{db: d, cfg: c}, nil
}

func bootSuccess() (*Server, error) {
	return q.AssembleErr[*Server](newConfig, newDB, newServer)
}

func bootFailure() (*Server, error) {
	cfg := &Config{DB: "fail-db"}
	return q.AssembleErr[*Server](cfg, newDB, newServer)
}

// Compose with q.Try at the call site.
func bootViaTry() (*Server, error) {
	s := q.Try(q.AssembleErr[*Server](newConfig, newDB, newServer))
	return s, nil
}

func main() {
	s, err := bootSuccess()
	fmt.Println("success ok:", err == nil, "cfg:", s.cfg.DB)

	_, err = bootFailure()
	fmt.Println("failure err:", err)

	s, err = bootViaTry()
	fmt.Println("via Try ok:", err == nil, "cfg:", s.cfg.DB)
}
