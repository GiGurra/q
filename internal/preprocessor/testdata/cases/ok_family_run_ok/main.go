// Fixture: exercises bare q.Ok and every q.OkE chain method end-to-
// end. Covers both call-argument shapes: the two-arg form
// (q.Ok(v, ok)) and the single-call form (q.Ok(fn()) where fn
// returns (T, bool)), since Go's f(g()) tuple-forwarding rule
// applies to both. One helper per method; each invoked on both the
// ok=true and ok=false paths.
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

var ErrNotFound = errors.New("not found")

// lookup returns (value, ok) so the single-call form q.Ok(lookup(k))
// is exercised in addition to the two-arg form q.Ok(v, ok).
func lookup(table map[string]int, key string) (int, bool) {
	v, ok := table[key]
	return v, ok
}

func bareTwoArg(table map[string]int, key string) (int, error) {
	v, ok := table[key]
	return q.Ok(v, ok), nil
}

func bareSingleCall(table map[string]int, key string) (int, error) {
	return q.Ok(lookup(table, key)), nil
}

func errMethod(table map[string]int, key string) (int, error) {
	v, ok := table[key]
	return q.OkE(v, ok).Err(ErrNotFound), nil
}

func errFMethod(table map[string]int, key string) (int, error) {
	v := q.OkE(lookup(table, key)).ErrF(func() error {
		return errors.New("computed")
	})
	return v, nil
}

func wrapMethod(table map[string]int, key string) (int, error) {
	v := q.OkE(lookup(table, key)).Wrap("ok-context")
	return v, nil
}

func wrapfMethod(table map[string]int, key string) (int, error) {
	v, ok := table[key]
	return q.OkE(v, ok).Wrapf("no entry for %q", key), nil
}

func catchRecover(table map[string]int, key string) (int, error) {
	v := q.OkE(lookup(table, key)).Catch(func() (int, error) {
		return 99, nil
	})
	return v, nil
}

func catchTransform(table map[string]int, key string) (int, error) {
	v := q.OkE(lookup(table, key)).Catch(func() (int, error) {
		return 0, fmt.Errorf("catch-transformed not-ok")
	})
	return v, nil
}

// formsDiscardAndAssign exercises the assign and discard forms in
// one place — prior fixtures showed these matter per-family, not
// just for Try. The discard runs first so the forms.bad case exits
// via the chain's Err(ErrNotFound) instead of the later assign's
// bare ErrNotOk bubble.
func formsDiscardAndAssign(table map[string]int, key string) (int, error) {
	v, ok := table[key]
	_ = q.OkE(v, ok).Err(ErrNotFound) // chain discard — bubbles ErrNotFound when !ok
	var out int
	out = q.Ok(lookup(table, key)) // assign form — only reached on ok
	return out, nil
}

// hoistForm exercises q.* nested inside a call arg — the hoist path.
func identity(x int) int { return x }

func hoistForm(table map[string]int, key string) (int, error) {
	return identity(q.Ok(lookup(table, key))), nil
}

func report(name string, n int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
	} else {
		fmt.Printf("%s: ok=%d\n", name, n)
	}
}

func main() {
	table := map[string]int{"present": 7}

	n, err := bareTwoArg(table, "present")
	report("bareTwo.ok", n, err)
	n, err = bareTwoArg(table, "missing")
	report("bareTwo.bad", n, err)

	n, err = bareSingleCall(table, "present")
	report("bareOne.ok", n, err)
	n, err = bareSingleCall(table, "missing")
	report("bareOne.bad", n, err)

	n, err = errMethod(table, "present")
	report("Err.ok", n, err)
	n, err = errMethod(table, "missing")
	report("Err.bad", n, err)

	n, err = errFMethod(table, "present")
	report("ErrF.ok", n, err)
	n, err = errFMethod(table, "missing")
	report("ErrF.bad", n, err)

	n, err = wrapMethod(table, "present")
	report("Wrap.ok", n, err)
	n, err = wrapMethod(table, "missing")
	report("Wrap.bad", n, err)

	n, err = wrapfMethod(table, "present")
	report("Wrapf.ok", n, err)
	n, err = wrapfMethod(table, "missing")
	report("Wrapf.bad", n, err)

	n, err = catchRecover(table, "present")
	report("CatchRec.ok", n, err)
	n, err = catchRecover(table, "missing")
	report("CatchRec.bad", n, err)

	n, err = catchTransform(table, "present")
	report("CatchXf.ok", n, err)
	n, err = catchTransform(table, "missing")
	report("CatchXf.bad", n, err)

	n, err = hoistForm(table, "present")
	report("hoist.ok", n, err)
	n, err = hoistForm(table, "missing")
	report("hoist.bad", n, err)

	// Cover the assign form plus a discard that must bubble.
	n, err = formsDiscardAndAssign(table, "present")
	report("forms.ok", n, err)
	n, err = formsDiscardAndAssign(table, "missing")
	report("forms.bad", n, err)

	// Sentinel identity — q.Ok's bare bubble is errors.Is-detectable.
	_, err = bareTwoArg(table, "missing")
	fmt.Printf("isErrNotOk: %v\n", errors.Is(err, q.ErrNotOk))
}
