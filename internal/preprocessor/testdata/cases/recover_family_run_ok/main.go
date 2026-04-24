// Fixture: q.Recover and q.RecoverE convert panics into errors via
// a deferred call at the top of the function. Both are pure runtime
// helpers — the preprocessor does no rewriting for them; Go's
// recover() sees the in-flight panic because q.Recover / the
// terminal RecoverE method IS the deferred function.
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// bareRecover converts any panic in body into *q.PanicError.
func bareRecover(body func()) (err error) {
	defer q.Recover(&err)
	body()
	return nil
}

// recoverMap translates the panic value into a typed error.
type myErr struct{ tag string }

func (e *myErr) Error() string { return "my/" + e.tag }

func recoverMap(body func()) (err error) {
	defer q.RecoverE(&err).Map(func(r any) error {
		if s, ok := r.(string); ok {
			return &myErr{tag: s}
		}
		return fmt.Errorf("unknown: %v", r)
	})
	body()
	return nil
}

// recoverErr replaces any panic with a constant.
func recoverErr(body func()) (err error) {
	defer q.RecoverE(&err).Err(errors.New("fixed-error"))
	body()
	return nil
}

// recoverWrap prefixes the default PanicError with a message.
func recoverWrap(body func()) (err error) {
	defer q.RecoverE(&err).Wrap("during work")
	body()
	return nil
}

// recoverWrapf uses fmt-style format.
func recoverWrapf(body func(), label string) (err error) {
	defer q.RecoverE(&err).Wrapf("label=%s", label)
	body()
	return nil
}

// recoverErrF accesses the default *PanicError to build a richer
// wrapper.
func recoverErrF(body func()) (err error) {
	defer q.RecoverE(&err).ErrF(func(pe *q.PanicError) error {
		return fmt.Errorf("custom[%v]", pe.Value)
	})
	body()
	return nil
}

func panicWith(v any) func() { return func() { panic(v) } }

func noPanic() {}

func main() {
	// Happy path — no panic, err should be nil.
	err := bareRecover(noPanic)
	fmt.Printf("noPanic.err=%v\n", err)

	// bare Recover + errors.As to extract original panic.
	err = bareRecover(panicWith("boom"))
	var pe *q.PanicError
	fmt.Printf("bare.isPanicErr=%v\n", errors.As(err, &pe))
	if pe != nil {
		fmt.Printf("bare.value=%v\n", pe.Value)
		fmt.Printf("bare.stackNonEmpty=%v\n", len(pe.Stack) > 0)
	}

	// Map — custom translation.
	err = recoverMap(panicWith("tag1"))
	var me *myErr
	fmt.Printf("map.isMyErr=%v tag=%v\n", errors.As(err, &me), me)
	err = recoverMap(panicWith(42))
	fmt.Printf("map.intPanic=%v\n", err)

	// Err — replacement constant.
	err = recoverErr(panicWith("whatever"))
	fmt.Printf("err.replaced=%v\n", err)

	// Wrap — prefixed PanicError.
	err = recoverWrap(panicWith("x"))
	fmt.Printf("wrap.msg=%v\n", err)
	pe = nil
	fmt.Printf("wrap.unwrap=%v\n", errors.As(err, &pe))
	if pe != nil {
		fmt.Printf("wrap.value=%v\n", pe.Value)
	}

	// Wrapf.
	err = recoverWrapf(panicWith("x"), "job-7")
	fmt.Printf("wrapf.msg=%v\n", err)

	// ErrF — custom wrapper over the *PanicError.
	err = recoverErrF(panicWith("z"))
	fmt.Printf("errF.msg=%v\n", err)
}
