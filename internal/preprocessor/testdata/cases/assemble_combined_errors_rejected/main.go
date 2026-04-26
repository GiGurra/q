// Negative fixture: one q.Assemble call exhibits *multiple* problems
// at once. The resolver must list every one in a single multi-line
// diagnostic, not bail on the first — the user fixes all of them and
// reruns instead of round-tripping per problem.
//
// This fixture intentionally combines:
//   - Duplicate provider for *Config (two recipes produce it)
//   - Missing recipe for *Cache (newServer needs it, nobody provides)
//   - Unrelated provider supplied (string from `unrelated`)
//
// The cycle / unused diagnostics are mutually exclusive with the
// missing-recipe one (the unused-recipe pass only runs when the rest
// of the graph is healthy), so this fixture covers what CAN coexist.
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }
type Cache struct{}
type Server struct {
	cfg   *Config
	cache *Cache
}

func newConfig() *Config       { return &Config{DB: "primary"} }
func newOtherConfig() *Config  { return &Config{DB: "other"} } // duplicate provider for *Config
func newServer(c *Config, ch *Cache) *Server { return &Server{cfg: c, cache: ch} }
// *Cache is missing — nobody provides it.
func unrelated() string { return "stray" } // declared but unused; not part of this graph

func main() {
	_, _ = q.Assemble[*Server](newConfig, newOtherConfig, newServer).DeferCleanup()
	_ = unrelated
}
