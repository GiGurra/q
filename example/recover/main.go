// example/recover mirrors docs/api/recover.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/recover
package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

type Input struct{ Bad bool }

func process(in Input) {
	if in.Bad {
		panic("boom")
	}
}

// ---------- "What q.Recover does" — auto form ----------
func doWorkAuto(input Input) error {
	defer q.Recover()
	process(input)
	return nil
}

// Explicit form (pure runtime — works without the preprocessor too).
func doWorkExplicit(input Input) (err error) {
	defer q.Recover(&err)
	process(input)
	return nil
}

// ---------- Unwrapping the panic ----------
func describeErr(err error) string {
	if err == nil {
		return "<nil>"
	}
	var pe *q.PanicError
	if errors.As(err, &pe) {
		return fmt.Sprintf("%s [unwrap=%v stack-has-runtime=%v]", err.Error(), pe.Value, strings.Contains(string(pe.Stack), "runtime/"))
	}
	return err.Error()
}

// ---------- Chain methods on q.RecoverE ----------

type BusinessRuleViolation struct{ Detail string }

func (b BusinessRuleViolation) String() string { return b.Detail }

type APIError struct {
	Code   int
	Detail string
}

func (e *APIError) Error() string { return fmt.Sprintf("api %d: %s", e.Code, e.Detail) }

// .Map — full custom translation.
func doWorkMap(input Input) (err error) {
	defer q.RecoverE(&err).Map(func(r any) error {
		if s, ok := r.(BusinessRuleViolation); ok {
			return &APIError{Code: 400, Detail: s.String()}
		}
		return &APIError{Code: 500, Detail: fmt.Sprint(r)}
	})
	process(input)
	return nil
}

func doWorkMapBusiness() (err error) {
	defer q.RecoverE(&err).Map(func(r any) error {
		if s, ok := r.(BusinessRuleViolation); ok {
			return &APIError{Code: 400, Detail: s.String()}
		}
		return &APIError{Code: 500, Detail: fmt.Sprint(r)}
	})
	panic(BusinessRuleViolation{Detail: "must be positive"})
}

// .Err — discard panic value and stack.
var ErrFailed = errors.New("operation failed")

func doWorkErr(input Input) (err error) {
	defer q.RecoverE(&err).Err(ErrFailed)
	process(input)
	return nil
}

// .ErrF — see the wrapper, return a richer error.
func doWorkErrF(input Input) (err error) {
	defer q.RecoverE(&err).ErrF(func(pe *q.PanicError) error {
		return fmt.Errorf("custom wrap: value=%v", pe.Value)
	})
	process(input)
	return nil
}

// .Wrap — prefix the default PanicError.
func doWorkWrap(input Input) (err error) {
	defer q.RecoverE(&err).Wrap("doWorkWrap")
	process(input)
	return nil
}

// .Wrapf — formatted prefix.
func doWorkWrapf(input Input, id int) (err error) {
	defer q.RecoverE(&err).Wrapf("doWorkWrapf id=%d", id)
	process(input)
	return nil
}

// Auto form for q.RecoverE.
func doWorkAutoRecoverE(input Input) error {
	defer q.RecoverE().Map(func(r any) error { return &APIError{Code: 500, Detail: fmt.Sprint(r)} })
	process(input)
	return nil
}

func main() {
	good := Input{Bad: false}
	bad := Input{Bad: true}

	fmt.Printf("doWorkAuto(good): %s\n", describeErr(doWorkAuto(good)))
	fmt.Printf("doWorkAuto(bad): %s\n", describeErr(doWorkAuto(bad)))
	fmt.Printf("doWorkExplicit(bad): %s\n", describeErr(doWorkExplicit(bad)))

	fmt.Printf("doWorkMap(good): %s\n", describeErr(doWorkMap(good)))
	fmt.Printf("doWorkMap(bad): %s\n", describeErr(doWorkMap(bad)))
	fmt.Printf("doWorkMapBusiness(): %s\n", describeErr(doWorkMapBusiness()))

	fmt.Printf("doWorkErr(bad): %s\n", describeErr(doWorkErr(bad)))
	fmt.Printf("doWorkErr(bad).is(ErrFailed): %v\n", errors.Is(doWorkErr(bad), ErrFailed))

	fmt.Printf("doWorkErrF(bad): %s\n", describeErr(doWorkErrF(bad)))
	fmt.Printf("doWorkWrap(bad): %s\n", describeErr(doWorkWrap(bad)))
	fmt.Printf("doWorkWrapf(bad, 7): %s\n", describeErr(doWorkWrapf(bad, 7)))

	fmt.Printf("doWorkAutoRecoverE(bad): %s\n", describeErr(doWorkAutoRecoverE(bad)))
}
