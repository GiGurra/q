// Fixture: every chain method on q.AssembleE shapes the bubbled error
// the same way q.TryE does — Err / ErrF / Wrap / Wrapf / Catch.
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }
type DB struct{ cfg *Config }
type Server struct{ db *DB }

func newConfig(name string) *Config { return &Config{DB: name} }

func newDB(c *Config) (*DB, error) {
	if c.DB == "fail" {
		return nil, errors.New("dial refused")
	}
	return &DB{cfg: c}, nil
}

func newServer(d *DB) *Server { return &Server{db: d} }

var ErrSentinel = errors.New("sentinel")

func errFn(err error) error { return fmt.Errorf("transformed: %w", err) }

func bootErr(name string) (*Server, error) {
	cfg := newConfig(name)
	return q.AssembleE[*Server](cfg, newDB, newServer).Err(ErrSentinel), nil
}

func bootErrF(name string) (*Server, error) {
	cfg := newConfig(name)
	return q.AssembleE[*Server](cfg, newDB, newServer).ErrF(errFn), nil
}

func bootWrap(name string) (*Server, error) {
	cfg := newConfig(name)
	return q.AssembleE[*Server](cfg, newDB, newServer).Wrap("init"), nil
}

func bootWrapf(name string) (*Server, error) {
	cfg := newConfig(name)
	return q.AssembleE[*Server](cfg, newDB, newServer).Wrapf("init %q", name), nil
}

func bootCatch(name string) (*Server, error) {
	cfg := newConfig(name)
	return q.AssembleE[*Server](cfg, newDB, newServer).Catch(func(error) (*Server, error) {
		return &Server{db: &DB{cfg: &Config{DB: "fallback"}}}, nil
	}), nil
}

func main() {
	// Happy paths — none of the chain methods fire.
	_, err := bootErr("ok")
	fmt.Println("Err happy:", err == nil)
	_, err = bootErrF("ok")
	fmt.Println("ErrF happy:", err == nil)
	_, err = bootWrap("ok")
	fmt.Println("Wrap happy:", err == nil)
	_, err = bootWrapf("ok")
	fmt.Println("Wrapf happy:", err == nil)
	s, err := bootCatch("ok")
	fmt.Println("Catch happy:", err == nil, "cfg:", s.db.cfg.DB)

	// Bubble paths — the recipe's err triggers the chain method.
	_, err = bootErr("fail")
	fmt.Println("Err bubble is sentinel:", errors.Is(err, ErrSentinel))

	_, err = bootErrF("fail")
	fmt.Println("ErrF bubble:", err)

	_, err = bootWrap("fail")
	fmt.Println("Wrap bubble:", err)

	_, err = bootWrapf("fail")
	fmt.Println("Wrapf bubble:", err)

	s, err = bootCatch("fail")
	fmt.Println("Catch bubble cfg:", s.db.cfg.DB, "err:", err)
}
