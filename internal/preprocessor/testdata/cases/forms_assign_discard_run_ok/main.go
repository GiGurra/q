// Fixture: exercises the assign (`=`) and discard (no-LHS) forms for
// both q.Try and q.NotNil families, including a chain method to make
// sure the form support composes.
package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

var ErrPing = errors.New("ping")

// formAssign on q.Try: v already exists, the rewriter must use `=`
// and pre-declare the err var.
func tryAssign(s string) (int, error) {
	var n int
	n = q.Try(strconv.Atoi(s))
	return n, nil
}

// formDiscard on q.Try: bubble the err, drop the parsed int.
func tryDiscard(s string) error {
	q.Try(strconv.Atoi(s))
	return nil
}

// formAssign on q.TryE chain: combines reassignment with Wrapf-style
// wrapping.
func tryEAssignWrapf(s string) (int, error) {
	var n int
	n = q.TryE(strconv.Atoi(s)).Wrapf("assignWrap %q", s)
	return n, nil
}

// formDiscard on q.TryE chain: bubble through Err.
func tryEDiscardErr(s string) error {
	q.TryE(strconv.Atoi(s)).Err(ErrPing)
	return nil
}

// formAssign on q.NotNil: p already exists.
func notNilAssign(p *int) (int, error) {
	var pp *int
	pp = q.NotNil(p)
	return *pp, nil
}

// formDiscard on q.NotNil: precondition-style assertion that the
// pointer is non-nil; bubbles q.ErrNil if nil.
func notNilDiscard(p *int) error {
	q.NotNil(p)
	return nil
}

// formDiscard on q.NotNilE chain: bubble through Err.
func notNilEDiscardErr(p *int) error {
	q.NotNilE(p).Err(ErrPing)
	return nil
}

func report(name string, n int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
	} else {
		fmt.Printf("%s: ok=%d\n", name, n)
	}
}

func reportE(name string, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
	} else {
		fmt.Printf("%s: ok\n", name)
	}
}

func main() {
	const okInput, badInput = "11", "abc"
	x := 5
	good, bad := &x, (*int)(nil)

	n, err := tryAssign(okInput)
	report("tryAssign.ok", n, err)
	n, err = tryAssign(badInput)
	report("tryAssign.bad", n, err)

	reportE("tryDiscard.ok", tryDiscard(okInput))
	reportE("tryDiscard.bad", tryDiscard(badInput))

	n, err = tryEAssignWrapf(okInput)
	report("tryEAssignWrapf.ok", n, err)
	n, err = tryEAssignWrapf(badInput)
	report("tryEAssignWrapf.bad", n, err)

	reportE("tryEDiscardErr.ok", tryEDiscardErr(okInput))
	reportE("tryEDiscardErr.bad", tryEDiscardErr(badInput))

	n, err = notNilAssign(good)
	report("notNilAssign.ok", n, err)
	n, err = notNilAssign(bad)
	report("notNilAssign.bad", n, err)

	reportE("notNilDiscard.ok", notNilDiscard(good))
	reportE("notNilDiscard.bad", notNilDiscard(bad))

	reportE("notNilEDiscardErr.ok", notNilEDiscardErr(good))
	reportE("notNilEDiscardErr.bad", notNilEDiscardErr(bad))
}
