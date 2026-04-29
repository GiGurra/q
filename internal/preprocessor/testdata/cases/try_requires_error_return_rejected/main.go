// Negative fixture for docs/api/try.md's invariant:
//
//	"q.Try requires the enclosing function's last result to be `error`."
//
// The preprocessor unconditionally emits the bubble shape with the
// captured `error` in the final return slot. If the enclosing function
// has no error slot (or the final slot isn't `error`), Go's type-checker
// rejects the rewritten file. The diagnostic is downstream of the
// preprocessor, but the contract still holds: builds fail loudly.
//
// expected_build.txt asserts on the substring the type-checker emits
// today so a future change that loses or downgrades this guard is
// caught here.
package main

import (
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

func parseAndDouble(s string) int {
	n := q.Try(strconv.Atoi(s))
	return n * 2
}

func main() { _ = parseAndDouble("21") }
