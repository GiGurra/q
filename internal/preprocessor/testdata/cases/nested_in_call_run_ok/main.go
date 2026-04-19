// Fixture: q.* nested inside a non-return expression. Every q.* call
// found anywhere in a statement's expressions is hoisted to a
// preceding bind + bubble check, then the statement is re-emitted
// with q.* spans substituted by their _qTmpN temps.
package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

// q.Try nested inside an RHS call expression. v := f(q.Try(...)).
func nestedInCallRHS(s string) (int, error) {
	v := double(q.Try(strconv.Atoi(s)))
	return v, nil
}

// Multi-LHS with a nested q.*. Direct-bind can't handle this (single
// q.* producing multiple LHS would need Try2/Try3); hoist does.
func multiLHS(s string) (int, string, error) {
	a, b := split(q.Try(strconv.Atoi(s)))
	return a, b, nil
}

// q.* as an argument inside an expression-statement call (no LHS).
func exprStmtNested(s string) error {
	sink(q.Try(strconv.Atoi(s)))
	return nil
}

// Multiple q.*s in one statement: two Try args and one NotNil deref,
// combined in an arithmetic expression.
func multipleInOne(a, b string, p *int) (int, error) {
	v := sum(q.Try(strconv.Atoi(a)), q.Try(strconv.Atoi(b))) + *q.NotNil(p)
	return v, nil
}

// q.* inside a non-ident assign LHS target: m[key] = q.Try(...).
// Direct-bind handles this (LHS is a non-nested expression without
// q.*) — included here to confirm hoist path doesn't regress it.
func mapAssign(m map[string]int, key, s string) (int, error) {
	m[key] = q.Try(strconv.Atoi(s))
	return m[key], nil
}

// q.* nested inside the LHS index expression. The LHS contains a
// q.*, so direct-bind can't apply; hoist must pull it out before
// emitting the assignment.
func indexLHS(m map[int]int, s string, v int) (int, error) {
	m[q.Try(strconv.Atoi(s))] = v
	return m[0], nil
}

// q.* as an argument to a chain method (MethodArgs). Hoist is
// expected to treat the inner q.* as a separate sub-call.
func chainedArg(a, b string) (int, error) {
	v := double(q.TryE(strconv.Atoi(a)).Wrap(fmt.Sprintf("parsing %q", b)))
	return v, nil
}

// q.* nested directly inside another q.*'s InnerExpr: the outer
// takes (T, error); its argument is a function call whose arg is
// itself a (T, error) producing q.Try. The rewriter must render the
// inner first and feed its temp into the outer's bind.
func qInsideQ(s string) (int, error) {
	x := q.Try(addOne(q.Try(strconv.Atoi(s))))
	return x, nil
}

func addOne(n int) (int, error) { return n + 1, nil }

var ErrMissing = errors.New("missing")

func double(n int) int       { return n * 2 }
func split(n int) (int, string) {
	if n < 0 {
		return 0, "neg"
	}
	return n, "pos"
}
func sink(n int)             { _ = n }
func sum(x, y int) int       { return x + y }

func report(name string, n int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
	} else {
		fmt.Printf("%s: ok=%d\n", name, n)
	}
}

func reportS(name string, n int, s string, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
	} else {
		fmt.Printf("%s: ok=%d,%s\n", name, n, s)
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
	n, err := nestedInCallRHS("7")
	report("nestedInCallRHS.ok", n, err)
	n, err = nestedInCallRHS("xx")
	report("nestedInCallRHS.bad", n, err)

	a, b, err := multiLHS("3")
	reportS("multiLHS.ok", a, b, err)
	a, b, err = multiLHS("yy")
	reportS("multiLHS.bad", a, b, err)

	reportE("exprStmtNested.ok", exprStmtNested("9"))
	reportE("exprStmtNested.bad", exprStmtNested("zz"))

	x := 5
	good := &x
	n, err = multipleInOne("1", "2", good)
	report("multipleInOne.ok", n, err)
	n, err = multipleInOne("qq", "2", good)
	report("multipleInOne.badA", n, err)
	n, err = multipleInOne("1", "2", nil)
	report("multipleInOne.nilPtr", n, err)

	m := map[string]int{}
	n, err = mapAssign(m, "k", "11")
	report("mapAssign.ok", n, err)
	n, err = mapAssign(m, "k", "xx")
	report("mapAssign.bad", n, err)

	im := map[int]int{}
	n, err = indexLHS(im, "0", 42)
	report("indexLHS.ok", n, err)
	n, err = indexLHS(im, "zz", 42)
	report("indexLHS.bad", n, err)

	n, err = chainedArg("6", "label")
	report("chainedArg.ok", n, err)
	n, err = chainedArg("xx", "label")
	report("chainedArg.bad", n, err)

	n, err = qInsideQ("7")
	report("qInsideQ.ok", n, err)
	n, err = qInsideQ("zz")
	report("qInsideQ.bad", n, err)
}
