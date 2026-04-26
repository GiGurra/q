// example/open — q.Open and q.OpenE for resource acquisition with
// defer-on-success cleanup. .DeferCleanup is always the terminal.
// Run with:
//
//	go run -toolexec=q ./example/open
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// Conn is a toy resource. Close is recorded in closeLog so each
// example prints what was cleaned up.
type Conn struct{ id int }

var closeLog []int

func (c *Conn) Close() { closeLog = append(closeLog, c.id) }

// dial produces a *Conn or an error based on fail.
func dial(id int, fail bool) (*Conn, error) {
	if fail {
		return nil, fmt.Errorf("dial failed for %d", id)
	}
	return &Conn{id: id}, nil
}

// bareOpen — acquire + defer cleanup. On err, bubble; no cleanup
// registered (nothing to clean up).
func bareOpen(id int, fail bool) error {
	conn := q.Open(dial(id, fail)).DeferCleanup((*Conn).Close)
	_ = conn
	return nil
}

// openWithWrap — shape the bubbled error, still register the defer
// on success.
func openWithWrap(id int, fail bool) error {
	conn := q.OpenE(dial(id, fail)).Wrap("dialing").DeferCleanup((*Conn).Close)
	_ = conn
	return nil
}

// openWithCatchRecover — on error, recover with a fallback conn.
// The defer fires on the *recovered* conn, not the failed one.
func openWithCatchRecover(id int, fail bool) error {
	conn := q.OpenE(dial(id, fail)).Catch(func(e error) (*Conn, error) {
		return &Conn{id: 99}, nil // substitute a fallback
	}).DeferCleanup((*Conn).Close)
	_ = conn
	return nil
}

// twoOpensLIFO — two sequential Opens. On success, both cleanups
// register; defer is LIFO so the second one runs first.
func twoOpensLIFO(failA, failB bool) error {
	a := q.Open(dial(100, failA)).DeferCleanup((*Conn).Close)
	b := q.Open(dial(200, failB)).DeferCleanup((*Conn).Close)
	_, _ = a, b
	return nil
}

// openInReturn — q.Open in return-position.
func openInReturn(id int, fail bool) (*Conn, error) {
	return q.Open(dial(id, fail)).DeferCleanup((*Conn).Close), nil
}

var ErrFailover = errors.New("failover triggered")

func main() {
	report := func(label string, err error) {
		if err != nil {
			fmt.Printf("%-30s => err: %v  closed=%v\n", label, err, closeLog)
		} else {
			fmt.Printf("%-30s => ok           closed=%v\n", label, closeLog)
		}
		closeLog = closeLog[:0]
	}

	report("bareOpen(1, ok)", bareOpen(1, false))
	report("bareOpen(1, fail)", bareOpen(1, true))

	report("openWithWrap(2, ok)", openWithWrap(2, false))
	report("openWithWrap(2, fail)", openWithWrap(2, true))

	report("openWithCatchRecover(3, ok)", openWithCatchRecover(3, false))
	report("openWithCatchRecover(3, fail)", openWithCatchRecover(3, true))

	report("twoOpensLIFO(ok, ok)", twoOpensLIFO(false, false))
	report("twoOpensLIFO(fail, _)", twoOpensLIFO(true, false))
	report("twoOpensLIFO(ok, fail)", twoOpensLIFO(false, true))

	_, err := openInReturn(10, false)
	report("openInReturn(10, ok)", err)
	_, err = openInReturn(10, true)
	report("openInReturn(10, fail)", err)

	// Show the sentinel usage is fine too.
	_ = ErrFailover
}
