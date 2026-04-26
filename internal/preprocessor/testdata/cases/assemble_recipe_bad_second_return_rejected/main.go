// Negative fixture: a recipe whose second return is not the built-in
// `error` interface (e.g. `*MyErr`) is rejected. Same typed-nil-
// interface pitfall q.Try guards against — the implicit conversion
// would turn a typed-nil into a non-nil error and bubble bogus
// failures.
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

type DB struct{}
type Server struct{}
type MyErr struct{}

func (e *MyErr) Error() string { return "my err" }

// Second return is *MyErr, not the built-in `error`.
func newDB() (*DB, *MyErr) { return &DB{}, nil }

func newServer(d *DB) *Server { return &Server{} }

func main() {
	_, _ = q.Assemble[*Server](newDB, newServer).DeferCleanup()
}
