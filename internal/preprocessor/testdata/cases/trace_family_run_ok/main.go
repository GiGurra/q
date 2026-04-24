// Fixture: exercises bare q.Trace and every q.TraceE chain method.
// The bubble includes a "<filename>:<line>:" prefix captured at
// compile time by the preprocessor. Plain Go can't express this —
// runtime code has no access to its own source location without a
// stack walk — so Trace is a compile-time-only feature.
package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

var ErrReplaced = errors.New("replaced")

func bareTrace(s string) (int, error) {
	n := q.Trace(strconv.Atoi(s))
	return n, nil
}

func errMethod(s string) (int, error) {
	n := q.TraceE(strconv.Atoi(s)).Err(ErrReplaced)
	return n, nil
}

func errFMethod(s string) (int, error) {
	n := q.TraceE(strconv.Atoi(s)).ErrF(func(e error) error {
		return fmt.Errorf("f: %w", e)
	})
	return n, nil
}

func wrapMethod(s string) (int, error) {
	n := q.TraceE(strconv.Atoi(s)).Wrap("parsing")
	return n, nil
}

func wrapfMethod(s string) (int, error) {
	n := q.TraceE(strconv.Atoi(s)).Wrapf("parsing %q", s)
	return n, nil
}

func catchRecover(s string) (int, error) {
	n := q.TraceE(strconv.Atoi(s)).Catch(func(e error) (int, error) {
		return 42, nil
	})
	return n, nil
}

func catchTransform(s string) (int, error) {
	n := q.TraceE(strconv.Atoi(s)).Catch(func(e error) (int, error) {
		return 0, fmt.Errorf("catch: %w", e)
	})
	return n, nil
}

// Each helper returns errors whose prefix we can pattern-match on.
// We don't hard-code the line number — just assert the prefix shape
// starts with "main.go:" and the rest of the message is present.
func report(label string, _ int, err error) {
	if err == nil {
		fmt.Printf("%s: ok\n", label)
		return
	}
	msg := err.Error()
	// Expect "main.go:<line>: …" where <line> is a positive integer.
	// Normalise by replacing the actual line with "N" so the fixture
	// output stays stable across edits.
	colon := -1
	for i := len("main.go:"); i < len(msg); i++ {
		if msg[i] == ':' {
			colon = i
			break
		}
	}
	if colon < 0 {
		fmt.Printf("%s: err=%s\n", label, msg)
		return
	}
	head := msg[:len("main.go:")]
	tail := msg[colon:]
	fmt.Printf("%s: err=%sN%s\n", label, head, tail)
}

func main() {
	_, err := bareTrace("abc")
	report("bare", 0, err)
	_, err = errMethod("abc")
	report("Err", 0, err)
	_, err = errFMethod("abc")
	report("ErrF", 0, err)
	_, err = wrapMethod("abc")
	report("Wrap", 0, err)
	_, err = wrapfMethod("abc")
	report("Wrapf", 0, err)
	n, err := catchRecover("abc")
	if err != nil {
		fmt.Printf("CatchRec: unexpected err=%s\n", err)
	} else {
		fmt.Printf("CatchRec: ok=%d\n", n)
	}
	_, err = catchTransform("abc")
	report("CatchXf", 0, err)

	// Verify the underlying errors.Is / errors.As still work through
	// the trace prefix — fmt.Errorf("%w") keeps the wrap chain intact.
	_, err = bareTrace("abc")
	var numErr *strconv.NumError
	fmt.Printf("unwrap.As: %v\n", errors.As(err, &numErr))
	_, err = errMethod("abc")
	fmt.Printf("unwrap.Is(Replaced): %v\n", errors.Is(err, ErrReplaced))
}
