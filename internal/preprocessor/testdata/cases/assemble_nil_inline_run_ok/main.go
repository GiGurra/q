// Fixture: a typed-nil inline value flows into the assembly. The
// runtime nil-check on the bound _qDep<N> fires immediately — same
// machinery that catches nil-returning constructors. For q.AssembleErr
// the failure bubbles a fmt.Errorf wrapping q.ErrNil.
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }
type DB struct{ cfg *Config }

func newDB(c *Config) *DB { return &DB{cfg: c} }

func boot() (*DB, error) {
	var nilCfg *Config // typed-nil
	return q.AssembleErr[*DB](nilCfg, newDB)
}

func main() {
	_, err := boot()
	fmt.Println("got err:", err != nil)
	fmt.Println("is q.ErrNil:", errors.Is(err, q.ErrNil))
}
