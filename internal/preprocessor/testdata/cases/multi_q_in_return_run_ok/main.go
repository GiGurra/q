// Fixture: multiple q.* calls in a single return expression. Each
// binds to its own _qTmpN with its own bubble check, and the final
// return substitutes all of them back. Earlier failures short-circuit
// because each has its own early return.
package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

// Three Try calls in one arithmetic return. Any one failing bubbles
// that call's error without running later calls.
func compute(a, b, c string) (int, error) {
	return q.Try(strconv.Atoi(a)) * q.Try(strconv.Atoi(b)) / q.Try(strconv.Atoi(c)), nil
}

// Mixed Try + NotNil in one return — exercises per-sub-call family
// dispatch.
func mix(s string, p *int) (int, error) {
	return q.Try(strconv.Atoi(s)) + *q.NotNil(p), nil
}

// Two TryE chain calls with different wrap methods.
func twoWraps(a, b string) (int, error) {
	return q.TryE(strconv.Atoi(a)).Wrap("first") + q.TryE(strconv.Atoi(b)).Wrap("second"), nil
}

// Order of evaluation: later q.*s must not execute when an earlier
// one fails. Count invocations via a package-level counter.
var calls int

func counted(s string) (int, error) {
	calls++
	n, err := strconv.Atoi(s)
	return n, err
}

func shortCircuit(a, b string) (int, error) {
	return q.Try(counted(a)) + q.Try(counted(b)), nil
}

var ErrCustom = errors.New("custom")

func report(name string, n int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
	} else {
		fmt.Printf("%s: ok=%d\n", name, n)
	}
}

func main() {
	n, err := compute("2", "3", "5")
	report("compute.okok", n, err)
	n, err = compute("xx", "3", "5")
	report("compute.badA", n, err)
	n, err = compute("2", "yy", "5")
	report("compute.badB", n, err)
	n, err = compute("2", "3", "zz")
	report("compute.badC", n, err)

	x := 7
	good := &x
	n, err = mix("3", good)
	report("mix.ok", n, err)
	n, err = mix("3", nil)
	report("mix.nilPtr", n, err)
	n, err = mix("xx", good)
	report("mix.badS", n, err)

	n, err = twoWraps("4", "9")
	report("twoWraps.okok", n, err)
	n, err = twoWraps("xx", "9")
	report("twoWraps.badA", n, err)
	n, err = twoWraps("4", "yy")
	report("twoWraps.badB", n, err)

	calls = 0
	n, err = shortCircuit("xx", "99")
	report("shortCircuit.badFirst", n, err)
	fmt.Printf("shortCircuit.callsAfterBadFirst=%d\n", calls)

	calls = 0
	n, err = shortCircuit("1", "2")
	report("shortCircuit.okok", n, err)
	fmt.Printf("shortCircuit.callsAfterOkOk=%d\n", calls)
}
