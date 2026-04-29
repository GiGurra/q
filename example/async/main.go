// example/async mirrors docs/api/async.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/async
package main

import (
	"errors"
	"fmt"
	"sort"

	"github.com/GiGurra/q/pkg/q"
)

// User and the doc's `fetchUser` / `anonUser` referents.
type User struct{ Name string }

var byName = map[int]string{
	1: "Ada",
	2: "Linus",
}

func fetchUser() (User, error) { return User{Name: byName[1]}, nil }

func anonUser() User { return User{Name: "<anon>"} }

// ---------- "What q.Await does" ----------
//
//	f := q.Async(fetchUser)
//	u := q.Await(f)
func whatQAwaitDoes() (User, error) {
	f := q.Async(fetchUser)
	u := q.Await(f)
	return u, nil
}

// ---------- "Chain methods on q.AwaitE" ----------
//
//	u := q.AwaitE(f).Wrapf("fetching user %d", id)
//	u := q.AwaitE(f).Catch(func(e error) (User, error) { return anonUser(), nil })
func awaitEWrapf(id int) (User, error) {
	f := q.Async(func() (User, error) {
		if name, ok := byName[id]; ok {
			return User{Name: name}, nil
		}
		return User{}, errors.New("not found")
	})
	u := q.AwaitE(f).Wrapf("fetching user %d", id)
	return u, nil
}

func awaitECatch(id int) (User, error) {
	f := q.Async(func() (User, error) {
		if name, ok := byName[id]; ok {
			return User{Name: name}, nil
		}
		return User{}, errors.New("not found")
	})
	u := q.AwaitE(f).Catch(func(e error) (User, error) { return anonUser(), nil })
	return u, nil
}

// ---------- "Fan-out / fan-in" ----------
//
//	futures := make([]q.Future[int], len(urls))
//	for i, url := range urls {
//	    futures[i] = q.Async(func() (int, error) { return fetchSize(url) })
//	}
//	total := 0
//	for _, f := range futures {
//	    total += q.Await(f)
//	}
func fetchSize(url string) (int, error) { return len(url), nil }

func fanOut(urls []string) (int, error) {
	futures := make([]q.Future[int], len(urls))
	for i, url := range urls {
		futures[i] = q.Async(func() (int, error) { return fetchSize(url) })
	}
	total := 0
	for _, f := range futures {
		total += q.Await(f)
	}
	return total, nil
}

// ---------- "Statement forms — same five positions as q.Try" ----------

func formDefine() (User, error) {
	u := q.Await(q.Async(fetchUser))
	return u, nil
}

func formAssign() (User, error) {
	var arr [1]User
	arr[0] = q.Await(q.Async(fetchUser))
	return arr[0], nil
}

func formDiscard() error {
	q.Await(q.Async(fetchUser))
	return nil
}

func formReturn() (User, error) {
	return q.Await(q.Async(fetchUser)), nil
}

func formHoist() (string, error) {
	name := nameOf(q.Await(q.Async(fetchUser)))
	return name, nil
}

func nameOf(u User) string { return u.Name }

func main() {
	if u, err := whatQAwaitDoes(); err != nil {
		fmt.Printf("whatQAwaitDoes: err=%s\n", err)
	} else {
		fmt.Printf("whatQAwaitDoes: %s\n", u.Name)
	}

	if u, err := awaitEWrapf(1); err != nil {
		fmt.Printf("awaitEWrapf(1): err=%s\n", err)
	} else {
		fmt.Printf("awaitEWrapf(1): %s\n", u.Name)
	}
	if _, err := awaitEWrapf(99); err != nil {
		fmt.Printf("awaitEWrapf(99): err=%s\n", err)
	}

	if u, err := awaitECatch(99); err != nil {
		fmt.Printf("awaitECatch(99): err=%s\n", err)
	} else {
		fmt.Printf("awaitECatch(99): %s (anon)\n", u.Name)
	}

	urls := []string{"a", "bb", "ccc", "dddd"}
	sort.Strings(urls) // determinism
	if total, err := fanOut(urls); err != nil {
		fmt.Printf("fanOut: err=%s\n", err)
	} else {
		fmt.Printf("fanOut: total=%d\n", total)
	}

	if u, err := formDefine(); err != nil {
		fmt.Printf("formDefine: err=%s\n", err)
	} else {
		fmt.Printf("formDefine: %s\n", u.Name)
	}
	if u, err := formAssign(); err != nil {
		fmt.Printf("formAssign: err=%s\n", err)
	} else {
		fmt.Printf("formAssign: %s\n", u.Name)
	}
	if err := formDiscard(); err != nil {
		fmt.Printf("formDiscard: err=%s\n", err)
	} else {
		fmt.Println("formDiscard: ok")
	}
	if u, err := formReturn(); err != nil {
		fmt.Printf("formReturn: err=%s\n", err)
	} else {
		fmt.Printf("formReturn: %s\n", u.Name)
	}
	if name, err := formHoist(); err != nil {
		fmt.Printf("formHoist: err=%s\n", err)
	} else {
		fmt.Printf("formHoist: %s\n", name)
	}
}
