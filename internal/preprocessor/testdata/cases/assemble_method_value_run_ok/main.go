// Fixture: method values as recipes. `srv.NewDB` is a function value
// of type `func(*Config) *DB`; the resolver should treat it like any
// top-level function reference.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }
type DB struct{ cfg *Config }
type Server struct{ db *DB }

// Service holds the constructors as methods so we can pass method
// values as recipes.
type Service struct{ tag string }

func (s *Service) NewConfig() *Config       { return &Config{DB: s.tag} }
func (s *Service) NewDB(c *Config) *DB      { return &DB{cfg: c} }
func (s *Service) NewServer(d *DB) *Server { return &Server{db: d} }

func main() {
	svc := &Service{tag: "method-value"}
	s := q.Assemble[*Server](svc.NewConfig, svc.NewDB, svc.NewServer)
	fmt.Println("cfg:", s.db.cfg.DB)
}
