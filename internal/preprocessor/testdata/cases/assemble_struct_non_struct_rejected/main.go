// Negative fixture: q.AssembleStruct[T] requires T to be a struct.
// Pointer/value/interface targets must use q.Assemble[T] instead.
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

type Server struct{}

func newServer() *Server { return nil }

func main() {
	// *Server is a pointer, not a struct underlying.
	_, _ = q.AssembleStruct[*Server](newServer).Release()
}
