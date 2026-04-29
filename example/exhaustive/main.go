// example/exhaustive mirrors docs/api/exhaustive.md one-to-one.
// Run with:
//
//	go run -toolexec=q ./example/exhaustive
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- "At a glance — Color enum" ----------
type Color int

const (
	Red Color = iota
	Green
	Blue
)

//	switch q.Exhaustive(c) {
//	case Red: return "warm"
//	case Green: return "natural"
//	case Blue: return "cool"
//	}
func describe(c Color) string {
	switch q.Exhaustive(c) {
	case Red:
		return "warm"
	case Green:
		return "natural"
	case Blue:
		return "cool"
	}
	return "unknown"
}

// "Default for forward-compat":
//
//	switch q.Exhaustive(c) {
//	case Red:   return "red"
//	case Green: return "green"
//	case Blue:  return "blue"
//	default:    return "unknown"
//	}
func describeWithDefault(c Color) string {
	switch q.Exhaustive(c) {
	case Red:
		return "red"
	case Green:
		return "green"
	case Blue:
		return "blue"
	default:
		return "unknown"
	}
}

// "Multi-value cases work":
func warmCool(c Color) string {
	switch q.Exhaustive(c) {
	case Red, Green:
		return "warm-or-natural"
	case Blue:
		return "cool"
	}
	return ""
}

// "Switch-with-init works":
//
//	switch x := f(); q.Exhaustive(x) { … }
func switchWithInit() string {
	switch x := pickColor(); q.Exhaustive(x) {
	case Red:
		return "red"
	case Green:
		return "green"
	case Blue:
		return "blue"
	}
	return ""
}

func pickColor() Color { return Green }

// ---------- "q.OneOfN type-switch coverage" ----------
type Pending struct{}
type Done struct{ At int }
type Failed struct{ Err string }

type Status q.OneOf3[Pending, Done, Failed]

func describeStatus(s Status) string {
	switch v := q.Exhaustive(s.Value).(type) {
	case Pending:
		return "pending"
	case Done:
		return fmt.Sprintf("done@%d", v.At)
	case Failed:
		return "failed: " + v.Err
	}
	return ""
}

func main() {
	fmt.Printf("describe(Red): %s\n", describe(Red))
	fmt.Printf("describe(Green): %s\n", describe(Green))
	fmt.Printf("describe(Blue): %s\n", describe(Blue))

	fmt.Printf("describeWithDefault(Color(99)): %s\n", describeWithDefault(Color(99)))

	fmt.Printf("warmCool(Red): %s\n", warmCool(Red))
	fmt.Printf("warmCool(Blue): %s\n", warmCool(Blue))

	fmt.Printf("switchWithInit: %s\n", switchWithInit())

	fmt.Printf("describeStatus(Pending): %s\n", describeStatus(Status{Tag: 1, Value: Pending{}}))
	fmt.Printf("describeStatus(Done@42): %s\n", describeStatus(Status{Tag: 2, Value: Done{At: 42}}))
	fmt.Printf("describeStatus(Failed): %s\n", describeStatus(Status{Tag: 3, Value: Failed{Err: "boom"}}))
}
