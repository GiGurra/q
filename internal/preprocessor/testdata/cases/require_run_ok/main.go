// Fixture: q.Require — bubble-shape replacement for the old q.Assert.
// Statement-only; rewrites to `if !(cond) { return …, errors.New(…) }`.
// Covered:
//   - cond true: pass-through, no bubble.
//   - cond false with message: bubble carries the message after the
//     "q.Require failed file:line: " prefix.
//   - cond false with no message: bubble carries just the prefix.
//   - both enclosing function shapes: `error` only and `(T, error)`.
//   - file:line points at the q.Require call site (not at some helper).
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// stripLineNumber replaces the digits after "main.go:" with "N" so
// the expected-run output is stable across edits.
func stripLineNumber(s string) string {
	marker := "main.go:"
	i := 0
	for ; i+len(marker) <= len(s); i++ {
		if s[i:i+len(marker)] == marker {
			break
		}
	}
	if i+len(marker) > len(s) {
		return s
	}
	j := i + len(marker)
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	return s[:i+len(marker)] + "N" + s[j:]
}

// ---- error-only signature ----

func requirePassesErr(n int) error {
	q.Require(n > 0, "n must be positive")
	return nil
}

func requireFailsErr(n int) error {
	q.Require(n > 0, "n must be positive")
	return nil
}

func requireNoMsgErr(n int) error {
	q.Require(n > 0)
	return nil
}

// ---- (T, error) signature — proves zeros pull from full result list ----

func requirePassesT(n int) (int, error) {
	q.Require(n > 0, "n must be positive")
	return n * 2, nil
}

func requireFailsT(n int) (int, error) {
	q.Require(n > 0, "n must be positive")
	return n * 2, nil
}

// ---- runtime-evaluated message expression ----

func requireDynamicMsg(name string) error {
	q.Require(name != "", "name = "+name+" (must be non-empty)")
	return nil
}

// ---- report ----

func report(label string, err error) {
	if err == nil {
		fmt.Printf("%s: ok\n", label)
		return
	}
	fmt.Printf("%s: %s\n", label, stripLineNumber(err.Error()))
}

func main() {
	report("requirePassesErr", requirePassesErr(7))
	report("requireFailsErr", requireFailsErr(0))
	report("requireNoMsgErr", requireNoMsgErr(0))

	v, err := requirePassesT(7)
	if err != nil {
		report("requirePassesT", err)
	} else {
		fmt.Printf("requirePassesT: ok=%d\n", v)
	}

	v, err = requireFailsT(0)
	if err != nil {
		report("requireFailsT", err)
	} else {
		fmt.Printf("requireFailsT: ok=%d\n", v)
	}

	report("requireDynamicMsg", requireDynamicMsg(""))

	// Sentinel identity: every q.Require bubble wraps q.ErrRequireFailed
	// via %w, so errors.Is succeeds across the wrap.
	err = requireFailsErr(0)
	fmt.Printf("require.is: %v\n", errors.Is(err, q.ErrRequireFailed))
}
