// example/notnil mirrors docs/api/notnil.md one-to-one. Each section
// of the doc has a matching function below, named after the snippet
// it demonstrates. Run with:
//
//	go run -toolexec=q ./example/notnil
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// User and the doc's `table` / `ErrNotFound` referents — kept minimal
// so each snippet's call site can read like the doc.
type User struct {
	Name string
}

var ErrNotFound = errors.New("not found")

// table is the source of *User pointers (some real, some nil). The
// doc's snippets read `table[id]`; we keep the same shape here.
var table = map[int]*User{
	1: {Name: "Ada"},
	2: nil,
}

// backfill stands in for the doc's `backfill(id)` referenced from .Catch.
// Returns a pointer + nil to indicate recovery.
func backfill(id int) (*User, error) {
	return &User{Name: fmt.Sprintf("backfilled-%d", id)}, nil
}

// ---------- "What q.NotNil does" ----------
//
//	u := q.NotNil(table[id])
func whatQNotNilDoes(id int) (*User, error) {
	u := q.NotNil(table[id])
	return u, nil
}

// ---------- "Chain methods on q.NotNilE / Terminals" ----------

func notNilEErr(id int) (*User, error) {
	u := q.NotNilE(table[id]).Err(ErrNotFound)
	return u, nil
}

func notNilEErrF(id int) (*User, error) {
	u := q.NotNilE(table[id]).ErrF(func() error { return fmt.Errorf("no user %d", id) })
	return u, nil
}

func notNilEWrap(id int) (*User, error) {
	u := q.NotNilE(table[id]).Wrap("user not found")
	return u, nil
}

func notNilEWrapf(id int) (*User, error) {
	u := q.NotNilE(table[id]).Wrapf("no user %d", id)
	return u, nil
}

func notNilECatch(id int) (*User, error) {
	u := q.NotNilE(table[id]).Catch(func() (*User, error) {
		return backfill(id)
	})
	return u, nil
}

// ---------- "Statement forms / discard as precondition" ----------
//
//	q.NotNil(somePtr)                  // fail loudly and early if nil
//	// ... use somePtr.Field freely below
func discardPrecondition(somePtr *User) (string, error) {
	q.NotNil(somePtr)
	return somePtr.Name, nil
}

// ---------- "Statement forms" — same five positions as q.Try.
// define, assign, return, and hoist (discard above).

func formDefine(id int) (*User, error) {
	u := q.NotNil(table[id])
	return u, nil
}

func formAssign(id int) (*User, error) {
	var arr [1]*User
	arr[0] = q.NotNil(table[id])
	return arr[0], nil
}

func formReturn(id int) (*User, error) {
	return q.NotNil(table[id]), nil
}

func formHoist(id int) (string, error) {
	name := nameOf(q.NotNil(table[id]))
	return name, nil
}

func nameOf(u *User) string { return u.Name }

func main() {
	run := func(label string, fn func() (*User, error)) {
		u, err := fn()
		if err != nil {
			fmt.Printf("%s: err=%s\n", label, err)
			return
		}
		fmt.Printf("%s: ok=%s\n", label, u.Name)
	}

	// What q.NotNil does — bare bubble carries q.ErrNil.
	run("whatQNotNilDoes(1)", func() (*User, error) { return whatQNotNilDoes(1) })
	_, err := whatQNotNilDoes(2)
	if err != nil {
		fmt.Printf("whatQNotNilDoes(2): err=%s\n", err)
	}
	fmt.Printf("whatQNotNilDoes(2).is(q.ErrNil): %v\n", errors.Is(err, q.ErrNil))

	// Terminals.
	run("notNilEErr(2)", func() (*User, error) { return notNilEErr(2) })
	run("notNilEErrF(2)", func() (*User, error) { return notNilEErrF(2) })
	run("notNilEWrap(2)", func() (*User, error) { return notNilEWrap(2) })
	run("notNilEWrapf(2)", func() (*User, error) { return notNilEWrapf(2) })
	run("notNilECatch(2)", func() (*User, error) { return notNilECatch(2) }) // recovers via backfill

	// Statement forms.
	run("formDefine(1)", func() (*User, error) { return formDefine(1) })
	run("formAssign(1)", func() (*User, error) { return formAssign(1) })
	run("formReturn(1)", func() (*User, error) { return formReturn(1) })
	name, err := formHoist(1)
	if err != nil {
		fmt.Printf("formHoist(1): err=%s\n", err)
	} else {
		fmt.Printf("formHoist(1): ok=%s\n", name)
	}
	if _, err := formHoist(2); err != nil {
		fmt.Printf("formHoist(2): err=%s\n", err)
	}

	// Discard precondition.
	if name, err := discardPrecondition(table[1]); err != nil {
		fmt.Printf("discardPrecondition(1): err=%s\n", err)
	} else {
		fmt.Printf("discardPrecondition(1): ok=%s\n", name)
	}
	if _, err := discardPrecondition(table[2]); err != nil {
		fmt.Printf("discardPrecondition(2): err=%s\n", err)
	}
}
