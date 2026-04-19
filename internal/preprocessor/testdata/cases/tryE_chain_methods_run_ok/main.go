// Fixture: exercises every q.TryE chain method end-to-end. Each
// helper function uses one method on the same parseInt failure to
// keep stdout assertion legible. The fixture asserts on both branches
// (success and bubble) per method via expected_run.txt.
package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

var ErrCustom = errors.New("custom error")

func errMethod(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).Err(ErrCustom)
	return n, nil
}

func errFMethod(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).ErrF(func(e error) error {
		return fmt.Errorf("transformed: %w", e)
	})
	return n, nil
}

func wrapMethod(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).Wrap("wrap-context")
	return n, nil
}

func wrapfMethod(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).Wrapf("wrapf-context %q", s)
	return n, nil
}

func catchRecover(s string) (int, error) {
	// Recovery branch: on err, return (99, nil). The bubble does NOT
	// fire — the function returns 99 normally.
	n := q.TryE(strconv.Atoi(s)).Catch(func(e error) (int, error) {
		return 99, nil
	})
	return n, nil
}

func catchTransform(s string) (int, error) {
	// Transform branch: on err, return (zero, newErr). The bubble fires
	// with the transformed error.
	n := q.TryE(strconv.Atoi(s)).Catch(func(e error) (int, error) {
		return 0, fmt.Errorf("catch-transformed: %w", e)
	})
	return n, nil
}

func report(name string, n int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
	} else {
		fmt.Printf("%s: ok=%d\n", name, n)
	}
}

func main() {
	const okInput, badInput = "42", "abc"

	n, err := errMethod(okInput)
	report("Err.ok", n, err)
	n, err = errMethod(badInput)
	report("Err.bad", n, err)

	n, err = errFMethod(okInput)
	report("ErrF.ok", n, err)
	n, err = errFMethod(badInput)
	report("ErrF.bad", n, err)

	n, err = wrapMethod(okInput)
	report("Wrap.ok", n, err)
	n, err = wrapMethod(badInput)
	report("Wrap.bad", n, err)

	n, err = wrapfMethod(okInput)
	report("Wrapf.ok", n, err)
	n, err = wrapfMethod(badInput)
	report("Wrapf.bad", n, err)

	n, err = catchRecover(okInput)
	report("CatchRec.ok", n, err)
	n, err = catchRecover(badInput)
	report("CatchRec.bad", n, err)

	n, err = catchTransform(okInput)
	report("CatchXf.ok", n, err)
	n, err = catchTransform(badInput)
	report("CatchXf.bad", n, err)
}
