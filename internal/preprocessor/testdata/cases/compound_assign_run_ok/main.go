// Fixture: compound-assign tokens (`+=`, `-=`, `*=`, `/=`, `%=`, etc.)
// with a q.* call on the RHS flow through the hoist path: each q.*
// binds to a temp + bubble, the original statement is re-emitted with
// the q.* span substituted by its `_qTmpN`, the compound op is
// preserved verbatim.
package main

import (
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

func plusEq(s string) (int, error) {
	total := 1
	total += q.Try(strconv.Atoi(s))
	return total, nil
}

func minusEq(s string) (int, error) {
	total := 100
	total -= q.Try(strconv.Atoi(s))
	return total, nil
}

func mulEq(s string) (int, error) {
	total := 2
	total *= q.Try(strconv.Atoi(s))
	return total, nil
}

func indexedPlusEq(s string) ([]int, error) {
	arr := []int{1}
	arr[0] += q.Try(strconv.Atoi(s))
	return arr, nil
}

func mapPlusEq(s string) (map[string]int, error) {
	m := map[string]int{"k": 5}
	m["k"] += q.Try(strconv.Atoi(s))
	return m, nil
}

// Multiple q.* in one compound assign — the hoist path generates one
// bind+bubble per call, then re-emits the statement.
func multiPlusEq(a, b string) (int, error) {
	total := 0
	total += q.Try(strconv.Atoi(a)) + q.Try(strconv.Atoi(b))
	return total, nil
}

func main() {
	run := func(label string, fn func() (int, error)) {
		n, err := fn()
		if err != nil {
			fmt.Printf("%s: err=%s\n", label, err)
			return
		}
		fmt.Printf("%s: %d\n", label, n)
	}

	run("plusEq(7)", func() (int, error) { return plusEq("7") })
	run("plusEq(bad)", func() (int, error) { return plusEq("bad") })
	run("minusEq(40)", func() (int, error) { return minusEq("40") })
	run("mulEq(3)", func() (int, error) { return mulEq("3") })

	if arr, err := indexedPlusEq("4"); err != nil {
		fmt.Printf("indexedPlusEq(4): err=%s\n", err)
	} else {
		fmt.Printf("indexedPlusEq(4): %v\n", arr)
	}
	if m, err := mapPlusEq("3"); err != nil {
		fmt.Printf("mapPlusEq(3): err=%s\n", err)
	} else {
		fmt.Printf("mapPlusEq(3): k=%d\n", m["k"])
	}

	run("multiPlusEq(2,3)", func() (int, error) { return multiPlusEq("2", "3") })
	run("multiPlusEq(bad,3)", func() (int, error) { return multiPlusEq("bad", "3") })
}
