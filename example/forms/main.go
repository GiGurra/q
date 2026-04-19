// example/forms — q.Try in every supported statement form:
// define, assign, discard, return-position, and hoist (nested
// inside another expression). Run with:
//
//	go run -toolexec=q ./example/forms
package main

import (
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

// define: `v := q.Try(...)` on a fresh identifier.
func define(s string) (int, error) {
	v := q.Try(strconv.Atoi(s))
	return v * 2, nil
}

// assignToIndex: `arr[i] = q.Try(...)` on a non-ident LHS. Assign
// form is matched whenever the RHS is a direct q.* and the LHS has
// no nested q.*.
func assignToIndex(inputs []string) ([]int, error) {
	result := make([]int, len(inputs))
	for i, s := range inputs {
		result[i] = q.Try(strconv.Atoi(s))
	}
	return result, nil
}

// assignToField: `obj.field = q.Try(...)`. Another non-ident LHS.
type holder struct{ n int }

func assignToField(s string, h *holder) error {
	h.n = q.Try(strconv.Atoi(s))
	return nil
}

// discard: `q.Try(...)` as an expression statement. Bubble fires,
// value is dropped.
func discard(s string) error {
	q.Try(strconv.Atoi(s))
	return nil
}

// returnPosition: q.* anywhere inside a return-result expression.
func returnPosition(s string) (int, error) {
	return q.Try(strconv.Atoi(s)) * 2, nil
}

// hoist: q.* nested inside a call. The rewriter hoists the q.*
// into a preceding bind + check, substitutes the result back in.
func hoist(s string) (int, error) {
	result := double(q.Try(strconv.Atoi(s)))
	return result, nil
}

// hoistInLHS: q.* in the LHS index expression.
func hoistInLHS(s string, vals map[int]int) (int, error) {
	vals[q.Try(strconv.Atoi(s))] = 42
	return vals[0], nil
}

func double(n int) int { return n * 2 }

func main() {
	show := func(name string, fn func() (int, error)) {
		n, err := fn()
		if err != nil {
			fmt.Printf("%-20s => err: %v\n", name, err)
		} else {
			fmt.Printf("%-20s => %d\n", name, n)
		}
	}

	show("define(\"7\")", func() (int, error) { return define("7") })
	show("define(\"bad\")", func() (int, error) { return define("bad") })

	arr, err := assignToIndex([]string{"3", "5", "7"})
	if err != nil {
		fmt.Printf("%-20s => err: %v\n", "assignToIndex(ok)", err)
	} else {
		fmt.Printf("%-20s => %v\n", "assignToIndex(ok)", arr)
	}
	arr, err = assignToIndex([]string{"3", "bad", "7"})
	if err != nil {
		fmt.Printf("%-20s => err: %v\n", "assignToIndex(bad)", err)
	} else {
		fmt.Printf("%-20s => %v\n", "assignToIndex(bad)", arr)
	}

	h := &holder{}
	err = assignToField("21", h)
	fmt.Printf("%-20s => h.n=%d err=%v\n", "assignToField(\"21\")", h.n, err)
	err = assignToField("bad", h)
	fmt.Printf("%-20s => h.n=%d err=%v\n", "assignToField(\"bad\")", h.n, err)

	if err := discard("0"); err != nil {
		fmt.Printf("%-20s => err: %v\n", "discard(\"0\")", err)
	} else {
		fmt.Printf("%-20s => ok\n", "discard(\"0\")")
	}
	if err := discard("bad"); err != nil {
		fmt.Printf("%-20s => err: %v\n", "discard(\"bad\")", err)
	}

	show("returnPosition(\"5\")", func() (int, error) { return returnPosition("5") })
	show("returnPosition(\"bad\")", func() (int, error) { return returnPosition("bad") })

	show("hoist(\"9\")", func() (int, error) { return hoist("9") })
	show("hoist(\"bad\")", func() (int, error) { return hoist("bad") })

	vals := map[int]int{}
	show("hoistInLHS(\"3\")", func() (int, error) { return hoistInLHS("3", vals) })
	show("hoistInLHS(\"bad\")", func() (int, error) { return hoistInLHS("bad", vals) })
}

// Note: `discard` returns error, not (int, error), so we can't use
// the show helper for it — q.Try needs the enclosing function's
// last return to be `error`, which works whether or not earlier
// results are present.
