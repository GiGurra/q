// Fixture: q.AssembleE chain — Wrap / Wrapf / Err / ErrF / Catch
// shape the bubbled error at the outer call site, just like q.TryE.
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }
type DB struct{ cfg *Config }
type Server struct {
	db  *DB
	cfg *Config
}

func newConfig(name string) func() *Config {
	return func() *Config { return &Config{DB: name} }
}

func newDB(c *Config) (*DB, error) {
	if c.DB == "fail-db" {
		return nil, errors.New("dial refused")
	}
	return &DB{cfg: c}, nil
}

func newServer(d *DB, c *Config) *Server { return &Server{db: d, cfg: c} }

var ErrSentinel = errors.New("server init failed")

func bootWrap(cfgName string) (*Server, error) {
	cfg := newConfig(cfgName)()
	return q.AssembleE[*Server](cfg, newDB, newServer).Wrap("server init"), nil
}

func bootErr(cfgName string) (*Server, error) {
	cfg := newConfig(cfgName)()
	return q.AssembleE[*Server](cfg, newDB, newServer).Err(ErrSentinel), nil
}

func bootCatch(cfgName string) (*Server, error) {
	cfg := newConfig(cfgName)()
	return q.AssembleE[*Server](cfg, newDB, newServer).Catch(func(error) (*Server, error) {
		return &Server{cfg: &Config{DB: "fallback"}}, nil
	}), nil
}

func main() {
	s, err := bootWrap("primary")
	fmt.Println("wrap ok:", err == nil, "cfg:", s.cfg.DB)

	_, err = bootWrap("fail-db")
	fmt.Println("wrap err:", err)

	_, err = bootErr("fail-db")
	fmt.Println("err sentinel:", errors.Is(err, ErrSentinel))

	s, _ = bootCatch("fail-db")
	fmt.Println("catch fallback cfg:", s.cfg.DB)
}
