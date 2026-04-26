// Fixture: q.Assemble — auto-derived dependency injection. Always
// returns (T, error). Compose at the call site:
//   - q.Try when the caller bubbles errors
//   - q.Unwrap when the caller has no error path (main, init, tests)
//   - direct (v, err) :=  for explicit handling
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }
type DB struct{ cfg *Config }
type Cache struct{ db *DB }
type Server struct {
	db    *DB
	cache *Cache
	cfg   *Config
}

func newConfig() *Config           { return &Config{DB: "primary"} }
func newDB(c *Config) *DB          { return &DB{cfg: c} }
func newCache(d *DB) *Cache        { return &Cache{db: d} }
func newServer(d *DB, c *Cache, cfg *Config) *Server {
	return &Server{db: d, cache: c, cfg: cfg}
}

// Tuple receive — recipes can appear in any order; the preprocessor
// topo-sorts.
func boot1() (*Server, error) {
	return q.Assemble[*Server](newServer, newCache, newDB, newConfig).Release()
}

// q.Try unwraps to bare T inside a function returning error.
func boot2() (*Server, error) {
	s := q.Try(q.Assemble[*Server](newServer, newCache, newDB, newConfig).Release())
	return s, nil
}

// Inline value as a recipe — its type IS the provided type.
func boot3() (*Server, error) {
	customCfg := &Config{DB: "override"}
	return q.Assemble[*Server](customCfg, newDB, newCache, newServer).Release()
}

func main() {
	// q.Unwrap panics on err; useful in main() where there's no error
	// return path. Here we know the assembly is well-formed at build
	// time so the err is a static impossibility.
	s := q.Unwrap(boot1())
	fmt.Println("boot1 cfg:", s.cfg.DB)
	s = q.Unwrap(boot2())
	fmt.Println("boot2 cfg:", s.cfg.DB)
	s = q.Unwrap(boot3())
	fmt.Println("boot3 cfg:", s.cfg.DB)
}
