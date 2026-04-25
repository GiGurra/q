// Fixture: q.Assemble — pure auto-derived dependency injection. The
// preprocessor reads each recipe's signature, builds a dep graph
// keyed by output type, topo-sorts, and emits the inlined construction.
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

func main() {
	// Recipes can appear in any order; the preprocessor topo-sorts.
	s := q.Assemble[*Server](newServer, newCache, newDB, newConfig)
	fmt.Println("server cfg:", s.cfg.DB)
	fmt.Println("server.cache.db == server.db:", s.cache.db == s.db)

	// Inline value as a recipe — its type IS the provided type, no
	// inputs required. Direct ZIO ZLayer.succeed analogue.
	customCfg := &Config{DB: "override"}
	s2 := q.Assemble[*Server](customCfg, newDB, newCache, newServer)
	fmt.Println("override cfg:", s2.cfg.DB)
}
