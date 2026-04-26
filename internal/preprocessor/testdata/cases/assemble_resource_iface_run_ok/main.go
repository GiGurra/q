// Fixture: q.Assemble with interface-typed resource recipe.
// `(Greeter, func(), error)` is just as valid as `(*Greeter, func(), error)`.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Greeter interface{ Greet() string }
type Logger interface{ Log(string) }

type englishGreeter struct{ name string }

func (e englishGreeter) Greet() string { return "hi " + e.name }

type stderrLogger struct{}

func (stderrLogger) Log(s string) {}

var shutdownLog []string

// Resource recipe returning an INTERFACE type.
func openGreeter() (Greeter, func(), error) {
	return englishGreeter{name: "world"}, func() { shutdownLog = append(shutdownLog, "greeter") }, nil
}

// Resource recipe returning a CONCRETE type.
func openLogger() (*stderrLogger, func(), error) {
	return &stderrLogger{}, func() { shutdownLog = append(shutdownLog, "logger") }, nil
}

type App struct {
	g Greeter
	l Logger
}

func newApp(g Greeter, l Logger) *App { return &App{g: g, l: l} }

func main() {
	app, shutdown, err := q.Assemble[*App](openGreeter, openLogger, newApp).NoRelease()
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println("greet:", app.g.Greet())
	shutdown()

	// Reverse-topo: openLogger built last, fires first.
	for _, s := range shutdownLog {
		fmt.Println("-", s)
	}
}
