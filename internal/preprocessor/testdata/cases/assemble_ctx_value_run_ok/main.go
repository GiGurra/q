// Fixture: a recipe takes context.Context as one of its inputs. The
// caller passes ctx as an inline value at the q.Assemble call site;
// the resolver matches it against the input slot via interface
// satisfaction (context.Context is an interface; the caller's
// context.Context value satisfies it).
package main

import (
	"context"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }
type DB struct{ cfg *Config; ctx context.Context }
type Server struct{ db *DB }

func newConfig() *Config             { return &Config{DB: "x"} }
func newDB(ctx context.Context, c *Config) *DB { return &DB{cfg: c, ctx: ctx} }
func newServer(d *DB) *Server        { return &Server{db: d} }

func main() {
	ctx := context.WithValue(context.Background(), "k", "v")
	s := q.Assemble[*Server](ctx, newConfig, newDB, newServer)
	fmt.Println("ctx threaded:", s.db.ctx.Value("k"))
}
