// Fixture: q.AssembleAll[Plugin] where two distinct providers each
// depend on a shared *Config recipe. The topo sort must produce the
// *Config exactly once and feed its single _qDep into both plugin
// constructors. Verifies that transitive deps still flow through
// the same auto-derived graph as q.Assemble.
package main

import (
	"fmt"
	"sort"

	"github.com/GiGurra/q/pkg/q"
)

type Plugin interface{ Describe() string }

type Config struct {
	Region string
}

type AuthPlugin struct {
	cfg *Config
}

type LogPlugin struct {
	cfg *Config
}

func (a AuthPlugin) Describe() string { return "auth@" + a.cfg.Region }
func (l LogPlugin) Describe() string  { return "log@" + l.cfg.Region }

func newConfig() *Config       { return &Config{Region: "eu-west-1"} }
func newAuth(c *Config) Plugin { return AuthPlugin{cfg: c} }
func newLog(c *Config) Plugin  { return LogPlugin{cfg: c} }

func main() {
	plugins := q.Unwrap(q.AssembleAll[Plugin](newConfig, newAuth, newLog).Release())

	descs := make([]string, 0, len(plugins))
	for _, p := range plugins {
		descs = append(descs, p.Describe())
	}
	sort.Strings(descs)
	fmt.Println("count:", len(plugins))
	fmt.Println("plugins:", descs)
}
