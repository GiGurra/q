// Fixture: a main package that imports pkg/q and calls q.Try in a
// throw-away function. With -toolexec=q the preprocessor injects the
// _q_atCompileTime stub when compiling pkg/q so this links cleanly.
// expected_build.txt is absent => the harness asserts the build
// succeeds.
package main

import (
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

func parseAndDouble(s string) (int, error) {
	n := q.Try(strconv.Atoi(s))
	return n * 2, nil
}

func main() {
	_, _ = parseAndDouble("21")
}
