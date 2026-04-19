// Fixture: q.Open / q.OpenE across every form Try supports — define,
// assign, discard, return-position, and nested-in-call (hoist). Plus
// the OpenE shape methods Err/ErrF/Wrap/Wrapf/Catch mirroring the
// TryE coverage. A global counter observes each deferred cleanup so
// the test asserts not only that the cleanup fires, but also the
// order (defer is LIFO within a function).
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

var (
	ErrBoom     = errors.New("boom")
	ErrReplaced = errors.New("replaced")
)

// Conn is a toy resource; its Close is the typical cleanup target.
type Conn struct {
	id int
}

// closeLog records every Close invocation for ordering assertions.
var closeLog []int

func closeConn(c *Conn) {
	closeLog = append(closeLog, c.id)
}

func dial(id int, fail bool) (*Conn, error) {
	if fail {
		return nil, ErrBoom
	}
	return &Conn{id: id}, nil
}

// formDefine: `v := q.Open(...)` — direct-bind with := on a fresh ident.
func openDefine(fail bool) error {
	conn := q.Open(dial(1, fail)).Release(closeConn)
	_ = conn
	return nil
}

// formAssign: pre-declared LHS.
func openAssign(fail bool) error {
	var conn *Conn
	conn = q.Open(dial(2, fail)).Release(closeConn)
	_ = conn
	return nil
}

// formDiscard: no LHS. Still binds to a temp so defer has a target.
func openDiscard(fail bool) error {
	q.Open(dial(3, fail)).Release(closeConn)
	return nil
}

// formReturn: q.* as one of the return results.
func openReturn(fail bool) (*Conn, error) {
	return q.Open(dial(4, fail)).Release(closeConn), nil
}

// formHoist: q.* nested inside a larger expression.
func openHoist(fail bool) (int, error) {
	id := identity(q.Open(dial(5, fail)).Release(closeConn)).id
	return id, nil
}

// OpenE shape methods — one-per-method coverage of the chain shape.
func openEErr(fail bool) error {
	conn := q.OpenE(dial(6, fail)).Err(ErrReplaced).Release(closeConn)
	_ = conn
	return nil
}

func openEErrF(fail bool) error {
	conn := q.OpenE(dial(7, fail)).ErrF(func(e error) error { return fmt.Errorf("transformed: %w", e) }).Release(closeConn)
	_ = conn
	return nil
}

func openEWrap(fail bool) error {
	conn := q.OpenE(dial(8, fail)).Wrap("dialing").Release(closeConn)
	_ = conn
	return nil
}

func openEWrapf(fail bool, host string) error {
	conn := q.OpenE(dial(9, fail)).Wrapf("dialing %q", host).Release(closeConn)
	_ = conn
	return nil
}

// Catch-recover: when Catch returns (recovered, nil), the recovered
// value feeds Release — the deferred cleanup runs on the recovered
// conn, not the failed one.
func openECatchRecover(fail bool) error {
	conn := q.OpenE(dial(10, fail)).Catch(func(e error) (*Conn, error) {
		return &Conn{id: 99}, nil
	}).Release(closeConn)
	_ = conn
	return nil
}

// Catch-bubble: Catch returns (zero, newErr); newErr bubbles.
func openECatchBubble(fail bool) error {
	conn := q.OpenE(dial(11, fail)).Catch(func(e error) (*Conn, error) {
		return nil, fmt.Errorf("caught: %w", e)
	}).Release(closeConn)
	_ = conn
	return nil
}

// Defer-ordering check: two Opens in sequence — successful exit must
// call cleanup on conn2 before conn1 (LIFO). The fail argument picks
// which one errors so the runtime demonstrates "only the acquired
// ones fire cleanup".
func twoOpens(failFirst, failSecond bool) error {
	c1 := q.Open(dial(100, failFirst)).Release(closeConn)
	c2 := q.Open(dial(200, failSecond)).Release(closeConn)
	_, _ = c1, c2
	return nil
}

func identity(c *Conn) *Conn { return c }

func resetLog() {
	closeLog = closeLog[:0]
}

func reportE(name string, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s closed=%v\n", name, err, closeLog)
	} else {
		fmt.Printf("%s: ok closed=%v\n", name, closeLog)
	}
}

func main() {
	resetLog()
	reportE("openDefine.ok", openDefine(false))
	resetLog()
	reportE("openDefine.bad", openDefine(true))

	resetLog()
	reportE("openAssign.ok", openAssign(false))
	resetLog()
	reportE("openAssign.bad", openAssign(true))

	resetLog()
	reportE("openDiscard.ok", openDiscard(false))
	resetLog()
	reportE("openDiscard.bad", openDiscard(true))

	resetLog()
	_, err := openReturn(false)
	reportE("openReturn.ok", err)
	resetLog()
	_, err = openReturn(true)
	reportE("openReturn.bad", err)

	resetLog()
	_, err = openHoist(false)
	reportE("openHoist.ok", err)
	resetLog()
	_, err = openHoist(true)
	reportE("openHoist.bad", err)

	resetLog()
	reportE("openEErr.ok", openEErr(false))
	resetLog()
	reportE("openEErr.bad", openEErr(true))

	resetLog()
	reportE("openEErrF.ok", openEErrF(false))
	resetLog()
	reportE("openEErrF.bad", openEErrF(true))

	resetLog()
	reportE("openEWrap.ok", openEWrap(false))
	resetLog()
	reportE("openEWrap.bad", openEWrap(true))

	resetLog()
	reportE("openEWrapf.ok", openEWrapf(false, "host-a"))
	resetLog()
	reportE("openEWrapf.bad", openEWrapf(true, "host-b"))

	resetLog()
	reportE("openECatchRecover.ok", openECatchRecover(false))
	resetLog()
	reportE("openECatchRecover.recovered", openECatchRecover(true))

	resetLog()
	reportE("openECatchBubble.ok", openECatchBubble(false))
	resetLog()
	reportE("openECatchBubble.bad", openECatchBubble(true))

	// LIFO defer ordering: conn2 (200) closes before conn1 (100).
	resetLog()
	reportE("twoOpens.okok", twoOpens(false, false))
	// First fails → no resource to cleanup.
	resetLog()
	reportE("twoOpens.badFirst", twoOpens(true, false))
	// First succeeds, second fails → only conn1 cleanup fires.
	resetLog()
	reportE("twoOpens.badSecond", twoOpens(false, true))
}
