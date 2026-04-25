// Negative fixture: a recipe needs *DB but no recipe provides one.
// The preprocessor must surface a "missing recipe for *DB" diagnostic
// pointing at the consuming recipe.
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

func newConfig() *Config                 { return &Config{DB: "x"} }
func newServer(d *DB, c *Config) *Server { return &Server{db: d, cfg: c} }

func main() {
	// *DB is missing — newServer needs it.
	_ = q.Assemble[*Server](newConfig, newServer)
}
