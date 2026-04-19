// Fixture: q.Check / q.CheckE — error-only bubble helpers. q.Check is
// always an ExprStmt (returns nothing) so it doesn't appear in
// formDefine/formAssign/formReturn/formHoist. This fixture mirrors
// the Try-family error-path coverage: every entry + chain method on
// both the success and failure paths.
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

var (
	ErrPing     = errors.New("ping-failed")
	ErrReplaced = errors.New("replaced")
)

// Returns an error from closeIt(); q.Check bubbles it.
func bareCheck(fail bool) error {
	q.Check(closeIt(fail))
	return nil
}

// CheckE.Err replaces the bubbled error with a constant.
func checkErr(fail bool) error {
	q.CheckE(closeIt(fail)).Err(ErrReplaced)
	return nil
}

// CheckE.ErrF transforms the captured error.
func checkErrF(fail bool) error {
	q.CheckE(closeIt(fail)).ErrF(func(e error) error { return fmt.Errorf("wrapped: %w", e) })
	return nil
}

// CheckE.Wrap prefixes the message with %w-format.
func checkWrap(fail bool) error {
	q.CheckE(closeIt(fail)).Wrap("closing")
	return nil
}

// CheckE.Wrapf builds the message with format args.
func checkWrapf(fail bool, id int) error {
	q.CheckE(closeIt(fail)).Wrapf("closing %d", id)
	return nil
}

// CheckE.Catch suppresses an error when the callback returns nil.
// Otherwise the callback's non-nil result is bubbled.
func checkCatchSuppress(fail bool) error {
	q.CheckE(closeIt(fail)).Catch(func(e error) error { return nil })
	return nil
}

func checkCatchBubble(fail bool) error {
	q.CheckE(closeIt(fail)).Catch(func(e error) error { return fmt.Errorf("caught: %w", e) })
	return nil
}

// q.Check is used in the `(T, error)` function shape too — bubble
// produces a zero T alongside the error.
func checkInValueFn(fail bool) (int, error) {
	q.Check(closeIt(fail))
	return 42, nil
}

func closeIt(fail bool) error {
	if fail {
		return ErrPing
	}
	return nil
}

func reportE(name string, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
	} else {
		fmt.Printf("%s: ok\n", name)
	}
}

func reportV(name string, n int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
	} else {
		fmt.Printf("%s: ok=%d\n", name, n)
	}
}

func main() {
	reportE("bareCheck.ok", bareCheck(false))
	reportE("bareCheck.bad", bareCheck(true))

	reportE("checkErr.ok", checkErr(false))
	reportE("checkErr.bad", checkErr(true))

	reportE("checkErrF.ok", checkErrF(false))
	reportE("checkErrF.bad", checkErrF(true))

	reportE("checkWrap.ok", checkWrap(false))
	reportE("checkWrap.bad", checkWrap(true))

	reportE("checkWrapf.ok", checkWrapf(false, 7))
	reportE("checkWrapf.bad", checkWrapf(true, 7))

	reportE("checkCatchSuppress.ok", checkCatchSuppress(false))
	reportE("checkCatchSuppress.bad", checkCatchSuppress(true))

	reportE("checkCatchBubble.ok", checkCatchBubble(false))
	reportE("checkCatchBubble.bad", checkCatchBubble(true))

	n, err := checkInValueFn(false)
	reportV("checkInValueFn.ok", n, err)
	n, err = checkInValueFn(true)
	reportV("checkInValueFn.bad", n, err)
}
