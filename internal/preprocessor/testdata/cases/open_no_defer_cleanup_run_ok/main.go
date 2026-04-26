// Fixture: q.Open / q.OpenE terminated by .NoDeferCleanup() — opt-in
// "we acquired this resource but no cleanup is wanted" form. Same
// bubble path as .DeferCleanup, but no defer is registered. The fixture
// observes a global cleanup-counter to assert the cleanup did NOT
// fire (counter stays 0 after success), while .DeferCleanup on the same
// resource does fire (counter > 0).
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

var ErrBoom = errors.New("boom")

type Conn struct{ id int }

var closes int // counts Close calls; reset per-helper

func closeConn(*Conn) { closes++ }

func dial(id int, fail bool) (*Conn, error) {
	if fail {
		return nil, ErrBoom
	}
	return &Conn{id: id}, nil
}

// noDeferCleanupSuccess — happy path, no defer registered.
func noDeferCleanupSuccess() (*Conn, error) {
	conn := q.Open(dial(1, false)).NoDeferCleanup()
	return conn, nil
}

// noDeferCleanupBubble — failure path, bubble fires same as DeferCleanup.
func noDeferCleanupBubble() (*Conn, error) {
	conn := q.Open(dial(2, true)).NoDeferCleanup()
	return conn, nil
}

// noDeferCleanupEWrap — chain-shape on OpenE composes with NoDeferCleanup.
func noDeferCleanupEWrap() (*Conn, error) {
	conn := q.OpenE(dial(3, true)).Wrap("dialing").NoDeferCleanup()
	return conn, nil
}

// noDeferCleanupECatch — Catch recovers, NoDeferCleanup still skips defer.
func noDeferCleanupECatch() (*Conn, error) {
	conn := q.OpenE(dial(4, true)).Catch(func(error) (*Conn, error) {
		return &Conn{id: 99}, nil
	}).NoDeferCleanup()
	return conn, nil
}

// regression: .DeferCleanup still fires the defer cleanup.
//
//q:no-escape-check
func deferCleanupSuccess() (*Conn, error) {
	conn := q.Open(dial(5, false)).DeferCleanup(closeConn)
	return conn, nil
}

func main() {
	// noDeferCleanupSuccess — value returned, defer NOT registered.
	closes = 0
	c, err := noDeferCleanupSuccess()
	fmt.Printf("noDeferCleanupSuccess: id=%d err=%v closes=%d\n", c.id, err, closes)

	// noDeferCleanupBubble — bubble fires; closes still 0 since the
	// allocation failed and there was nothing to defer anyway.
	closes = 0
	c, err = noDeferCleanupBubble()
	fmt.Printf("noDeferCleanupBubble: nil=%v err=%v closes=%d\n", c == nil, err, closes)

	// noDeferCleanupEWrap — bubble shaped; still no defer.
	closes = 0
	c, err = noDeferCleanupEWrap()
	fmt.Printf("noDeferCleanupEWrap: nil=%v err=%v closes=%d\n", c == nil, err, closes)

	// noDeferCleanupECatch — Catch recovers to id=99; no defer fires.
	closes = 0
	c, err = noDeferCleanupECatch()
	fmt.Printf("noDeferCleanupECatch: id=%d err=%v closes=%d\n", c.id, err, closes)

	// regression: plain DeferCleanup still fires the defer when the helper returns.
	closes = 0
	c, err = deferCleanupSuccess()
	fmt.Printf("deferCleanupSuccess: id=%d err=%v closes=%d\n", c.id, err, closes)
}
