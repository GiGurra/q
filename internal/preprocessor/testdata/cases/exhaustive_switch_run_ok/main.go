// Fixture: q.Exhaustive(v) — the typecheck pass enforces that every
// constant of v's type appears in some case clause. The wrapper is
// stripped at rewrite time, leaving a plain switch.
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

type Status string

const (
	Pending Status = "pending"
	Done    Status = "done"
	Failed  Status = "failed"
)

func describeColor(c Color) string {
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

// Multi-value case: covers two constants in one clause.
func describeColorGroup(c Color) string {
	switch q.Exhaustive(c) {
	case Red, Blue:
		return "extreme"
	case Green:
		return "middle"
	}
	return "unknown"
}

// String-typed enum.
func describeStatus(s Status) string {
	switch q.Exhaustive(s) {
	case Pending:
		return "wait"
	case Done:
		return "ok"
	case Failed:
		return "uh oh"
	}
	return "unknown"
}

// Default catches unknown values (e.g. forward-compat for Lax-JSON
// types or runtime drift) but does NOT replace coverage of declared
// constants. Every const must still appear in some case clause, AND
// the default arm handles values outside the declared set.
func describeColorWithDefault(c Color) string {
	switch q.Exhaustive(c) {
	case Red:
		return "warm-with-default"
	case Green:
		return "natural-with-default"
	case Blue:
		return "cool-with-default"
	default:
		return "unknown — possibly a future Color value"
	}
}

// Switch with init clause + Exhaustive in tag.
func describeFromInit() string {
	switch c := pickColor(); q.Exhaustive(c) {
	case Red:
		return "init/red"
	case Green:
		return "init/green"
	case Blue:
		return "init/blue"
	}
	return "init/unknown"
}

func pickColor() Color { return Green }

func main() {
	allColors := q.EnumValues[Color]()
	allStatuses := q.EnumValues[Status]()
	for _, c := range allColors {
		fmt.Printf("%s: %s\n", q.EnumName[Color](c), describeColor(c))
	}
	for _, c := range allColors {
		fmt.Printf("group %s: %s\n", q.EnumName[Color](c), describeColorGroup(c))
	}
	for _, s := range allStatuses {
		fmt.Printf("status %s: %s\n", q.EnumName[Status](s), describeStatus(s))
	}
	fmt.Printf("default red: %s\n", describeColorWithDefault(Red))
	fmt.Printf("default green: %s\n", describeColorWithDefault(Green))
	fmt.Printf("from init: %s\n", describeFromInit())
}
