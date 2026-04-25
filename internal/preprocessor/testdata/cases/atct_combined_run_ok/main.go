package main

import (
	"fmt"

	"fixture/cfg"
	"fixture/util"

	"github.com/GiGurra/q/pkg/q"
)

// Combined fixture: cross-call captures + module-local helper packages
// + struct R + slice R, all in one program. Exercises the synthesis
// pass's topo-sort and codec-based pipeline together.

func main() {
	// Cross-call: a → b → c, with b/c using util helpers and the
	// captured int value from earlier.
	a := q.AtCompileTime[int](func() int {
		return util.Square(7) // 49
	})
	b := q.AtCompileTime[string](func() string {
		return util.Stringify("squared", a) // "squared:49"
	})
	c := q.AtCompileTime[[]string](func() []string {
		return []string{b, b, b}
	})
	// Struct R from a sibling package.
	svc := q.AtCompileTime[cfg.Service](func() cfg.Service {
		s := cfg.DefaultService()
		s.Port = a + 1 // captured a (=49) + 1
		return s
	})

	fmt.Println("a:", a)
	fmt.Println("b:", b)
	fmt.Println("c:", c)
	fmt.Println("svc.Name:", svc.Name)
	fmt.Println("svc.Port:", svc.Port)
}
