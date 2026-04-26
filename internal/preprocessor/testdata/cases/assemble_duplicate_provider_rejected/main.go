// Negative fixture: two recipes provide *Config — the resolver can't
// pick between them. The user must remove one or define distinct
// named types per variant.
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }

func newConfig() *Config       { return &Config{DB: "primary"} }
func newOtherConfig() *Config  { return &Config{DB: "other"} }

func main() {
	_, _ = q.Assemble[*Config](newConfig, newOtherConfig).Release()
}
