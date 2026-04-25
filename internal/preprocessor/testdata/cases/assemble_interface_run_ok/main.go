// Fixture: a recipe needs an interface input, satisfied by a concrete
// provider via types.AssignableTo. Exact-type matches always win
// first; this scan only kicks in when no exact provider exists.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Greeter interface{ Greet() string }

type EnglishGreeter struct{}

func (EnglishGreeter) Greet() string { return "hello" }

type App struct{ g Greeter }

func newGreeter() *EnglishGreeter { return &EnglishGreeter{} } // produces *EnglishGreeter
func newApp(g Greeter) *App       { return &App{g: g} }        // wants Greeter (interface)

func main() {
	app := q.Unwrap(q.Assemble[*App](newGreeter, newApp))
	fmt.Println(app.g.Greet())
}
