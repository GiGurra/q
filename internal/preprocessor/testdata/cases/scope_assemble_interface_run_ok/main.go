// Fixture: cross-assembly cache hit through an interface dependency.
//
// Assembly 1 builds a *Greeter directly. Assembly 2 needs a Greeter
// (interface) input — it lists newGreeter (the *EnglishGreeter
// recipe) in its recipes, so the rewriter resolves the iface input
// to the concrete recipe at compile time. The cache key emitted is
// the concrete type's key, which matches Assembly 1's cache entry.
//
// Net effect: newGreeter runs ONCE total, both assemblies see the
// same instance, and scope.Close fires the concrete's cleanup once.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Greeter interface{ Greet() string }

type EnglishGreeter struct {
	id  int
	out *[]string
}

func (g *EnglishGreeter) Greet() string                        { return fmt.Sprintf("hello#%d", g.id) }
func (g *EnglishGreeter) Close()                                { *g.out = append(*g.out, fmt.Sprintf("greeter.Close#%d", g.id)) }

type App struct {
	g  Greeter
	id int
}

var nextID int

func nextN() int { nextID++; return nextID }

func newGreeter(out *[]string) *EnglishGreeter {
	*out = append(*out, "build:greeter")
	return &EnglishGreeter{id: nextN(), out: out}
}

func newApp(g Greeter) *App {
	return &App{g: g, id: nextN()}
}

func main() {
	var trace []string
	scope, shutdown := q.NewScope().NoDeferCleanup()

	// Assembly 1: cache the concrete *EnglishGreeter under its
	// concrete-type key.
	g1 := q.Unwrap(q.Assemble[*EnglishGreeter](&trace, newGreeter).WithScope(scope))

	// Assembly 2: needs Greeter (interface). Lists newGreeter in
	// its recipes; the rewriter resolves Greeter → *EnglishGreeter
	// at compile time, so the cache key matches Assembly 1's entry
	// and newGreeter is NOT invoked again.
	app := q.Unwrap(q.Assemble[*App](&trace, newGreeter, newApp).WithScope(scope))

	// Sanity: same instance flows through both assemblies.
	fmt.Println("same:", any(app.g) == any(g1))
	fmt.Println("greet:", app.g.Greet())
	fmt.Println("trace:", trace)

	shutdown()
	fmt.Println("post-close:", trace)
}
