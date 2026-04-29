// example/match mirrors docs/api/match.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/match
package main

import (
	"fmt"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- "Switch shape — enum" ----------
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

// ---------- "If-chain shape — predicate arms" ----------

func describeNumber(n int) string {
	getThreshold := func() int { return 10 }
	slowPositive := func(x int) func() bool { return func() bool { return x > 100 } }

	return q.Match(n,
		q.Case(0, "zero"),
		q.Case(n > 0, "positive"),
		q.Case(getThreshold, "matches t"),
		q.Case(slowPositive(n), "complex pos"),
		q.Default("other"),
	)
}

// ---------- "Source-rewriting (laziness for free)" ----------

var calls struct{ expensive, log, fallback int }

func expensive(n int) string { calls.expensive++; return fmt.Sprintf("expensive(%d)", n) }
func logFn(n int) string     { calls.log++; return fmt.Sprintf("log(%d)", n) }
func fallback() string       { calls.fallback++; return "fallback" }

func laziness(n int) string {
	return q.Match(n,
		q.Case(0, expensive(n)),
		q.Case(n > 0, logFn(n)),
		q.Default(fallback()),
	)
}

// ---------- "Composes with non-enum values" ----------

func httpStatus(code int) string {
	return q.Match(code,
		q.Case(200, "ok"),
		q.Case(404, "not found"),
		q.Case(500, "internal error"),
		q.Default("unknown"),
	)
}

// ---------- "Rich result types" ----------
type Coords struct{ X, Y int }

func direction(s string) Coords {
	return q.Match(s,
		q.Case("up", Coords{0, -1}),
		q.Case("down", Coords{0, 1}),
		q.Case("left", Coords{-1, 0}),
		q.Case("right", Coords{1, 0}),
		q.Default(Coords{0, 0}),
	)
}

// ---------- "Discriminated-sum dispatch" ----------
type Pending struct{}
type Done struct{ At time.Time }
type Failed struct{ Err error }

type Status q.OneOf3[Pending, Done, Failed]

func describeStatus(s Status) string {
	return q.Match(s,
		q.Case(Pending{}, "waiting"),
		q.OnType(func(d Done) string { return "done at " + d.At.Format("15:04:05") }),
		q.OnType(func(f Failed) string { return "failed: " + f.Err.Error() }),
	)
}

func main() {
	fmt.Printf("describe(Red): %s\n", describe(Red))
	fmt.Printf("describe(Green): %s\n", describe(Green))
	fmt.Printf("describe(Blue): %s\n", describe(Blue))

	fmt.Printf("describeNumber(0): %s\n", describeNumber(0))
	fmt.Printf("describeNumber(5): %s\n", describeNumber(5))
	fmt.Printf("describeNumber(-7): %s\n", describeNumber(-7))

	calls = struct{ expensive, log, fallback int }{}
	fmt.Printf("laziness(0): %s, expensive=%d log=%d fallback=%d\n",
		laziness(0), calls.expensive, calls.log, calls.fallback)
	calls = struct{ expensive, log, fallback int }{}
	fmt.Printf("laziness(5): %s, expensive=%d log=%d fallback=%d\n",
		laziness(5), calls.expensive, calls.log, calls.fallback)
	calls = struct{ expensive, log, fallback int }{}
	fmt.Printf("laziness(-3): %s, expensive=%d log=%d fallback=%d\n",
		laziness(-3), calls.expensive, calls.log, calls.fallback)

	fmt.Printf("httpStatus(200): %s\n", httpStatus(200))
	fmt.Printf("httpStatus(418): %s\n", httpStatus(418))

	fmt.Printf("direction(left): %+v\n", direction("left"))
	fmt.Printf("direction(?): %+v\n", direction("?"))

	when, _ := time.Parse(time.RFC3339, "2026-01-02T12:34:56Z")
	fmt.Printf("describeStatus(Pending): %s\n", describeStatus(Status{Tag: 1, Value: Pending{}}))
	fmt.Printf("describeStatus(Done): %s\n", describeStatus(Status{Tag: 2, Value: Done{At: when}}))
	fmt.Printf("describeStatus(Failed): %s\n", describeStatus(Status{Tag: 3, Value: Failed{Err: fmt.Errorf("boom")}}))
}
