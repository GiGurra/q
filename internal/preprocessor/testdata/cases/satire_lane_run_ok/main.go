// Fixture: satire-lane features — q.Async + q.Await (+ AwaitE),
// plus q.TryCatch. Async is a plain runtime function; Await and
// AwaitE are rewritten like Try / TryE with q.AwaitRaw as the
// source. TryCatch is an IIFE with defer-recover (Java-style
// try/catch demo).
package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

var ErrSoft = errors.New("soft")

// fetch returns (v, err) with a tiny simulated delay.
func fetch(v int, err error) func() (int, error) {
	return func() (int, error) {
		time.Sleep(5 * time.Millisecond)
		return v, err
	}
}

// awaitBare exercises bare q.Await — bubble on err.
func awaitBare(f q.Future[int]) (int, error) {
	return q.Await(f), nil
}

// awaitWrap exercises q.AwaitE.Wrap — chain-shaped bubble.
func awaitWrap(f q.Future[int]) (int, error) {
	return q.AwaitE(f).Wrap("fetching"), nil
}

// awaitCatch exercises q.AwaitE.Catch — recover from soft errors.
func awaitCatch(f q.Future[int]) (int, error) {
	return q.AwaitE(f).Catch(func(e error) (int, error) {
		if errors.Is(e, ErrSoft) {
			return 99, nil
		}
		return 0, e
	}), nil
}

// tryCatchSample exercises q.TryCatch — run risky body, recover
// panic via handler.
func tryCatchSample(msg string) {
	q.TryCatch(func() {
		if msg == "panic" {
			panic("boom")
		}
		fmt.Printf("try.ran: %s\n", msg)
	}).Catch(func(r any) {
		fmt.Printf("catch.caught: %v\n", r)
	})
}

func report(name string, n int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
	} else {
		fmt.Printf("%s: ok=%d\n", name, n)
	}
}

func main() {
	// q.Async + q.Await happy path.
	okFuture := q.Async(fetch(42, nil))
	n, err := awaitBare(okFuture)
	report("await.ok", n, err)

	// q.Await bubble — Future carries an error.
	errFuture := q.Async(fetch(0, errors.New("boom")))
	n, err = awaitBare(errFuture)
	report("await.err", n, err)

	// q.AwaitE.Wrap — shape the bubble.
	wrapFuture := q.Async(fetch(0, errors.New("boom")))
	n, err = awaitWrap(wrapFuture)
	report("awaitWrap.err", n, err)

	// q.AwaitE.Catch — recover from soft.
	softFuture := q.Async(fetch(0, ErrSoft))
	n, err = awaitCatch(softFuture)
	report("awaitCatch.soft", n, err)

	// q.AwaitE.Catch — bubble hard.
	hardFuture := q.Async(fetch(0, errors.New("hard")))
	n, err = awaitCatch(hardFuture)
	report("awaitCatch.hard", n, err)

	// q.TryCatch happy path.
	tryCatchSample("no-panic")

	// q.TryCatch panic path.
	tryCatchSample("panic")
}
