package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// Phase 4.2 fixture: recursive q.AtCompileTime — a comptime closure
// that itself contains a q.AtCompileTime call. The outer synthesis
// pass writes a main.go containing the inner q.AtCompileTime; that
// main.go gets compiled with -toolexec=<qBin>, so the inner q
// invocation processes the inner call recursively (creating its own
// .q-comptime-<hash>/ directory).

func main() {
	// Outer closure embeds an inner q.AtCompileTime call. The inner
	// produces 21; the outer doubles it.
	doubled := q.AtCompileTime[int](func() int {
		inner := q.AtCompileTime[int](func() int { return 21 })
		return inner * 2
	})
	// Two-level deep — outer captures the result of an inner that
	// itself uses a sibling stdlib helper.
	hashed := q.AtCompileTime[string](func() string {
		base := q.AtCompileTime[string](func() string {
			return "comptime"
		})
		return base + "/" + base
	})
	fmt.Println("doubled:", doubled)
	fmt.Println("hashed:", hashed)
}
