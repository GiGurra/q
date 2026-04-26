// Fixture: q.AssembleAll — multi-provider aggregation. Every recipe
// whose output is assignable to T contributes one element to the
// resulting []T, in recipe declaration order. The motivating shape
// is plugin / handler / middleware sets where multiple distinct
// concrete types satisfy a common interface.
package main

import (
	"fmt"
	"sort"

	"github.com/GiGurra/q/pkg/q"
)

type Plugin interface{ Name() string }

type AuthPlugin struct{}
type LogPlugin struct{}
type MetricsPlugin struct{}

func (AuthPlugin) Name() string    { return "auth" }
func (LogPlugin) Name() string     { return "log" }
func (MetricsPlugin) Name() string { return "metrics" }

func newAuth() Plugin    { return AuthPlugin{} }
func newLog() Plugin     { return LogPlugin{} }
func newMetrics() Plugin { return MetricsPlugin{} }

func main() {
	plugins := q.Unwrap(q.AssembleAll[Plugin](newAuth, newLog, newMetrics).DeferCleanup())

	names := make([]string, 0, len(plugins))
	for _, p := range plugins {
		names = append(names, p.Name())
	}
	sort.Strings(names)
	fmt.Println("plugin count:", len(plugins))
	fmt.Println("plugins:", names)
}
