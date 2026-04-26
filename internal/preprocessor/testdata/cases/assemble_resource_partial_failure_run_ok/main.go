// Fixture: q.Assemble[T](recipes...).NoDeferCleanup() — partial-failure
// auto-cleanup. When a recipe mid-chain fails, the IIFE itself fires
// the cleanups for everything successfully built so far (in reverse-
// topo order) before returning the error. The user's shutdown
// closure is a no-op in that case.
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{}
type DB struct{}
type Cache struct{}
type Server struct{}

var shutdownLog []string

func newConfig() *Config { return &Config{} }

func openDB(c *Config) (*DB, func(), error) {
	return &DB{}, func() { shutdownLog = append(shutdownLog, "db") }, nil
}

func openCache(d *DB) (*Cache, func(), error) {
	return &Cache{}, func() { shutdownLog = append(shutdownLog, "cache") }, nil
}

var errBoot = errors.New("server boot failed")

// openServer fails — but db and cache are already constructed.
// Their cleanups must fire automatically before the error bubbles.
func openServer(d *DB, c *Cache) (*Server, func(), error) {
	return nil, nil, errBoot
}

func main() {
	server, shutdown, err := q.Assemble[*Server](newConfig, openDB, openCache, openServer).NoDeferCleanup()

	fmt.Println("err is errBoot:", errors.Is(err, errBoot))
	fmt.Println("server nil:", server == nil)
	fmt.Println("shutdown not nil:", shutdown != nil)

	// Auto-cleanup of partial-build resources already fired:
	fmt.Println("partial cleanup order:")
	for _, s := range shutdownLog {
		fmt.Println(" -", s)
	}
	fmt.Println("total fired:", len(shutdownLog))

	// User's shutdown is a no-op now.
	shutdown()
	fmt.Println("post-shutdown total:", len(shutdownLog))
}
