// Fixture: every bubble family works in both enclosing-function
// return shapes — `func() error` and `func() (T, error)`. The
// rewriter's commonRenderInputs pulls zeros from the full Results
// list and substitutes the error expression in the last slot, so
// both shapes bubble correctly with no family-specific handling.
//
// One helper per family per signature. The fixture is deliberately
// shallow — only bare forms and only the bubble path — because the
// per-family fixtures already lock in happy-path semantics. This
// file is the regression guard for the invariant "all bubbles
// support both signatures".
package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

// ---- q.Try ----

func tryOnlyErr(s string) error {
	q.Try(strconv.Atoi(s))
	return nil
}

func tryPair(s string) (int, error) {
	return q.Try(strconv.Atoi(s)) * 2, nil
}

// ---- q.NotNil ----

func notNilOnlyErr(p *int) error {
	q.NotNil(p)
	return nil
}

func notNilPair(p *int) (int, error) {
	return *q.NotNil(p), nil
}

// ---- q.Ok ----

func lookup(m map[string]int, k string) (int, bool) { v, ok := m[k]; return v, ok }

func okOnlyErr(m map[string]int, k string) error {
	q.Ok(lookup(m, k))
	return nil
}

func okPair(m map[string]int, k string) (int, error) {
	return q.Ok(lookup(m, k)), nil
}

// ---- q.Recv ----

func recvOnlyErr(ch <-chan int) error {
	q.Recv(ch)
	return nil
}

func recvPair(ch <-chan int) (int, error) {
	return q.Recv(ch), nil
}

// ---- q.As ----

func asOnlyErr(x any) error {
	q.As[int](x)
	return nil
}

func asPair(x any) (int, error) {
	return q.As[int](x), nil
}

// ---- q.Check ----

func checkOnlyErr(e error) error {
	q.Check(e)
	return nil
}

func checkPair(e error) (int, error) {
	q.Check(e)
	return 42, nil
}

// ---- q.Open ----

type Conn struct{ id int }

func (*Conn) Close() {}

func dial(id int, fail bool) (*Conn, error) {
	if fail {
		return nil, errors.New("dial-failed")
	}
	return &Conn{id: id}, nil
}

func openOnlyErr(fail bool) error {
	c := q.Open(dial(1, fail)).Release((*Conn).Close)
	_ = c
	return nil
}

func openPair(fail bool) (*Conn, error) {
	return q.Open(dial(2, fail)).Release((*Conn).Close), nil
}

// ---- q.Trace ----

// strip everything after the first ':' in "basename:N:rest" so the
// expected output stays stable across source edits to this file.
func stripLine(err error) string {
	if err == nil {
		return "<nil>"
	}
	s := err.Error()
	const marker = "main.go:"
	i := 0
	for ; i+len(marker) <= len(s); i++ {
		if s[i:i+len(marker)] == marker {
			break
		}
	}
	if i+len(marker) > len(s) {
		return s
	}
	j := i + len(marker)
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	return s[:i+len(marker)] + "N" + s[j:]
}

func traceOnlyErr(s string) error {
	q.Trace(strconv.Atoi(s))
	return nil
}

func tracePair(s string) (int, error) {
	return q.Trace(strconv.Atoi(s)), nil
}

// ---- q.Await ----

func awaitOnlyErr() error {
	f := q.Async(func() (int, error) { return 0, errors.New("await-bad") })
	q.Await(f)
	return nil
}

func awaitPair() (int, error) {
	f := q.Async(func() (int, error) { return 0, errors.New("await-bad") })
	return q.Await(f), nil
}

func main() {
	report := func(name string, err error) {
		if err == nil {
			fmt.Printf("%s: ok\n", name)
			return
		}
		fmt.Printf("%s: %s\n", name, err)
	}
	reportPair := func(name string, _ int, err error) { report(name, err) }
	reportTrace := func(name string, err error) {
		fmt.Printf("%s: %s\n", name, stripLine(err))
	}

	report("try.err", tryOnlyErr("bad"))
	_, err := tryPair("bad")
	reportPair("try.pair", 0, err)

	report("notnil.err", notNilOnlyErr(nil))
	_, err = notNilPair(nil)
	reportPair("notnil.pair", 0, err)

	report("ok.err", okOnlyErr(nil, "x"))
	_, err = okPair(nil, "x")
	reportPair("ok.pair", 0, err)

	ch := make(chan int)
	close(ch)
	report("recv.err", recvOnlyErr(ch))
	_, err = recvPair(ch)
	reportPair("recv.pair", 0, err)

	report("as.err", asOnlyErr("nope"))
	_, err = asPair("nope")
	reportPair("as.pair", 0, err)

	report("check.err", checkOnlyErr(errors.New("c-err")))
	_, err = checkPair(errors.New("c-err"))
	reportPair("check.pair", 0, err)

	report("open.err", openOnlyErr(true))
	_, err = openPair(true)
	reportPair("open.pair", 0, err)

	reportTrace("trace.err", traceOnlyErr("bad"))
	_, err = tracePair("bad")
	reportTrace("trace.pair", err)

	report("await.err", awaitOnlyErr())
	_, err = awaitPair()
	reportPair("await.pair", 0, err)
}
