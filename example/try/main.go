// example/try — every shape of q.Try and q.TryE, on both ok and
// failure paths. Run with:
//
//	go run -toolexec=q ./example/try
package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

var ErrBadInput = errors.New("bad input")

// bareTry — the smallest form. Bubble the captured err unchanged.
func bareTry(s string) (int, error) {
	n := q.Try(strconv.Atoi(s))
	return n, nil
}

// tryWithErr — replace the bubbled error with a constant.
func tryWithErr(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).Err(ErrBadInput)
	return n, nil
}

// tryWithErrF — transform the bubbled error via a function.
func tryWithErrF(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).ErrF(func(e error) error {
		return fmt.Errorf("wrapped: %w", e)
	})
	return n, nil
}

// tryWithWrap — prefix a message onto the bubbled error via
// fmt.Errorf("%s: %w", ...). errors.Is traverses the wrap.
func tryWithWrap(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).Wrap("parsing")
	return n, nil
}

// tryWithWrapf — like Wrap but with format args. Format must be
// a string literal (the rewriter splices `: %w` into it).
func tryWithWrapf(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).Wrapf("parsing %q", s)
	return n, nil
}

// tryWithCatchRecover — on error, return a default value and skip
// the bubble entirely.
func tryWithCatchRecover(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).Catch(func(e error) (int, error) {
		if errors.Is(e, strconv.ErrSyntax) {
			return 0, nil // recovered; bubble is skipped
		}
		return 0, fmt.Errorf("unexpected: %w", e)
	})
	return n, nil
}

// tryWithCatchBubble — on error, bubble a different error.
func tryWithCatchBubble(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).Catch(func(e error) (int, error) {
		return 0, fmt.Errorf("caught: %w", e)
	})
	return n, nil
}

func main() {
	cases := []struct {
		name string
		fn   func(string) (int, error)
	}{
		{"bareTry", bareTry},
		{"tryWithErr", tryWithErr},
		{"tryWithErrF", tryWithErrF},
		{"tryWithWrap", tryWithWrap},
		{"tryWithWrapf", tryWithWrapf},
		{"tryWithCatchRecover", tryWithCatchRecover},
		{"tryWithCatchBubble", tryWithCatchBubble},
	}

	for _, c := range cases {
		for _, input := range []string{"21", "abc"} {
			n, err := c.fn(input)
			if err != nil {
				fmt.Printf("%-24s(%q) => err: %v\n", c.name, input, err)
			} else {
				fmt.Printf("%-24s(%q) => %d\n", c.name, input, n)
			}
		}
	}
}
