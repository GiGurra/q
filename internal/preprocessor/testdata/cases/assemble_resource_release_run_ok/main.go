// Fixture: q.Assemble[T](recipes...).Release() — auto-defer
// resource lifetime. The Release chain terminator injects a defer
// in the enclosing function that fires the cleanup chain in
// reverse-topo order when the function returns.
//
// Validates:
//   1. Multi-layer dep graph constructs in topo order.
//   2. Release fires cleanups in REVERSE topo order on success path.
//   3. Each cleanup runs synchronously (blocking next one).
//   4. Mix of (T, func(), error) recipes and pure (T) / (T, error)
//      recipes — pure ones don't push cleanups.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ Region string }
type DB struct{ cfg *Config }
type Cache struct{ db *DB }
type Server struct {
	db    *DB
	cache *Cache
}

var shutdownLog []string

// Pure recipe — no cleanup, doesn't show up in shutdownLog.
func newConfig() *Config { return &Config{Region: "us-east-1"} }

func openDB(c *Config) (*DB, func(), error) {
	return &DB{cfg: c}, func() { shutdownLog = append(shutdownLog, "db") }, nil
}

func openCache(d *DB) (*Cache, func(), error) {
	return &Cache{db: d}, func() { shutdownLog = append(shutdownLog, "cache") }, nil
}

func openServer(d *DB, c *Cache) (*Server, func(), error) {
	return &Server{db: d, cache: c}, func() { shutdownLog = append(shutdownLog, "server") }, nil
}

func boot() {
	server, err := q.Assemble[*Server](newConfig, openDB, openCache, openServer).Release()
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println("server.db.cfg.Region:", server.db.cfg.Region)
	fmt.Println("during boot, shutdownLog len:", len(shutdownLog))
} // ← deferred Release fires here, in reverse-topo

func main() {
	boot()

	// After boot returns: cleanups have fired in reverse-topo.
	// Construction was config → db → cache → server (topo).
	// Cleanup is server → cache → db (reverse). Config has no
	// cleanup (pure recipe).
	fmt.Println("after boot:")
	for _, s := range shutdownLog {
		fmt.Println(" -", s)
	}
	fmt.Println("total fired:", len(shutdownLog))
}
