// Negative fixture: T (*Server) is not produced by any recipe — the
// resolver should fail with "target type ... is not produced".
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }
type DB struct{ cfg *Config }
type Server struct {
	db  *DB
	cfg *Config
}

func newConfig() *Config { return &Config{DB: "x"} }
func newDB(c *Config) *DB { return &DB{cfg: c} }

func main() {
	// No recipe produces *Server.
	_, _ = q.Assemble[*Server](newConfig, newDB).Release()
}
