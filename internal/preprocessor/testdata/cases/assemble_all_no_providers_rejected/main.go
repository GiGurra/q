// Negative fixture: q.AssembleAll[Plugin] with no recipe whose
// output is assignable to Plugin. The would-be success path returns
// an empty []Plugin, which is almost certainly a mistake — the
// resolver surfaces it as a build-time error instead.
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

type Plugin interface{ Name() string }
type Config struct{}

func newConfig() *Config { return &Config{} }

func main() {
	// *Config is not assignable to Plugin.
	_, _ = q.AssembleAll[Plugin](newConfig).Release()
}
