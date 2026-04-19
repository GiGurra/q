// Fixture: a main that uses bare q.Try in the smallest recognised
// shape (`v := q.Try(call())`). The preprocessor must rewrite the
// call into the inlined if-err form so the runtime path actually
// returns the propagated error instead of panicking with
// "call site was not rewritten".
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
	if n, err := parseAndDouble("21"); err != nil {
		fmt.Printf("ok-path: unexpected err %v\n", err)
	} else {
		fmt.Printf("ok-path: %d\n", n)
	}

	if n, err := parseAndDouble("abc"); err != nil {
		fmt.Printf("err-path: %s\n", err)
	} else {
		fmt.Printf("err-path: unexpected ok %d\n", n)
	}
}
