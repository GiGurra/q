// example/ok mirrors docs/api/ok.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/ok
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type User struct{ Name string }

var users = map[int]User{
	1: {Name: "Ada"},
	2: {Name: "Linus"},
}

var ErrNotFound = errors.New("not found")

func lookup(key int) (User, bool) {
	u, ok := users[key]
	return u, ok
}

func backfill(id int) (User, error) {
	return User{Name: fmt.Sprintf("backfilled-%d", id)}, nil
}

// ---------- "What q.Ok does — single-call form" ----------
//
//	v := q.Ok(lookup(key))
func singleCall(key int) (User, error) {
	v := q.Ok(lookup(key))
	return v, nil
}

// "Two-arg form":
//
//	v, ok := table[key]
//	return q.Ok(v, ok), nil
func twoArg(key int) (User, error) {
	v, ok := users[key]
	return q.Ok(v, ok), nil
}

// ---------- "Chain methods on q.OkE" ----------

func okEErr(id int) (User, error) {
	v := q.OkE(lookup(id)).Err(ErrNotFound)
	return v, nil
}

func okEErrF(id int) (User, error) {
	v := q.OkE(lookup(id)).ErrF(func() error { return fmt.Errorf("missing %d", id) })
	return v, nil
}

func okEWrap(id int) (User, error) {
	v := q.OkE(lookup(id)).Wrap("user lookup failed")
	return v, nil
}

func okEWrapf(id int) (User, error) {
	v := q.OkE(lookup(id)).Wrapf("no user %d", id)
	return v, nil
}

func okECatch(id int) (User, error) {
	v := q.OkE(lookup(id)).Catch(func() (User, error) { return backfill(id) })
	return v, nil
}

// ---------- "Statement forms" ----------

func formDefine(key int) (User, error) {
	u := q.Ok(lookup(key))
	return u, nil
}

func formAssign(key int) (User, error) {
	var arr [1]User
	arr[0] = q.Ok(lookup(key))
	return arr[0], nil
}

func formDiscard(key int) error {
	q.Ok(lookup(key))
	return nil
}

func formReturn(key int) (User, error) {
	return q.Ok(lookup(key)), nil
}

func formHoist(key int) (string, error) {
	name := nameOf(q.Ok(lookup(key)))
	return name, nil
}

func nameOf(u User) string { return u.Name }

func main() {
	run := func(label string, fn func() (User, error)) {
		u, err := fn()
		if err != nil {
			fmt.Printf("%s: err=%s\n", label, err)
			return
		}
		fmt.Printf("%s: ok=%s\n", label, u.Name)
	}

	run("singleCall(1)", func() (User, error) { return singleCall(1) })
	_, err := singleCall(99)
	fmt.Printf("singleCall(99): err=%s\n", err)
	fmt.Printf("singleCall(99).is(q.ErrNotOk): %v\n", errors.Is(err, q.ErrNotOk))

	run("twoArg(1)", func() (User, error) { return twoArg(1) })

	run("okEErr(99)", func() (User, error) { return okEErr(99) })
	run("okEErrF(99)", func() (User, error) { return okEErrF(99) })
	run("okEWrap(99)", func() (User, error) { return okEWrap(99) })
	run("okEWrapf(99)", func() (User, error) { return okEWrapf(99) })
	run("okECatch(99) [recovers via backfill]", func() (User, error) { return okECatch(99) })

	run("formDefine(1)", func() (User, error) { return formDefine(1) })
	run("formAssign(1)", func() (User, error) { return formAssign(1) })
	if err := formDiscard(1); err != nil {
		fmt.Printf("formDiscard(1): err=%s\n", err)
	} else {
		fmt.Println("formDiscard(1): ok")
	}
	if err := formDiscard(99); err != nil {
		fmt.Printf("formDiscard(99): err=%s\n", err)
	}
	run("formReturn(1)", func() (User, error) { return formReturn(1) })
	if name, err := formHoist(1); err != nil {
		fmt.Printf("formHoist(1): err=%s\n", err)
	} else {
		fmt.Printf("formHoist(1): %s\n", name)
	}
}
