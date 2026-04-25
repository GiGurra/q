// Negative fixture: q.Open(...).Release() (auto) on a type with no
// recognised cleanup. The typecheck pass must surface a clear
// diagnostic naming the type and pointing at the two acceptable
// fixes (explicit cleanup or .NoRelease()).
package main

import (
	"github.com/GiGurra/q/pkg/q"
)

// plainResource has no Close method and isn't a channel — auto-Release
// has no shape to dispatch to.
type plainResource struct{ id int }

func openPlain() (*plainResource, error) {
	return &plainResource{id: 1}, nil
}

func use() error {
	_ = q.Open(openPlain()).Release()
	return nil
}

func main() {
	_ = use()
}
