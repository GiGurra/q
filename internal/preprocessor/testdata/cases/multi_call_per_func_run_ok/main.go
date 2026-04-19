// Fixture: multiple q.* calls in one function body. Verifies the
// rewriter's per-call counter (_qErr1, _qErr2, …) keeps the rewritten
// var names distinct, that earlier bubbles short-circuit before later
// calls run, and that mixed Try / TryE / NotNil shapes compose in
// sequence.
package main

import (
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

// pipeline runs three q.* operations in a row and returns the final
// product. Each call has its own bubble target; the second and third
// only run if the previous succeeded.
func pipeline(a, b string, table map[string]*int) (int, error) {
	x := q.Try(strconv.Atoi(a))
	y := q.TryE(strconv.Atoi(b)).Wrapf("parsing %q", b)
	p := q.NotNil(table["k"])
	return x + y + *p, nil
}

// firstBubbleWins exercises the short-circuit: when the first q call
// fails, the second's call expression must not be evaluated. We use a
// counter to detect any unexpected execution.
var atoiCalls int

func countingAtoi(s string) (int, error) {
	atoiCalls++
	return strconv.Atoi(s)
}

func firstBubbleWins(a, b string) (int, error) {
	x := q.Try(countingAtoi(a))
	y := q.Try(countingAtoi(b))
	return x + y, nil
}

func main() {
	v := 7
	good := map[string]*int{"k": &v}
	bad := map[string]*int{}

	n, err := pipeline("1", "2", good)
	if err != nil {
		fmt.Printf("pipeline.ok: unexpected err %s\n", err)
	} else {
		fmt.Printf("pipeline.ok: %d\n", n)
	}

	_, err = pipeline("1", "abc", good)
	fmt.Printf("pipeline.midbad: %s\n", err)

	_, err = pipeline("1", "2", bad)
	fmt.Printf("pipeline.lastbad: %s\n", err)

	atoiCalls = 0
	_, err = firstBubbleWins("abc", "5")
	fmt.Printf("shortcircuit: err=%s atoiCalls=%d\n", err, atoiCalls)
}
