// Negative fixture: a variadic recipe can't be auto-resolved — the
// dep set isn't fixed. The resolver must reject and suggest wrapping
// in a fixed-arity adapter.
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

type DB struct{}
type Server struct{}

// Variadic in `extras` — recipe set isn't fixed.
func newServer(d *DB, extras ...string) *Server { return &Server{} }

func newDB() *DB { return &DB{} }

func main() {
	_, _ = q.Assemble[*Server](newDB, newServer)
}
