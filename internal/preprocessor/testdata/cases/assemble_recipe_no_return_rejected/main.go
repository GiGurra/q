// Negative fixture: a recipe that returns no values can't provide a
// type. The resolver must surface a clear shape diagnostic.
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

type Server struct{}

// Returns nothing — invalid recipe shape.
func sideEffect() { _ = "init" }

func newServer() *Server { return &Server{} }

func main() {
	_ = q.Assemble[*Server](sideEffect, newServer)
}
