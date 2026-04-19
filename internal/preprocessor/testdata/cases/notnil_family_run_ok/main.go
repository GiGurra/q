// Fixture: exercises bare q.NotNil and every q.NotNilE chain method
// end-to-end. One helper per method; each invoked on both the
// "pointer present" and "pointer absent" paths.
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

var ErrNotFound = errors.New("not found")

func bareNotNil(p *int) (int, error) {
	v := q.NotNil(p)
	return *v, nil
}

func errMethod(p *int) (int, error) {
	v := q.NotNilE(p).Err(ErrNotFound)
	return *v, nil
}

func errFMethod(p *int) (int, error) {
	v := q.NotNilE(p).ErrF(func() error {
		return errors.New("computed")
	})
	return *v, nil
}

func wrapMethod(p *int) (int, error) {
	v := q.NotNilE(p).Wrap("nil-context")
	return *v, nil
}

func wrapfMethod(name string, p *int) (int, error) {
	v := q.NotNilE(p).Wrapf("nil at %q", name)
	return *v, nil
}

func catchRecover(p *int) (int, error) {
	// Recover with a fallback pointer.
	fallback := 99
	v := q.NotNilE(p).Catch(func() (*int, error) {
		return &fallback, nil
	})
	return *v, nil
}

func catchTransform(p *int) (int, error) {
	v := q.NotNilE(p).Catch(func() (*int, error) {
		return nil, fmt.Errorf("catch-transformed nil")
	})
	return *v, nil
}

func report(name string, n int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
	} else {
		fmt.Printf("%s: ok=%d\n", name, n)
	}
}

func main() {
	x := 7
	good := &x
	var bad *int

	n, err := bareNotNil(good)
	report("bare.ok", n, err)
	n, err = bareNotNil(bad)
	report("bare.bad", n, err)

	n, err = errMethod(good)
	report("Err.ok", n, err)
	n, err = errMethod(bad)
	report("Err.bad", n, err)

	n, err = errFMethod(good)
	report("ErrF.ok", n, err)
	n, err = errFMethod(bad)
	report("ErrF.bad", n, err)

	n, err = wrapMethod(good)
	report("Wrap.ok", n, err)
	n, err = wrapMethod(bad)
	report("Wrap.bad", n, err)

	n, err = wrapfMethod("v", good)
	report("Wrapf.ok", n, err)
	n, err = wrapfMethod("v", bad)
	report("Wrapf.bad", n, err)

	n, err = catchRecover(good)
	report("CatchRec.ok", n, err)
	n, err = catchRecover(bad)
	report("CatchRec.bad", n, err)

	n, err = catchTransform(good)
	report("CatchXf.ok", n, err)
	n, err = catchTransform(bad)
	report("CatchXf.bad", n, err)
}
