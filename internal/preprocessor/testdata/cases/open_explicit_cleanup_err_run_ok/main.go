// Fixture: q.Open(...).DeferCleanup(cleanup) with cleanup of shape
// `func(T) error`. The preprocessor's typecheck pass inspects the
// cleanup arg's type and dispatches to a defer that logs any
// close-time error via slog.Error rather than discarding it. The
// same dispatch covers passing a method value of a type whose Close
// returns error (e.g. `(*os.File).Close`). Pair fixture for
// open_run_ok which exercises the void-cleanup form.
//
// To keep the assertion deterministic, this fixture configures slog
// at init() to write to stdout with the time attribute stripped, so
// the slog-emitted line interleaves predictably with fmt.Printf
// output. The slog-fires path is exercised by failingCloseCleanup;
// the silent path by methodValueCleanup (Close returns nil).
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/GiGurra/q/pkg/q"
)

func init() {
	h := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	slog.SetDefault(slog.New(h))
}

type Closer struct {
	id        int
	failClose bool
}

func (c *Closer) Close() error {
	closeLog = append(closeLog, c.id)
	if c.failClose {
		return errors.New("close failed")
	}
	return nil
}

var closeLog []int

func dial(id int, failDial, failClose bool) (*Closer, error) {
	if failDial {
		return nil, fmt.Errorf("dial failed for %d", id)
	}
	return &Closer{id: id, failClose: failClose}, nil
}

// methodValueCleanup — `func(*Closer) error` via method value. Close
// returns nil here so the slog branch does NOT fire; the test asserts
// the rewriter still emits a buildable defer for the err-returning
// shape.
func methodValueCleanup(id int, failDial bool) error {
	c := q.Open(dial(id, failDial, false)).DeferCleanup((*Closer).Close)
	_ = c
	return nil
}

// failingCloseCleanup — same shape, but Close returns a non-nil error,
// so the rewritten defer must invoke slog.Error.
func failingCloseCleanup(id int) error {
	c := q.Open(dial(id, false, true)).DeferCleanup((*Closer).Close)
	_ = c
	return nil
}

// explicitFnCleanup — `func(*Closer) error` supplied as a literal
// closure rather than a method value.
func explicitFnCleanup(id int, failDial bool) error {
	c := q.Open(dial(id, failDial, false)).DeferCleanup(func(c *Closer) error {
		closeLog = append(closeLog, c.id*1000)
		return nil
	})
	_ = c
	return nil
}

func main() {
	report := func(label string, err error) {
		closes := closeLog
		closeLog = nil
		if err != nil {
			fmt.Printf("%s: err=%v closes=%v\n", label, err, closes)
			return
		}
		fmt.Printf("%s: ok closes=%v\n", label, closes)
	}

	report("methodValueCleanup(1, ok)", methodValueCleanup(1, false))
	report("methodValueCleanup(1, fail)", methodValueCleanup(1, true))

	// failingCloseCleanup: Close returns a non-nil error, so the
	// rewritten defer should slog.Error. The slog line lands on stdout
	// (configured in init) and appears in expected_run.txt.
	report("failingCloseCleanup(7) [err logged↓]", failingCloseCleanup(7))

	report("explicitFnCleanup(2, ok)", explicitFnCleanup(2, false))
	report("explicitFnCleanup(2, fail)", explicitFnCleanup(2, true))
}
