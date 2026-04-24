// Fixture: `defer q.Recover()` / `defer q.RecoverE().Method(args)`
// with zero args on the entry. The preprocessor infers the enclosing
// function's error-return name — reusing it if the user supplied one,
// or injecting a named `_qErr` (plus `_qRetN` names for other unnamed
// slots) via a signature rewrite. The body-level rewrite splices
// `&<name>` into the deferred call.
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// withNamedErr: signature already names the error return as `err`.
// The preprocessor should reuse that name — no signature rewrite.
func withNamedErr(trigger bool) (err error) {
	defer q.Recover()
	if trigger {
		panic("named-err")
	}
	return nil
}

// unnamedSingle: single unnamed `error` return. Preprocessor injects
// `_qErr error` into the signature and wires the defer to it.
func unnamedSingle(trigger bool) error {
	defer q.Recover()
	if trigger {
		panic("unnamed-single")
	}
	return nil
}

// unnamedPair: `(int, error)` — both unnamed. Preprocessor injects
// `(_qRet0 int, _qErr error)`. The pre-panic return values come
// through: `return 42, nil` before panic → _qRet0=42, _qErr=nil,
// then the panic fires and the defer overwrites _qErr.
//
// For this fixture we just panic before return so _qRet0 stays 0.
func unnamedPair(trigger bool) (int, error) {
	defer q.Recover()
	if trigger {
		panic("unnamed-pair")
	}
	return 42, nil
}

// recoverEMap: zero-arg RecoverE with a .Map chain method. The
// preprocessor splices `&_qErr` into the entry while leaving .Map's
// argument intact.
func recoverEMap(trigger bool) error {
	defer q.RecoverE().Map(func(r any) error {
		return fmt.Errorf("mapped: %v", r)
	})
	if trigger {
		panic("em-boom")
	}
	return nil
}

// recoverEWrap: chain with Wrap — the default *PanicError gets
// prefixed with the wrap message.
func recoverEWrap(trigger bool) error {
	defer q.RecoverE().Wrap("during work")
	if trigger {
		panic("ew-boom")
	}
	return nil
}

// recoverEErr: replaces the panic with a constant error.
var ErrFixed = errors.New("fixed-replacement")

func recoverEErr(trigger bool) error {
	defer q.RecoverE().Err(ErrFixed)
	if trigger {
		panic("ee-boom")
	}
	return nil
}

// multipleCalls: two auto-Recover deferred calls in one function —
// the signature should only be rewritten once. LIFO order: the
// later-registered defer runs first, so the second q.RecoverE wins
// on the panic path.
func multipleCalls(trigger bool) error {
	defer q.Recover()                    // registered first  → runs second
	defer q.RecoverE().Wrap("inner-wrap") // registered second → runs first
	if trigger {
		panic("multi-boom")
	}
	return nil
}

// explicitStillWorks: the 1-arg form with `&err` continues to work
// exactly as before (runtime helper, no rewriting). Regression
// guard that the auto-mode didn't break the explicit mode.
func explicitStillWorks(trigger bool) (err error) {
	defer q.Recover(&err)
	if trigger {
		panic("explicit-boom")
	}
	return nil
}

func report(name string, _ int, err error) {
	if err == nil {
		fmt.Printf("%s: ok\n", name)
		return
	}
	// Direct type assertion, not errors.As — wrapped errors
	// (q.RecoverE.Wrap / .Wrapf) carry PanicError in their chain
	// but we want the top-level err.Error() text for those.
	if pe, ok := err.(*q.PanicError); ok {
		fmt.Printf("%s: panic=%v\n", name, pe.Value)
		return
	}
	fmt.Printf("%s: err=%s\n", name, err)
}

func main() {
	// Happy paths — no panic, named/unnamed signatures all return nil.
	err := withNamedErr(false)
	report("named.ok", 0, err)

	err = unnamedSingle(false)
	report("single.ok", 0, err)

	n, err := unnamedPair(false)
	fmt.Printf("pair.ok: n=%d err=%v\n", n, err)

	// Panic paths.
	err = withNamedErr(true)
	report("named.panic", 0, err)

	err = unnamedSingle(true)
	report("single.panic", 0, err)

	n, err = unnamedPair(true)
	fmt.Printf("pair.panic: n=%d ", n)
	if pe, ok := err.(*q.PanicError); ok {
		fmt.Printf("panic=%v\n", pe.Value)
	} else {
		fmt.Printf("err=%s\n", err)
	}

	// Chain variants.
	err = recoverEMap(true)
	report("eMap.panic", 0, err)

	err = recoverEWrap(true)
	report("eWrap.panic", 0, err)

	err = recoverEErr(true)
	report("eErr.panic", 0, err)

	// Multiple defers — LIFO.
	err = multipleCalls(true)
	report("multi.panic", 0, err)

	// Explicit form — regression.
	err = explicitStillWorks(true)
	report("explicit.panic", 0, err)
}
