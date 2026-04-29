// example/check mirrors docs/api/check.md one-to-one. Each section of
// the doc has a matching function below, named after the snippet it
// demonstrates. Run with:
//
//	go run -toolexec=q ./example/check
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/GiGurra/q/pkg/q"
)

// DB and File stand in for *sql.DB and *os.File so the q.Check snippets
// can read identically to the doc without pulling in database/sql + a
// driver or producing non-deterministic temp-path output. The shapes
// (Ping/Close, both returning error; second Close returns os.ErrClosed)
// match what the doc references.
type DB struct {
	failPing  bool
	failClose bool
}

func (d *DB) Ping() error {
	if d.failPing {
		return errors.New("ping failed")
	}
	return nil
}

func (d *DB) Close() error {
	if d.failClose {
		return errors.New("close failed")
	}
	return nil
}

type File struct{ closed bool }

func (f *File) Close() error {
	if f.closed {
		return os.ErrClosed
	}
	f.closed = true
	return nil
}

// ---------- "What q.Check does" ----------
//
//	q.Check(db.Ping())
func whatQCheckDoes(db *DB) error {
	q.Check(db.Ping())
	return nil
}

// ---------- "Chain methods on q.CheckE / .Catch swallow-pattern" ----------
//
//	q.CheckE(file.Close()).Catch(func(e error) error {
//	    if errors.Is(e, os.ErrClosed) {
//	        return nil
//	    }
//	    return e
//	})
func catchSwallowAlreadyClosed(file *File) error {
	q.CheckE(file.Close()).Catch(func(e error) error {
		if errors.Is(e, os.ErrClosed) {
			return nil
		}
		return e
	})
	return nil
}

// ---------- "Examples / shutdown" ----------
//
//	func shutdown(db *sql.DB, file *os.File) error {
//	    q.Check(db.Ping())
//	    q.CheckE(file.Close()).Wrap("closing log")
//	    q.Check(db.Close())
//	    return nil
//	}
func shutdown(db *DB, file *File) error {
	q.Check(db.Ping())
	q.CheckE(file.Close()).Wrap("closing log")
	q.Check(db.Close())
	return nil
}

// ---------- Remaining CheckE terminals (Err / ErrF / Wrap / Wrapf) ----------
// Doc lists them in a table; each exercised on the failing path so a
// regressed terminal would show in the diff.

func checkEErr(db *DB) error {
	q.CheckE(db.Ping()).Err(errors.New("replaced"))
	return nil
}

func checkEErrF(db *DB) error {
	q.CheckE(db.Ping()).ErrF(func(e error) error { return fmt.Errorf("transformed: %w", e) })
	return nil
}

func checkEWrap(db *DB) error {
	q.CheckE(db.Ping()).Wrap("starting up")
	return nil
}

func checkEWrapf(db *DB, id int) error {
	q.CheckE(db.Ping()).Wrapf("starting up id=%d", id)
	return nil
}

func show(label string, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", label, err)
		return
	}
	fmt.Printf("%s: ok\n", label)
}

func main() {
	// What q.Check does
	show("whatQCheckDoes(ok)", whatQCheckDoes(&DB{}))
	show("whatQCheckDoes(failPing)", whatQCheckDoes(&DB{failPing: true}))

	// Catch — swallow os.ErrClosed.
	f := &File{}
	show("catchSwallow(open file ok)", catchSwallowAlreadyClosed(f))
	show("catchSwallow(already closed swallowed)", catchSwallowAlreadyClosed(f))

	// shutdown — the doc's full example, walked through the input
	// permutations each bubble can fire on.
	show("shutdown(all ok)", shutdown(&DB{}, &File{}))
	show("shutdown(failPing)", shutdown(&DB{failPing: true}, &File{}))
	show("shutdown(failClose on db)", shutdown(&DB{failClose: true}, &File{}))
	show("shutdown(file already closed wrapped)", shutdown(&DB{}, &File{closed: true}))

	// Remaining CheckE terminals — all on the failing path.
	show("checkEErr(failPing)", checkEErr(&DB{failPing: true}))
	show("checkEErrF(failPing)", checkEErrF(&DB{failPing: true}))
	show("checkEWrap(failPing)", checkEWrap(&DB{failPing: true}))
	show("checkEWrapf(failPing,42)", checkEWrapf(&DB{failPing: true}, 42))
}
