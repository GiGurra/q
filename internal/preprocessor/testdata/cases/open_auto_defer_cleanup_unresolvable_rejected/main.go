// Negative fixture: q.Open(...).DeferCleanup() (auto) on a type with no
// recognised cleanup. The typecheck pass must surface a clear
// diagnostic naming the type and pointing at the two acceptable
// fixes (explicit cleanup or .NoDeferCleanup()).
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

// plainResource has no Close method and isn't a channel — auto-DeferCleanup
// has no shape to dispatch to.
type plainResource struct{ id int }

func openPlain() (*plainResource, error) {
	return &plainResource{id: 1}, nil
}

func use() error {
	_ = q.Open(openPlain()).DeferCleanup()
	return nil
}

func main() {
	_ = use()
}
