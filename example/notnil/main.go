// example/notnil — every shape of q.NotNil and q.NotNilE. The
// family uses the sentinel q.ErrNil for the bare form and the same
// chain vocabulary as TryE (adjusted for the no-source-error case).
// Run with:
//
//	go run -toolexec=q ./example/notnil
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

var ErrMissing = errors.New("missing")

// bareNotNil — bubbles q.ErrNil if the pointer is nil. Callers can
// errors.Is against q.ErrNil to detect the nil bubble specifically.
func bareNotNil(table map[int]*string, id int) (string, error) {
	name := q.NotNil(table[id])
	return *name, nil
}

// notNilWithErr — substitute a typed error for the nil branch.
func notNilWithErr(table map[int]*string, id int) (string, error) {
	name := q.NotNilE(table[id]).Err(ErrMissing)
	return *name, nil
}

// notNilWithErrF — compute the bubbled error via a thunk (no
// source-error to pass in on the nil branch).
func notNilWithErrF(table map[int]*string, id int) (string, error) {
	name := q.NotNilE(table[id]).ErrF(func() error {
		return fmt.Errorf("no entry for id=%d", id)
	})
	return *name, nil
}

// notNilWithWrap — bubble errors.New(msg). There's no source error
// to %w-wrap on the nil branch, so the message stands alone.
func notNilWithWrap(table map[int]*string, id int) (string, error) {
	name := q.NotNilE(table[id]).Wrap("name not found")
	return *name, nil
}

// notNilWithWrapf — bubble fmt.Errorf(format, args...). Again, no
// %w; the format *is* the complete message.
func notNilWithWrapf(table map[int]*string, id int) (string, error) {
	name := q.NotNilE(table[id]).Wrapf("no name for id=%d", id)
	return *name, nil
}

// notNilWithCatch — on nil, substitute a value from somewhere else.
// Returning a non-nil pointer short-circuits the bubble.
func notNilWithCatch(table map[int]*string, id int) (string, error) {
	fallback := "unknown"
	name := q.NotNilE(table[id]).Catch(func() (*string, error) {
		return &fallback, nil
	})
	return *name, nil
}

func main() {
	n := "Ada"
	table := map[int]*string{1: &n}

	cases := []struct {
		name string
		fn   func(map[int]*string, int) (string, error)
	}{
		{"bareNotNil", bareNotNil},
		{"notNilWithErr", notNilWithErr},
		{"notNilWithErrF", notNilWithErrF},
		{"notNilWithWrap", notNilWithWrap},
		{"notNilWithWrapf", notNilWithWrapf},
		{"notNilWithCatch", notNilWithCatch},
	}

	for _, c := range cases {
		for _, id := range []int{1, 99} {
			v, err := c.fn(table, id)
			if err != nil {
				fmt.Printf("%-20s(id=%d) => err: %v\n", c.name, id, err)
			} else {
				fmt.Printf("%-20s(id=%d) => %q\n", c.name, id, v)
			}
		}
	}

	// Sanity: the bare form's bubble is errors.Is-able against q.ErrNil.
	_, err := bareNotNil(table, 99)
	fmt.Printf("errors.Is(err, q.ErrNil) = %v\n", errors.Is(err, q.ErrNil))
}
