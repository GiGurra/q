// Package q is a question-mark operator for Go, delivered as a -toolexec
// preprocessor. Each q.Try / q.NotNil call (and their chain-style siblings
// q.TryE / q.NotNilE with .Err / .ErrF / .Catch / .Wrap / .Wrapf methods)
// is rewritten at compile time into the conventional `if err != nil {
// return … }` shape — flat call sites, identical generated code to
// hand-written error forwarding, zero runtime overhead.
//
// Build contract:
//
//   - Without `-toolexec=q`, the link step fails on the missing
//     _q_atCompileTime symbol (referenced once at package level via
//     //go:linkname). Forgetting the preprocessor is a loud,
//     deterministic build failure, never a silent runtime divergence.
//
//   - With `-toolexec=q`, every q.* call site is rewritten away before
//     the user's package compiles, so these function bodies do not run
//     in production. If the rewriter ever misses a call (its bug, not
//     the user's), the surviving body panics with a message naming the
//     unrewritten call — loud failure, again, never silent.
//
// IDE story: every function and method below is ordinary Go with a
// real signature. gopls, go vet, and editors see valid code at all
// times.
package q

import (
	"errors"

	_ "unsafe" // for //go:linkname
)

// ErrNil is the sentinel error the bare q.NotNil bubble produces when
// its supplied pointer is nil. Callers can errors.Is against it to
// detect "this came from q.NotNil specifically". Reach for q.NotNilE
// when a richer error is needed.
var ErrNil = errors.New("q: nil value")

// _qLink is the bodyless link-gate symbol. //go:linkname binds it to
// the external _q_atCompileTime symbol that only the q preprocessor's
// toolexec pass supplies (as a no-op companion file appended to
// pkg/q's compile). Without the preprocessor, the link step fails
// with "undefined: _q_atCompileTime".
//
// Referenced exactly once via the package init() below — calling the
// function (vs. taking its value into a blank) survives Go's
// dead-code elimination at every optimisation level. Generic callers
// of q.Try / etc. do not need to reference _qLink themselves; the
// gate is global, not per-function.
//
//go:linkname _qLink _q_atCompileTime
func _qLink()

// init forces link-time resolution of _qLink. The call is a no-op
// once the preprocessor's stub is in place; without the preprocessor,
// the link step fails before init() ever runs.
//
// A package-level `var _ = _qLink` was tried first but Go's compiler
// dead-code-eliminates the blank assignment — the linker then drops
// _qLink as unreferenced and the gate silently disengages. An init()
// that calls the function is the smallest construction that survives
// every optimisation level reliably.
func init() {
	_qLink()
}

// panicUnrewritten is the universal body for every q.* helper. The
// preprocessor rewrites every legitimate call site away, so reaching
// this code path means the rewriter missed one — surface that loudly,
// not silently. The string includes the helper name so the panic
// pinpoints which family escaped the rewrite.
func panicUnrewritten(name string) {
	panic("q: " + name + " call site was not rewritten by the preprocessor")
}

// Try forwards v when err is nil; the preprocessor rewrites the call
// site into the inlined `if err != nil { return zero, err }` shape.
// Use q.TryE for chain-style custom error handling.
//
// Example:
//
//	func loadUser(id int) (User, error) {
//	    row := q.Try(db.Query(id))
//	    user := q.Try(parse(row))
//	    return user, nil
//	}
func Try[T any](v T, err error) T {
	panicUnrewritten("q.Try")
	return v
}

// NotNil forwards p when non-nil; otherwise the preprocessor rewrites
// the call site into the inlined `if p == nil { return zero, q.ErrNil }`
// shape. Reach for q.NotNilE to provide a richer error.
func NotNil[T any](p *T) *T {
	panicUnrewritten("q.NotNil")
	return p
}

// TryE wraps a (T, error) pair into an ErrResult so the caller can
// chain a custom error handler. The full chain — q.TryE(call).Method(…)
// — is rewritten as one expression by the preprocessor.
func TryE[T any](v T, err error) ErrResult[T] {
	panicUnrewritten("q.TryE")
	return ErrResult[T]{}
}

