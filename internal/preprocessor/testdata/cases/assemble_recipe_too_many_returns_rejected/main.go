// Negative fixture: a recipe with 3+ return values can't be classified
// as either pure (T) or errored (T, error). The resolver must reject
// with a shape diagnostic naming the actual return arity.
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

type DB struct{}
type Server struct{}

// Three returns — invalid recipe shape.
func newDBTriple() (*DB, string, error) { return &DB{}, "", nil }

func newServer(d *DB) *Server { return &Server{} }

func main() {
	_, _ = q.Assemble[*Server](newDBTriple, newServer).DeferCleanup()
}
