// Fixture: ctx is just another dependency. Pass it as an inline-
// value recipe; recipes that take context.Context as input receive
// it via interface satisfaction. No special q.AssembleCtx entry —
// the resolver handles ctx like any other inline value.
package main

import (
	"context"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }
type DB struct {
	cfg *Config
	ctx context.Context
}
type Server struct{ db *DB }

func newConfig() *Config                       { return &Config{DB: "x"} }
func newDB(ctx context.Context, c *Config) *DB { return &DB{cfg: c, ctx: ctx} }
func newServer(d *DB) *Server                  { return &Server{db: d} }

func main() {
	ctx := context.WithValue(context.Background(), "k", "v")
	s := q.Unwrap(q.Assemble[*Server](ctx, newConfig, newDB, newServer).Release())
	fmt.Println("ctx threaded:", s.db.ctx.Value("k"))
}
