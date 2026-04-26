// Negative fixture: q.AssembleStruct[App] where one field type
// (*Worker) has no matching recipe. The resolver must surface a
// per-field missing-provider diagnostic naming the field.
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

type Server struct{}
type Worker struct{}
type App struct {
	Server *Server
	Worker *Worker
}

func newServer() *Server { return nil }

func main() {
	// No recipe provides *Worker.
	_, _ = q.AssembleStruct[App](newServer).Release()
}
