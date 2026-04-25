// Fixture: q.Open / q.OpenE terminated by .NoRelease() — opt-in
// "we acquired this resource but no cleanup is wanted" form. Same
// bubble path as .Release, but no defer is registered. The fixture
// observes a global cleanup-counter to assert the cleanup did NOT
// fire (counter stays 0 after success), while .Release on the same
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

// noReleaseSuccess — happy path, no defer registered.
func noReleaseSuccess() (*Conn, error) {
	conn := q.Open(dial(1, false)).NoRelease()
	return conn, nil
}

// noReleaseBubble — failure path, bubble fires same as Release.
func noReleaseBubble() (*Conn, error) {
	conn := q.Open(dial(2, true)).NoRelease()
	return conn, nil
}

// noReleaseEWrap — chain-shape on OpenE composes with NoRelease.
func noReleaseEWrap() (*Conn, error) {
	conn := q.OpenE(dial(3, true)).Wrap("dialing").NoRelease()
	return conn, nil
}

// noReleaseECatch — Catch recovers, NoRelease still skips defer.
func noReleaseECatch() (*Conn, error) {
	conn := q.OpenE(dial(4, true)).Catch(func(error) (*Conn, error) {
		return &Conn{id: 99}, nil
	}).NoRelease()
	return conn, nil
}

// regression: .Release still fires the defer cleanup.
//
//q:no-escape-check
func releaseSuccess() (*Conn, error) {
	conn := q.Open(dial(5, false)).Release(closeConn)
	return conn, nil
}

func main() {
	// noReleaseSuccess — value returned, defer NOT registered.
	closes = 0
	c, err := noReleaseSuccess()
	fmt.Printf("noReleaseSuccess: id=%d err=%v closes=%d\n", c.id, err, closes)

	// noReleaseBubble — bubble fires; closes still 0 since the
	// allocation failed and there was nothing to defer anyway.
	closes = 0
	c, err = noReleaseBubble()
	fmt.Printf("noReleaseBubble: nil=%v err=%v closes=%d\n", c == nil, err, closes)

	// noReleaseEWrap — bubble shaped; still no defer.
	closes = 0
	c, err = noReleaseEWrap()
	fmt.Printf("noReleaseEWrap: nil=%v err=%v closes=%d\n", c == nil, err, closes)

	// noReleaseECatch — Catch recovers to id=99; no defer fires.
	closes = 0
	c, err = noReleaseECatch()
	fmt.Printf("noReleaseECatch: id=%d err=%v closes=%d\n", c.id, err, closes)

	// regression: plain Release still fires the defer when the helper returns.
	closes = 0
	c, err = releaseSuccess()
	fmt.Printf("releaseSuccess: id=%d err=%v closes=%d\n", c.id, err, closes)
}
