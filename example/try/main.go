// example/try mirrors docs/api/try.md one-to-one. Each section of the
// doc has a matching function below, named after the snippet it
// demonstrates. Run with:
//
//	go run -toolexec=q ./example/try
package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

// Sentinel referenced by docs/api/try.md "Chain methods on q.TryE".
var ErrBadInput = errors.New("bad input")

// toDBError is the function the doc references in `q.TryE(...).ErrF(toDBError)`.
func toDBError(e error) error { return fmt.Errorf("db: %w", e) }

// ---------- "What q.Try does" ----------
// Doc snippet:
//
//	n := q.Try(strconv.Atoi(s))
func whatQTryDoes(s string) (int, error) {
	n := q.Try(strconv.Atoi(s))
	return n, nil
}

// ---------- "Chain methods on q.TryE / Terminals" ----------
// Each function below holds exactly the doc snippet for that terminal.

func tryEErr(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).Err(ErrBadInput)
	return n, nil
}

func tryEErrF(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).ErrF(toDBError)
	return n, nil
}

func tryEWrap(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).Wrap("parsing")
	return n, nil
}

func tryEWrapf(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).Wrapf("parsing %q", s)
	return n, nil
}

func tryECatch(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).Catch(func(e error) (int, error) {
		if errors.Is(e, strconv.ErrSyntax) {
			return 0, nil
		}
		return 0, e
	})
	return n, nil
}

// ---------- "Recovers (chain-continuing)" ----------
// The doc's "Before" / "After" pair. Both must compile and behave
// identically on the doc's stated semantics.

// Before
func recoverBefore(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).Catch(func(e error) (int, error) {
		if errors.Is(e, strconv.ErrSyntax) {
			return 0, nil
		}
		return 0, fmt.Errorf("parsing %q: %w", s, e)
	})
	return n, nil
}

// After
func recoverAfter(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).
		RecoverIs(strconv.ErrSyntax, 0).
		Wrapf("parsing %q", s)
	return n, nil
}

// "Multiple Recover steps may be chained" — the doc's multi-recover example.
type MyErr struct{ Msg string }

func (e *MyErr) Error() string { return "my-err: " + e.Msg }

var (
	ErrA = errors.New("A")
	ErrB = errors.New("B")
)

func multiRecover() (int, error) {
	n := q.TryE(call()).
		RecoverIs(ErrA, 1).
		RecoverIs(ErrB, 2).
		RecoverAs((*MyErr)(nil), 3).
		Wrap("loading")
	return n, nil
}

// call drives multiRecover. Returns one of ErrA / ErrB / *MyErr / ok
// per a package-level dial so main can exercise each branch.
var callMode string

func call() (int, error) {
	switch callMode {
	case "A":
		return 0, ErrA
	case "B":
		return 0, ErrB
	case "MyErr":
		return 0, &MyErr{Msg: "boom"}
	case "ok":
		return 7, nil
	}
	return 0, errors.New("other")
}

// ---------- "Standalone runtime helpers" ----------
// `q.Const[T any](v T) func(error) (T, error)` — `.Catch(q.Const(0))`.
func constCatch(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).Catch(q.Const(0))
	return n, nil
}

// ---------- "Statement forms" ----------
// Each line in the doc's statement-forms block becomes its own function.

// v := q.Try(call())                                // define
func formDefine(s string) (int, error) {
	v := q.Try(strconv.Atoi(s))
	return v, nil
}

// v  = q.Try(call())                                // assign (incl. obj.field, arr[i])
// Demonstrated with arr[i] — the doc's parenthetical — to keep the
// LHS non-ident and clearly distinct from the define form above.
func formAssign(s string) (int, error) {
	var arr [1]int
	arr[0] = q.Try(strconv.Atoi(s))
	return arr[0], nil
}

// v += q.Try(call())                                // compound assign
// The hoist path preserves the user's compound op verbatim.
func formCompoundAssign(s string) (int, error) {
	v := 1
	v += q.Try(strconv.Atoi(s))
	return v, nil
}

//      q.Try(call())                                // discard (ExprStmt)
func formDiscard(s string) error {
	q.Try(strconv.Atoi(s))
	return nil
}

// return q.Try(call()), nil                         // return-position
func formReturn(s string) (int, error) {
	return q.Try(strconv.Atoi(s)), nil
}

