// example/as mirrors docs/api/as.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/as
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// Admin / user are the doc's referents.
type User struct{ Name string }

type Admin struct {
	User
	Level int
}

// ---------- "What q.As does" ----------
//
//	n := q.As[int](x)
func whatQAsDoes(x any) (int, error) {
	n := q.As[int](x)
	return n, nil
}

// ---------- "Chain methods on q.AsE[T]" — useful example ----------
//
//	admin := q.AsE[Admin](user).Wrapf("%T is not an admin", user)
func chainExample(user any) (Admin, error) {
	admin := q.AsE[Admin](user).Wrapf("%T is not an admin", user)
	return admin, nil
}

// "Same as q.OkE" — exercise each terminal so a regression in any of
// them shows in the diff.

func asEErr(x any) (int, error) {
	n := q.AsE[int](x).Err(errors.New("not an int"))
	return n, nil
}

func asEErrF(x any) (int, error) {
	n := q.AsE[int](x).ErrF(func() error { return fmt.Errorf("nope: %T", x) })
	return n, nil
}

func asEWrap(x any) (int, error) {
	n := q.AsE[int](x).Wrap("expected int")
	return n, nil
}

func asEWrapf(x any) (int, error) {
	n := q.AsE[int](x).Wrapf("expected int, got %T", x)
	return n, nil
}

func asECatch(x any) (int, error) {
	n := q.AsE[int](x).Catch(func() (int, error) {
		return -1, nil
	})
	return n, nil
}

// ---------- "Statement forms — same five positions as q.Try" ----------

func formDefine(x any) (int, error) {
	n := q.As[int](x)
	return n, nil
}

func formAssign(x any) (int, error) {
	var arr [1]int
	arr[0] = q.As[int](x)
	return arr[0], nil
}

func formDiscard(x any) error {
	q.As[int](x)
	return nil
}

func formReturn(x any) (int, error) {
	return q.As[int](x), nil
}

func formHoist(x any) (int, error) {
	v := double(q.As[int](x))
	return v, nil
}

func double(n int) int { return n * 2 }

func main() {
	run := func(label string, fn func() (int, error)) {
		n, err := fn()
		if err != nil {
			fmt.Printf("%s: err=%s\n", label, err)
			return
		}
		fmt.Printf("%s: ok=%d\n", label, n)
	}

	// What q.As does — bare bubble carries q.ErrBadTypeAssert.
	run("whatQAsDoes(42)", func() (int, error) { return whatQAsDoes(42) })
	_, err := whatQAsDoes("hi")
	fmt.Printf("whatQAsDoes(hi): err=%s\n", err)
	fmt.Printf("whatQAsDoes(hi).is(q.ErrBadTypeAssert): %v\n", errors.Is(err, q.ErrBadTypeAssert))

	// Chain example.
	a, err := chainExample(Admin{User: User{Name: "ada"}, Level: 1})
	if err != nil {
		fmt.Printf("chainExample(admin): err=%s\n", err)
	} else {
		fmt.Printf("chainExample(admin): ok=%s/L%d\n", a.Name, a.Level)
	}
	_, err = chainExample(User{Name: "non-admin"})
	fmt.Printf("chainExample(user): err=%s\n", err)

	// AsE terminals — failing path.
	run("asEErr(hi)", func() (int, error) { return asEErr("hi") })
	run("asEErrF(hi)", func() (int, error) { return asEErrF("hi") })
	run("asEWrap(hi)", func() (int, error) { return asEWrap("hi") })
	run("asEWrapf(hi)", func() (int, error) { return asEWrapf("hi") })
	run("asECatch(hi)[recover]", func() (int, error) { return asECatch("hi") })

	// Statement forms.
	run("formDefine(7)", func() (int, error) { return formDefine(7) })
	run("formAssign(7)", func() (int, error) { return formAssign(7) })
	if err := formDiscard(7); err != nil {
		fmt.Printf("formDiscard(7): err=%s\n", err)
	} else {
		fmt.Println("formDiscard(7): ok")
	}
	if err := formDiscard("hi"); err != nil {
		fmt.Printf("formDiscard(hi): err=%s\n", err)
	}
	run("formReturn(7)", func() (int, error) { return formReturn(7) })
	run("formHoist(7)", func() (int, error) { return formHoist(7) })
}
