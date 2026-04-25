// Negative fixture: bubble-shape q.* inside a container header is
// rejected. The rewrite would inject a multi-line bind+check that
// breaks the header parse.
package main

import (
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

func parse(s string) (int, error) {
	if v := q.Try(strconv.Atoi(s)); v > 0 {
		return v, nil
	}
	return 0, nil
}

func main() {
	_, _ = parse("1")
}