// x := f(q.Try(call()))                             // nested in another expression (hoist)
func formHoist(s string) (int, error) {
	x := double(q.Try(strconv.Atoi(s)))
	return x, nil
}

func double(n int) int { return n * 2 }

// return q.Try(a()) * q.Try(b()) / q.Try(c()), nil  // multiple q.*s, short-circuit
func formMulti(a, b, c string) (int, error) {
	return q.Try(strconv.Atoi(a)) * q.Try(strconv.Atoi(b)) / q.Try(strconv.Atoi(c)), nil
}

// x := q.Try(Foo(q.Try(Bar())))                     // q.* nested inside another q.*
func formNested(s string) (int, error) {
	x := q.Try(foo(q.Try(strconv.Atoi(s))))
	return x, nil
}

func foo(n int) (int, error) {
	if n < 0 {
		return 0, fmt.Errorf("negative: %d", n)
	}
	return n * 10, nil
}

// ---------- "Closures and generics" ----------
// q.Try inside a func(...) { ... } uses the closure's own result list.
func closureBubble(s string) (int, error) {
	parse := func() (int, error) {
		return q.Try(strconv.Atoi(s)), nil
	}
	return parse()
}

// q.Try inside a generic function — *new(T) is universal.
func parseT[T any](parse func(string) (T, error), s string) (T, error) {
	v := q.Try(parse(s))
	return v, nil
}

func main() {
	report := func(label string, n int, err error) {
		if err != nil {
			fmt.Printf("%s: err=%s\n", label, err)
			return
		}
		fmt.Printf("%s: ok=%d\n", label, n)
	}

	// "What q.Try does"
	n, err := whatQTryDoes("21")
	report("whatQTryDoes(21)", n, err)
	n, err = whatQTryDoes("abc")
	report("whatQTryDoes(abc)", n, err)

	// Terminals
	n, err = tryEErr("abc")
	report("tryEErr(abc)", n, err)
	fmt.Printf("tryEErr.is(ErrBadInput): %v\n", errors.Is(err, ErrBadInput))
	n, err = tryEErrF("abc")
	report("tryEErrF(abc)", n, err)
	n, err = tryEWrap("abc")
	report("tryEWrap(abc)", n, err)
	n, err = tryEWrapf("abc")
	report("tryEWrapf(abc)", n, err)
	n, err = tryECatch("abc")
	report("tryECatch(abc-syntax-recovers)", n, err)

	// Recover Before/After
	n, err = recoverBefore("abc")
	report("recoverBefore(abc)", n, err)
	n, err = recoverAfter("abc")
	report("recoverAfter(abc)", n, err)

	// Multi-recover
	for _, mode := range []string{"A", "B", "MyErr", "other", "ok"} {
		callMode = mode
		n, err = multiRecover()
		report("multiRecover."+mode, n, err)
	}

	// Const helper
	n, err = constCatch("abc")
	report("constCatch(abc)", n, err)

	// Statement forms
	n, err = formDefine("21")
	report("formDefine(21)", n, err)
	n, err = formAssign("21")
	report("formAssign(21)", n, err)
	n, err = formCompoundAssign("21")
	report("formCompoundAssign(21)", n, err)
	n, err = formCompoundAssign("bad")
	report("formCompoundAssign(bad)", n, err)
	if err := formDiscard("21"); err != nil {
		fmt.Printf("formDiscard(21): err=%s\n", err)
	} else {
		fmt.Println("formDiscard(21): ok")
	}
	if err := formDiscard("abc"); err != nil {
		fmt.Printf("formDiscard(abc): err=%s\n", err)
	}
	n, err = formReturn("21")
	report("formReturn(21)", n, err)
	n, err = formHoist("21")
	report("formHoist(21)", n, err)
	n, err = formMulti("2", "3", "5")
	report("formMulti(2,3,5)", n, err)
	n, err = formMulti("2", "bad", "5")
	report("formMulti(2,bad,5)", n, err)
	n, err = formNested("3")
	report("formNested(3)", n, err)
	n, err = formNested("-1")
	report("formNested(-1)", n, err)

	// Closures and generics
	n, err = closureBubble("21")
	report("closureBubble(21)", n, err)
	n, err = closureBubble("abc")
	report("closureBubble(abc)", n, err)
	n, err = parseT(strconv.Atoi, "42")
	report("parseT(Atoi,42)", n, err)
	n, err = parseT(strconv.Atoi, "bad")
	report("parseT(Atoi,bad)", n, err)
}
