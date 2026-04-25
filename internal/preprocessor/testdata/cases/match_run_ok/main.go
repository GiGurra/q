// Fixture: q.Match — value-returning switch as an expression. Pairs
// with q.Case / q.Default. When V is an enum type and no q.Default
// is provided, the typecheck pass enforces coverage.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Color int

const (
	Red Color = iota
	Green
	Blue
)

func describe(c Color) string {
	return q.Match(c,
		q.Case(Red, "warm"),
		q.Case(Green, "natural"),
		q.Case(Blue, "cool"),
	)
}

// q.Default opts out of the coverage check.
func partial(c Color) string {
	return q.Match(c,
		q.Case(Red, "red-only"),
		q.Default("anything else"),
	)
}

// String-keyed match — works for any comparable type.
func httpStatus(code int) string {
	return q.Match(code,
		q.Case(200, "ok"),
		q.Case(404, "not found"),
		q.Case(500, "internal error"),
		q.Default("unknown"),
	)
}

// Result type can be a richer type (struct, slice, etc.).
type Coords struct{ X, Y int }

func directionVec(dir string) Coords {
	return q.Match(dir,
		q.Case("up", Coords{0, -1}),
		q.Case("down", Coords{0, 1}),
		q.Case("left", Coords{-1, 0}),
		q.Case("right", Coords{1, 0}),
		q.Default(Coords{0, 0}),
	)
}

// Lazy: q.CaseFn / q.DefaultFn — the result is produced by calling
// the supplied func, and only when the arm matches. Side effects in
// non-matching arms must NOT fire.
var sideEffectCount int

func sideEffect(name string) string {
	sideEffectCount++
	return name
}

func lazyDescribe(c Color) string {
	// All result expressions are source-rewritten by the preprocessor,
	// so sideEffect only fires for the matching arm.
	return q.Match(c,
		q.Case(Red, sideEffect("warm")),
		q.Case(Green, sideEffect("natural")),
		q.Case(Blue, sideEffect("cool")),
	)
}

func main() {
	fmt.Println(describe(Red))
	fmt.Println(describe(Green))
	fmt.Println(describe(Blue))
	fmt.Println(partial(Red))
	fmt.Println(partial(Green))
	fmt.Println(httpStatus(200))
	fmt.Println(httpStatus(503))
	fmt.Println(directionVec("up"))
	fmt.Println(directionVec("nowhere"))

	// Lazy: only the matching arm should run sideEffect.
	sideEffectCount = 0
	out := lazyDescribe(Green)
	fmt.Printf("lazyDescribe(Green) = %s sideEffects=%d\n", out, sideEffectCount)

	// Lazy q.Default — source-rewritten, only fires when no q.Case
	// matches.
	sideEffectCount = 0
	out = q.Match("xyz",
		q.Case("a", "alpha"),
		q.Default(sideEffect("default")),
	)
	fmt.Printf("DefaultFn miss = %s sideEffects=%d\n", out, sideEffectCount)
	sideEffectCount = 0
	out = q.Match("a",
		q.Case("a", "alpha"),
		q.Default(sideEffect("default")),
	)
	fmt.Printf("DefaultFn match = %s sideEffects=%d\n", out, sideEffectCount)
}
