// Fixture: q.Exhaustive / q.Match on a q.GenEnumJSONLax-opted type
// build successfully when the switch / match has an explicit default
// arm. Pairs with the …_rejected fixtures that prove the build fails
// without one.
package main

import (
	"encoding/json"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Status int

const (
	StatusActive Status = iota
	StatusBlocked
)

// Opt Status into Lax JSON — wire format admits unknown values.
var _ = q.GenEnumJSONLax[Status]()

// q.Exhaustive on a Lax-opted type: every declared constant is
// covered AND a default: arm catches unknown values.
func describe(s Status) string {
	switch q.Exhaustive(s) {
	case StatusActive:
		return "active"
	case StatusBlocked:
		return "blocked"
	default:
		return "unknown"
	}
}

// q.Match on a Lax-opted type: same rule — q.Default arm required.
func describeMatch(s Status) string {
	return q.Match(s,
		q.Case(StatusActive, "active"),
		q.Case(StatusBlocked, "blocked"),
		q.Default("unknown"),
	)
}

func main() {
	fmt.Println("Exhaustive Active:", describe(StatusActive))
	fmt.Println("Exhaustive Blocked:", describe(StatusBlocked))
	// A wire-only value (Lax JSON unmarshals it without error).
	var s Status
	_ = json.Unmarshal([]byte(`99`), &s)
	fmt.Println("Exhaustive Unknown:", describe(s))

	fmt.Println("Match Active:", describeMatch(StatusActive))
	fmt.Println("Match Unknown:", describeMatch(s))
}
