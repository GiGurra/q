// Negative fixture: a recipe is supplied but not transitively
// required by T. The preprocessor must reject the call so users don't
// silently leak unused construction.
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }
type DB struct{ cfg *Config }
type Cache struct{ db *DB }

func newConfig() *Config    { return &Config{DB: "x"} }
func newDB(c *Config) *DB   { return &DB{cfg: c} }
func newCache(d *DB) *Cache { return &Cache{db: d} }
func unrelated() string     { return "stray" }

func main() {
	// `unrelated` produces string — *Cache doesn't need it.
	_, _ = q.Assemble[*Cache](newConfig, newDB, newCache, unrelated).Release()
}
