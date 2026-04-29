// Fixture: IncDecStmt (`x++` / `x--`) with a q.* call in the LHS
// expression flows through the hoist path: the q.* binds to a temp,
// then the IncDecStmt is re-emitted verbatim with the q.* span
// substituted by its `_qTmpN`. Mirrors the compound-assign case for
// AssignStmt — same hoist machinery, different stmt kind.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Status q.Atom

func mapInc() (int, error) {
	m := map[Status]int{q.A[Status](): 1}
	m[q.A[Status]()]++
	m[q.A[Status]()]++
	return m[q.A[Status]()], nil
}

func arrInc() (int, error) {
	idx := func() (int, error) { return 0, nil }
	arr := []int{1}
	arr[q.Try(idx())]++
	return arr[0], nil
}

func main() {
	if v, err := mapInc(); err != nil {
		fmt.Printf("mapInc: err=%s\n", err)
	} else {
		fmt.Printf("mapInc: %d\n", v)
	}
	if v, err := arrInc(); err != nil {
		fmt.Printf("arrInc: err=%s\n", err)
	} else {
		fmt.Printf("arrInc: %d\n", v)
	}
}
