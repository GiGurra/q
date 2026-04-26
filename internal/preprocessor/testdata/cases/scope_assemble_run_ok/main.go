// Fixture: q.Assemble[T](...).WithScope(scope) — caches built deps
// in the scope and reuses them across multiple Assemble calls.
// Verifies cache hits, fresh builds, cleanup ordering on scope.Close,
// and ErrScopeClosed semantics for closed scopes.
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct {
	id  int
	out *[]string
}

type DB struct {
	cfg *Config
	id  int
	out *[]string
}

func (d *DB) Close() { *d.out = append(*d.out, fmt.Sprintf("db.Close#%d", d.id)) }

type Server struct {
	db   *DB
	id   int
	cfg  *Config
	out  *[]string
}

func (s *Server) Close() { *s.out = append(*s.out, fmt.Sprintf("srv.Close#%d", s.id)) }

// nextID is incremented per call so we can distinguish "fresh build"
// (gets a new id) from "cache hit" (reuses the prior id).
var nextID int

func nextN() int { nextID++; return nextID }

func newConfig(out *[]string) *Config {
	*out = append(*out, "build:cfg")
	return &Config{id: nextN(), out: out}
}

func newDB(cfg *Config) *DB {
	*cfg.out = append(*cfg.out, "build:db")
	return &DB{cfg: cfg, id: nextN(), out: cfg.out}
}

func newServer(db *DB, cfg *Config) *Server {
	*cfg.out = append(*cfg.out, "build:srv")
	return &Server{db: db, cfg: cfg, id: nextN(), out: cfg.out}
}

func main() {
	// Reuse-across-assemblies: two Assemble calls in the same scope.
	// The second call should see all three deps from cache and not
	// re-invoke any recipe.
	{
		var trace []string
		scope, shutdown := q.NewScope().NoDeferCleanup()

		s1, err := q.Assemble[*Server](&trace, newConfig, newDB, newServer).WithScope(scope)
		if err != nil {
			fmt.Println("first:", err)
			return
		}
		s2, err := q.Assemble[*Server](&trace, newConfig, newDB, newServer).WithScope(scope)
		if err != nil {
			fmt.Println("second:", err)
			return
		}
		fmt.Println("same:", s1 == s2)
		fmt.Println("trace:", trace)
		shutdown()
		fmt.Println("post-close:", trace)
	}

	// Closed-scope behaviour: an assembly on an already-closed scope
	// returns ErrScopeClosed without invoking any recipe.
	{
		var trace []string
		scope := q.NewScope()
		scope.Close()
		_, err := q.Assemble[*Server](&trace, newConfig, newDB, newServer).WithScope(scope)
		fmt.Println("closed-err:", errors.Is(err, q.ErrScopeClosed))
		fmt.Println("closed-trace:", trace)
	}
}
