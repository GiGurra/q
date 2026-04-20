//go:build qtoolexec

// Standalone unit tests for q.ToErr — the runtime helper that
// adapts a (T, *E)-returning call into (T, error) while collapsing
// a typed-nil *E into a literal nil error.
//
// Unlike every other helper in pkg/q, ToErr is NOT rewritten by the
// preprocessor — its body executes at runtime, so ordinary Go unit
// tests are the natural coverage.
//
// Build tag rationale. pkg/q's link gate (see `_qLink` /
// `_q_atCompileTime`) prevents any test binary for this package
// from linking without -toolexec=q. The `qtoolexec` build tag
// gates this file out of a plain `go test ./...` run — which
// would otherwise fail at link time — while the e2e harness
// opts in via `-tags=qtoolexec -toolexec=<qBin>` from
// TestPackageQUnit in internal/preprocessor.

package q_test

import (
	"errors"
	"testing"

	"github.com/GiGurra/q/pkg/q"
)

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

func TestToErr_NilConcreteCollapsesToNilInterface(t *testing.T) {
	var np *testErr // typed nil — the exact shape ToErr must collapse.
	v, err := q.ToErr(42, np)
	if v != 42 {
		t.Fatalf("value: want 42, got %d", v)
	}
	if err != nil {
		t.Fatalf("err: want nil interface, got non-nil %v (typed-nil leaked)", err)
	}
}

func TestToErr_NonNilConcretePropagates(t *testing.T) {
	e := &testErr{msg: "boom"}
	v, err := q.ToErr(7, e)
	if v != 7 {
		t.Fatalf("value: want 7, got %d", v)
	}
	if err == nil {
		t.Fatal("err: want non-nil")
	}
	if err.Error() != "boom" {
		t.Fatalf("err.Error: want \"boom\", got %q", err.Error())
	}
	// Unwrapping back to the concrete pointer must work — ToErr
	// does not lose identity when forwarding a real error.
	var asTestErr *testErr
	if !errors.As(err, &asTestErr) {
		t.Fatal("errors.As should pick up the concrete *testErr")
	}
	if asTestErr != e {
		t.Fatal("errors.As should return the same pointer instance")
	}
}

func TestToErr_LiteralNilIsNilInterface(t *testing.T) {
	// Caller passes an untyped nil via a function that returns
	// (T, *E); Go type-infers *testErr for the nil.
	fn := func() (int, *testErr) { return 99, nil }
	v, err := q.ToErr(fn())
	if v != 99 {
		t.Fatalf("value: want 99, got %d", v)
	}
	if err != nil {
		t.Fatalf("err: want nil, got %v", err)
	}
}

func TestToErr_PreservesValueOnError(t *testing.T) {
	// Contract: ToErr does not zero the value on error. The Try
	// rewrite may discard it, but the helper itself forwards
	// whatever the callee produced.
	e := &testErr{msg: "partial"}
	v, err := q.ToErr("result", e)
	if v != "result" {
		t.Fatalf("value: want \"result\", got %q", v)
	}
	if err == nil {
		t.Fatal("err: want non-nil")
	}
}
