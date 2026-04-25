// Fixture: q.WithAssemblyDebug enables per-step trace output. The
// ctx is supplied to q.Assemble as an inline-value recipe; the
// rewriter detects it and binds _qDbg from it for the optional
// trace prelude. q.WithAssemblyDebugWriter redirects to a custom
// writer (here a bytes.Buffer for assertion).
//
// Notably: the ctx-only-for-debug case works — even though no
// recipe consumes ctx as an input, the assembly accepts it
// (context.Context is exempt from the unused-recipe check).
package main

import (
	"bytes"
	"context"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }
type DB struct{ cfg *Config }
type Server struct{ db *DB }

func newConfig() *Config { return &Config{DB: "x"} }
func newDB(c *Config) *DB { return &DB{cfg: c} }
func newServer(d *DB) *Server { return &Server{db: d} }

func main() {
	// Without WithAssemblyDebug — no trace output. ctx is supplied
	// but no recipe consumes it; the unused-recipe exemption for
	// context.Context lets the build succeed.
	ctxQuiet := context.Background()
	_ = q.Unwrap(q.Assemble[*Server](ctxQuiet, newConfig, newDB, newServer))
	fmt.Println("quiet ok")

	// With WithAssemblyDebugWriter — trace prints to the buffer.
	var buf bytes.Buffer
	ctxLoud := q.WithAssemblyDebugWriter(context.Background(), &buf)
	_ = q.Unwrap(q.Assemble[*Server](ctxLoud, newConfig, newDB, newServer))
	fmt.Print(buf.String())
}
