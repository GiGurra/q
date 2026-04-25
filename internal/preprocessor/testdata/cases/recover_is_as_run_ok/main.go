// Fixture: q.TryE(...).RecoverIs / RecoverAs intermediates,
// terminated by every existing chain method (Wrap/Wrapf/Err/ErrF/Catch).
// Covers single-step and multi-step recovery, plus the
// "non-matching error bubbles" path through the terminal.
package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

var (
	ErrCustom1 = errors.New("custom-1")
	ErrCustom2 = errors.New("custom-2")
)

// failOf returns an error of the requested shape so each test path
// exercises a different match.
func failOf(kind string) (int, error) {
	switch kind {
	case "syntax":
		// Real strconv.NumError wrapping strconv.ErrSyntax.
		_, err := strconv.Atoi("abc")
		return 0, err
	case "custom1":
		return 0, ErrCustom1
	case "custom2":
		return 0, ErrCustom2
	case "ok":
		return 7, nil
	}
	return 0, errors.New("unknown")
}

// recoverIsWrapf — the user's target one-liner: recover ErrSyntax to
// 0, wrap anything else with a format.
func recoverIsWrapf(kind string) (int, error) {
	n := q.TryE(failOf(kind)).
		RecoverIs(strconv.ErrSyntax, 0).
		Wrapf("parsing %q", kind)
	return n, nil
}

// recoverAsErr — RecoverAs on a typed error, with terminal Err
// replacing the bubble for non-matches.
func recoverAsErr(kind string) (int, error) {
	n := q.TryE(failOf(kind)).
		RecoverAs((*strconv.NumError)(nil), -1).
		Err(ErrCustom2)
	return n, nil
}

// multiRecover — chain two RecoverIs steps in source order, then a
// terminal Wrap. Each step runs only if the previous didn't recover.
func multiRecover(kind string) (int, error) {
	n := q.TryE(failOf(kind)).
		RecoverIs(ErrCustom1, 100).
		RecoverIs(ErrCustom2, 200).
		Wrap("multi")
	return n, nil
}

// mixedIsAs — RecoverIs first, RecoverAs second, terminal Err.
func mixedIsAs(kind string) (int, error) {
	n := q.TryE(failOf(kind)).
		RecoverIs(ErrCustom1, 11).
		RecoverAs((*strconv.NumError)(nil), 22).
		Err(ErrCustom2)
	return n, nil
}

// recoverIsCatch — RecoverIs with terminal Catch (transform).
func recoverIsCatch(kind string) (int, error) {
	n := q.TryE(failOf(kind)).
		RecoverIs(ErrCustom1, 33).
		Catch(func(e error) (int, error) {
			return 0, fmt.Errorf("caught: %w", e)
		})
	return n, nil
}

// happyPath — call returns ok, recovers don't fire, terminal doesn't
// fire, value passes through.
func happyPath() (int, error) {
	n := q.TryE(failOf("ok")).
		RecoverIs(ErrCustom1, 99).
		Wrapf("never wrapped")
	return n, nil
}

func report(label string, n int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", label, err)
		return
	}
	fmt.Printf("%s: ok=%d\n", label, n)
}

func run(label string, fn func() (int, error)) {
	n, err := fn()
	report(label, n, err)
}

func main() {
	run("recoverIsWrapf.syntax", func() (int, error) { return recoverIsWrapf("syntax") })
	run("recoverIsWrapf.custom1", func() (int, error) { return recoverIsWrapf("custom1") })

	run("recoverAsErr.syntax", func() (int, error) { return recoverAsErr("syntax") })
	run("recoverAsErr.custom1", func() (int, error) { return recoverAsErr("custom1") })

	run("multiRecover.custom1", func() (int, error) { return multiRecover("custom1") })
	run("multiRecover.custom2", func() (int, error) { return multiRecover("custom2") })
	run("multiRecover.syntax", func() (int, error) { return multiRecover("syntax") })

	run("mixedIsAs.custom1", func() (int, error) { return mixedIsAs("custom1") })
	run("mixedIsAs.syntax", func() (int, error) { return mixedIsAs("syntax") })
	run("mixedIsAs.custom2", func() (int, error) { return mixedIsAs("custom2") })

	run("recoverIsCatch.custom1", func() (int, error) { return recoverIsCatch("custom1") })
	run("recoverIsCatch.custom2", func() (int, error) { return recoverIsCatch("custom2") })

	run("happyPath", happyPath)

	// Sentinel identity preserved through Wrapf for non-recovered errors.
	_, err := recoverIsWrapf("custom1")
	fmt.Printf("recoverIsWrapf.custom1.is: %v\n", errors.Is(err, ErrCustom1))
}
