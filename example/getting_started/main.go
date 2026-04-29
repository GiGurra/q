// example/getting_started mirrors docs/getting-started.md's "First
// passing build" snippet. Run with:
//
//	go run -toolexec=q ./example/getting_started
package main

import (
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

func parseAndDouble(s string) (int, error) {
	n := q.Try(strconv.Atoi(s))
	return n * 2, nil
}

func main() {
	n, err := parseAndDouble("21")
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println(n) // 42

	// Failing path — Atoi error bubbles through q.Try.
	if _, err := parseAndDouble("oops"); err != nil {
		fmt.Println("parseAndDouble(oops): err:", err)
	}
}
