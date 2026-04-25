package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// Phase 3 fixture: q.* calls inside the AtCompileTime closure body.
// The synthesis pass invokes its subprocess with -toolexec=<qBin>
// so the inner q.* calls get rewritten before the subprocess
// compiles.
//
// Closure bodies must return a single value (no error slot), so the
// q.* family used inside is restricted to NON-BUBBLING helpers —
// q.Match, q.Upper / Snake / Camel, q.F, q.SQL, q.Fields, etc.
// q.Try / q.Check / q.Recv / q.Open et al. would bubble into a
// non-existent error return, which won't typecheck.

func main() {
	label := q.AtCompileTime[string](func() string {
		v := 2
		return q.Match(v,
			q.Case(1, "one"),
			q.Case(2, "two"),
			q.Case(3, "three"),
			q.Default("other"),
		)
	})
	upper := q.AtCompileTime[string](func() string {
		return q.Upper("hello world")
	})
	snake := q.AtCompileTime[string](func() string {
		return q.Snake("HelloWorldFoo")
	})
	formatted := q.AtCompileTime[string](func() string {
		name := "alice"
		age := 30
		return q.F("hi {name}, age {age}")
	})
	fmt.Println("label:", label)
	fmt.Println("upper:", upper)
	fmt.Println("snake:", snake)
	fmt.Println("formatted:", formatted)
}
