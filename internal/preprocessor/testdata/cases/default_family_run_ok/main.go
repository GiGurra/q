// Fixture: q.Default unconditionally swallows err and substitutes a
// fallback. q.DefaultE.When(pred) gates the fallback on a
// predicate — matching errors → fallback, others bubble.
package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

var ErrSoft = errors.New("soft")
var ErrHard = errors.New("hard")

// parseOrFallback uses the 2-arg (call, fb) form.
func parseOrFallback(s string) int {
	return q.Default(strconv.Atoi(s), 99)
}

// parseOrFallback3 uses the 3-arg (v, err, fb) form.
func parseOrFallback3(s string) int {
	v, err := strconv.Atoi(s)
	return q.Default(v, err, 77)
}

// loader always fails with a caller-chosen error so we can gate
// DefaultE.When against specific cases.
func loader(e error) (int, error) { return 0, e }

// softOnly falls back only on ErrSoft; bubbles everything else.
func softOnly(e error) (int, error) {
	return q.DefaultE(loader(e), 42).When(func(err error) bool {
		return errors.Is(err, ErrSoft)
	}), nil
}

// assignForm exercises the assign form.
func assignForm(s string) int {
	var n int
	n = q.Default(strconv.Atoi(s), -1)
	return n
}

// returnForm exercises the return form.
func returnForm(s string) (int, error) {
	return q.Default(strconv.Atoi(s), 123), nil
}

// hoistForm exercises the hoist form.
func identity(x int) int { return x }

func hoistForm(s string) int {
	return identity(q.Default(strconv.Atoi(s), 55))
}

func main() {
	fmt.Printf("bare.ok: %d\n", parseOrFallback("21"))
	fmt.Printf("bare.fb: %d\n", parseOrFallback("abc"))

	fmt.Printf("three.ok: %d\n", parseOrFallback3("7"))
	fmt.Printf("three.fb: %d\n", parseOrFallback3("xyz"))

	n, err := softOnly(ErrSoft)
	fmt.Printf("When.soft: n=%d err=%v\n", n, err)
	n, err = softOnly(ErrHard)
	if err != nil {
		fmt.Printf("When.hard: err=%s\n", err)
	} else {
		fmt.Printf("When.hard: unexpected n=%d\n", n)
	}

	fmt.Printf("assign.ok: %d\n", assignForm("5"))
	fmt.Printf("assign.fb: %d\n", assignForm("bad"))

	n, err = returnForm("9")
	fmt.Printf("return.ok: n=%d err=%v\n", n, err)
	n, err = returnForm("bad")
	fmt.Printf("return.fb: n=%d err=%v\n", n, err)

	fmt.Printf("hoist.ok: %d\n", hoistForm("11"))
	fmt.Printf("hoist.fb: %d\n", hoistForm("bad"))
}
