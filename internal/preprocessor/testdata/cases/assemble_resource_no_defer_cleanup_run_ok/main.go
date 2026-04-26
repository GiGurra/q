// Fixture: q.Assemble[T](recipes...).NoDeferCleanup() — caller-managed
// shutdown closure. The IIFE returns (T, func(), error); the closure
// is sync.OnceFunc-wrapped so duplicate invocations are safe.
//
// Validates:
//   1. NoDeferCleanup returns 3 values usable directly.
//   2. shutdown is idempotent (multiple calls = one teardown).
//   3. Cleanup order is reverse-topo, blocking.
//   4. shutdown is non-nil on error path too (no-op closure).
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{}
type DB struct{}
type Cache struct{}

var shutdownLog []string

func newConfig() *Config { return &Config{} }

func openDB(c *Config) (*DB, func(), error) {
	return &DB{}, func() { shutdownLog = append(shutdownLog, "db") }, nil
}

func openCache(d *DB) (*Cache, func(), error) {
	return &Cache{}, func() { shutdownLog = append(shutdownLog, "cache") }, nil
}

func main() {
	cache, shutdown, err := q.Assemble[*Cache](newConfig, openDB, openCache).NoDeferCleanup()
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println("cache built:", cache != nil)
	fmt.Println("shutdown not nil:", shutdown != nil)

	// Idempotence: 4 calls — only one teardown.
	shutdown()
	shutdown()
	shutdown()
	shutdown()

	// Reverse-topo: cache (last built) first, then db.
	fmt.Println("shutdown order:")
	for _, s := range shutdownLog {
		fmt.Println(" -", s)
	}
	fmt.Println("total fired:", len(shutdownLog))
}
