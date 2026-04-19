// example/nested — q.* nested inside another q.*'s argument, and
// q.* nested inside a larger expression. The rewriter renders
// innermost-first so inner q.Try's _qTmp feeds the outer's bind.
// Run with:
//
//	go run -toolexec=q ./example/nested
package main

import (
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

// pipeline — two (T, error) calls chained. The inner q.Try feeds
// the outer Foo which itself returns (T, error), and that feeds the
// outer q.Try.
//
//	x := q.Try(Foo(q.Try(strconv.Atoi(s))))
func pipeline(s string) (int, error) {
	x := q.Try(Foo(q.Try(strconv.Atoi(s))))
	return x, nil
}

// Foo takes an int and returns (int, error), with an error when n
// is negative.
func Foo(n int) (int, error) {
	if n < 0 {
		return 0, fmt.Errorf("negative: %d", n)
	}
	return n * 10, nil
}

// multiInExpr — three q.Try calls in one arithmetic expression.
// Each binds to its own temp, each has its own bubble check,
// later ones short-circuit when an earlier one fails.
func multiInExpr(a, b, c string) (int, error) {
	return q.Try(strconv.Atoi(a)) * q.Try(strconv.Atoi(b)) / q.Try(strconv.Atoi(c)), nil
}

// hoistInNestedCall — q.* inside a nested call argument, not in
// a return-result position.
func hoistInNestedCall(s string) (int, error) {
	v := square(q.Try(strconv.Atoi(s)))
	return v, nil
}

func square(n int) int { return n * n }

// hoistAndReturn — combines the hoist (at LHS compute) with
// return-position q.*.
func hoistAndReturn(a, b string) (int, error) {
	base := square(q.Try(strconv.Atoi(a)))
	return base + q.Try(strconv.Atoi(b)), nil
}

func main() {
	show := func(name string, n int, err error) {
		if err != nil {
			fmt.Printf("%-35s => err: %v\n", name, err)
		} else {
			fmt.Printf("%-35s => %d\n", name, n)
		}
	}

	for _, s := range []string{"3", "bad"} {
		n, err := pipeline(s)
		show(fmt.Sprintf("pipeline(%q)", s), n, err)
	}

	// Both q.Try calls in pipeline's outer Foo — if Foo errors, the
	// outer q.Try catches Foo's error.
	n, err := pipeline("-1") // Atoi succeeds; Foo says "negative"
	show("pipeline(\"-1\")", n, err)

	for _, c := range [][3]string{
		{"2", "3", "5"},
		{"xx", "3", "5"},
		{"2", "yy", "5"},
		{"2", "3", "zz"},
	} {
		n, err := multiInExpr(c[0], c[1], c[2])
		show(fmt.Sprintf("multiInExpr(%q, %q, %q)", c[0], c[1], c[2]), n, err)
	}

	for _, s := range []string{"4", "bad"} {
		n, err := hoistInNestedCall(s)
		show(fmt.Sprintf("hoistInNestedCall(%q)", s), n, err)
	}

	for _, c := range [][2]string{{"3", "4"}, {"3", "bad"}, {"bad", "4"}} {
		n, err := hoistAndReturn(c[0], c[1])
		show(fmt.Sprintf("hoistAndReturn(%q, %q)", c[0], c[1]), n, err)
	}
}