// NotNilE wraps a *T into a NilResult for chain-style nil handling.
// See NotNil for the bare bubble form.
func NotNilE[T any](p *T) NilResult[T] {
	panicUnrewritten("q.NotNilE")
	return NilResult[T]{}
}

// ErrResult carries a captured (value, err) pair for the q.TryE chain.
// The receiver fields are extracted from the source call by the
// preprocessor when emitting the rewritten if-err-then-return shape;
// the struct itself is never materialized in production code.
type ErrResult[T any] struct {
	v   T
	err error
}

// Err bubbles the supplied constant error when the captured err is
// non-nil. The original err is discarded.
func (r ErrResult[T]) Err(replacement error) T {
	panicUnrewritten("q.TryE(...).Err")
	return r.v
}

// ErrF bubbles fn(capturedErr). Use this for type-mapping or
// annotation that needs the original err (e.g. errors.Is / errors.As
// inspection).
func (r ErrResult[T]) ErrF(fn func(error) error) T {
	panicUnrewritten("q.TryE(...).ErrF")
	return r.v
}

// Catch handles a non-nil err via fn, which returns either a recovered
// value (T, nil) — used in place of the bubble — or a new error
// (zero, err) — bubbled in place of the original. The most powerful
// chain method; reach for Err / ErrF / Wrap / Wrapf for the simpler
// shapes.
func (r ErrResult[T]) Catch(fn func(error) (T, error)) T {
	panicUnrewritten("q.TryE(...).Catch")
	return r.v
}

// Wrap is the no-format sugar for fmt.Errorf("<msg>: %w", err) on the
// bubble path. Reach for Wrapf when the message needs format args.
func (r ErrResult[T]) Wrap(msg string) T {
	panicUnrewritten("q.TryE(...).Wrap")
	return r.v
}

// Wrapf is the fmt.Errorf-with-%w sugar. The captured err is appended
// as the final %w arg by the preprocessor; the supplied format need
// not include it.
//
// Example:
//
//	user := q.TryE(loadUser(id)).Wrapf("loading user %d", id)
//	// rewrites to: if err != nil { return zero, fmt.Errorf("loading user %d: %w", id, err) }
func (r ErrResult[T]) Wrapf(format string, args ...any) T {
	panicUnrewritten("q.TryE(...).Wrapf")
	return r.v
}

// NilResult carries a captured *T for the q.NotNilE chain. Methods
// mirror ErrResult's vocabulary; the absence of an incoming err means
// ErrF / Catch take thunks (no error parameter).
type NilResult[T any] struct {
	p *T
}

// Err bubbles the supplied constant error when the captured pointer is
// nil.
func (r NilResult[T]) Err(replacement error) *T {
	panicUnrewritten("q.NotNilE(...).Err")
	return r.p
}

// ErrF computes the bubble error via fn — useful when the error needs
// runtime work to assemble (formatting against captured locals,
// joined errors, etc.).
func (r NilResult[T]) ErrF(fn func() error) *T {
	panicUnrewritten("q.NotNilE(...).ErrF")
	return r.p
}

// Catch handles a nil pointer via fn, which returns either a recovered
// pointer (*T, nil) — used in place of the bubble — or a new error
// (nil, err) — bubbled. Mirrors ErrResult.Catch.
func (r NilResult[T]) Catch(fn func() (*T, error)) *T {
	panicUnrewritten("q.NotNilE(...).Catch")
	return r.p
}

// Wrap bubbles errors.New(msg). There is no source error to %w-wrap on
// the nil branch; the message stands alone.
func (r NilResult[T]) Wrap(msg string) *T {
	panicUnrewritten("q.NotNilE(...).Wrap")
	return r.p
}

// Wrapf bubbles fmt.Errorf(format, args...). No %w is appended — there
// is no source error on the nil branch — so the supplied format is the
// full message.
//
// Example:
//
//	user := q.NotNilE(table[id]).Wrapf("no user %d", id)
//	// rewrites to: if p == nil { return nil, fmt.Errorf("no user %d", id) }
func (r NilResult[T]) Wrapf(format string, args ...any) *T {
	panicUnrewritten("q.NotNilE(...).Wrapf")
	return r.p
}
