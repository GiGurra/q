// Negative fixture: two concrete recipes both implement the interface
// the consumer needs. The resolver must reject the call so the user
// disambiguates (drop one or define distinct named types per variant).
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

type Greeter interface{ Greet() string }

type EnglishGreeter struct{}
type SpanishGreeter struct{}

func (EnglishGreeter) Greet() string { return "hello" }
func (SpanishGreeter) Greet() string { return "hola" }

type App struct{ g Greeter }

func newEN() *EnglishGreeter { return &EnglishGreeter{} }
func newES() *SpanishGreeter { return &SpanishGreeter{} }
func newApp(g Greeter) *App  { return &App{g: g} }

func main() {
	// Both *EnglishGreeter and *SpanishGreeter satisfy Greeter — ambiguous.
	_, _ = q.Assemble[*App](newEN, newES, newApp).Release()
}
