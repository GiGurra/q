// Fixture: q.* used in return-position — `return q.Try(call()), nil`
// and siblings. The rewriter binds the q.* call to a _qTmp temp,
// emits the usual bubble block, and rebuilds the final return with
// the temp spliced in place of the q.* sub-expression.
package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

// Bare q.Try at return position (single result + error).
func parseDouble(s string) (int, error) {
	return q.Try(strconv.Atoi(s)) * 2, nil
}

// q.TryE chain at return position with Wrap.
func parseWrap(s string) (int, error) {
	return q.TryE(strconv.Atoi(s)).Wrap("parseWrap"), nil
}

// q.NotNil as the middle result of three — exercises preserving the
// surrounding expressions verbatim in the rebuilt return.
func pickMiddle(p *int) (string, *int, error) {
	return "tag", q.NotNil(p), nil
}

// q.NotNilE chain at return position with a constant replacement error.
var ErrMissing = errors.New("missing")

func getOrFail(p *int) (*int, error) {
	return q.NotNilE(p).Err(ErrMissing), nil
}

func report(name string, n int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
	} else {
		fmt.Printf("%s: ok=%d\n", name, n)
	}
}

func reportP(name string, tag string, p *int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
		return
	}
	if p == nil {
		fmt.Printf("%s: ok tag=%s nil\n", name, tag)
	} else {
		fmt.Printf("%s: ok tag=%s val=%d\n", name, tag, *p)
	}
}

func main() {
	n, err := parseDouble("21")
	report("parseDouble.ok", n, err)
	n, err = parseDouble("abc")
	report("parseDouble.bad", n, err)

	n, err = parseWrap("7")
	report("parseWrap.ok", n, err)
	n, err = parseWrap("xyz")
	report("parseWrap.bad", n, err)

	x := 9
	good := &x
	tag, p, err := pickMiddle(good)
	reportP("pickMiddle.ok", tag, p, err)
	tag, p, err = pickMiddle(nil)
	reportP("pickMiddle.bad", tag, p, err)

	got, err := getOrFail(good)
	if err != nil {
		fmt.Printf("getOrFail.ok: err=%s\n", err)
	} else {
		fmt.Printf("getOrFail.ok: %d\n", *got)
	}
	got, err = getOrFail(nil)
	if err != nil {
		fmt.Printf("getOrFail.bad: err=%s\n", err)
	} else {
		fmt.Printf("getOrFail.bad: unexpected ok %v\n", got)
	}
}
