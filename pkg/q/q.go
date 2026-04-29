// Package q is "Go wild with Q, the funkiest -toolexec preprocessor"
// — a -toolexec preprocessor that implements rejected Go language
// proposals (the ? / try operator) plus a playground of helpers Go
// didn't ship: ctx cancellation checkpoints, futures and fan-in,
// panic→error recovery, mutex sugar, runtime preconditions,
// dbg!-style prints and slog.Attr builders. Each q.Try / q.NotNil
// call (and their chain-style siblings q.TryE / q.NotNilE with
// .Err / .ErrF / .Catch / .Wrap / .Wrapf / .RecoverIs / .RecoverAs
// methods) is rewritten at compile time into the conventional
// `if err != nil { return … }` shape — flat call sites, identical
// generated code to hand-written error forwarding, zero runtime
// overhead.
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
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"runtime/debug"
	"sync"
	"time"

	_ "unsafe" // for //go:linkname
)

// DebugWriter is the destination q.DebugPrintlnAt writes to. Defaults
// to os.Stderr; tests and library users can reassign it to capture
// q.DebugPrintln output for assertions.
var DebugWriter io.Writer = os.Stderr

// ErrNil is the sentinel error the bare q.NotNil bubble produces when
// its supplied pointer is nil. Callers can errors.Is against it to
// detect "this came from q.NotNil specifically". Reach for q.NotNilE
// when a richer error is needed.
var ErrNil = errors.New("q: nil value")

// ErrNotOk is the sentinel error the bare q.Ok bubble produces when
// its supplied ok flag is false. Mirrors ErrNil's role for the
// comma-ok family (map lookups, type assertions, channel receives).
// Reach for q.OkE when a richer error is needed.
var ErrNotOk = errors.New("q: not ok")

// ErrChanClosed is the sentinel error the bare q.Recv bubble
// produces when the channel is closed (i.e. the receive's ok flag
// is false). Use q.RecvE to supply a richer error.
var ErrChanClosed = errors.New("q: channel closed")

// ErrBadTypeAssert is the sentinel error the bare q.As bubble produces
// when the type assertion's ok flag is false. Use q.AsE to supply a
// richer error.
var ErrBadTypeAssert = errors.New("q: type assertion failed")

// ErrRequireFailed is wrapped (via %w) into the bubble produced by
// q.Require when its condition is false. Callers can errors.Is the
// resulting error against this sentinel to detect "this came from a
// q.Require call". The wrapping fmt.Errorf prefixes the call-site
// file:line and any user-supplied message before the sentinel.
var ErrRequireFailed = errors.New("q.Require failed")

// Unwrap takes a (T, error) pair and panics with the error when non-
// nil; otherwise returns v. Plain runtime function — NOT rewritten
// by the preprocessor.
//
// q.Try is the right tool inside functions returning error: it
// rewrites to `if err != nil { return zero, err }` and bubbles
// cleanly. q.Try cannot bubble from main(), init(), test
// helpers, or any other function without an error return slot —
// q.Unwrap fills that gap with a panic-on-error escape hatch.
//
// Use q.Unwrap when:
//   - The call site has no error return path (main, init, fixtures).
//   - You're asserting that a particular call cannot fail (config
//     loaded once at startup, regexp compiled from a literal,
//     q.Assemble of a graph proven correct at build time).
//
// Avoid q.Unwrap in production library code where the caller might
// reasonably want to handle the error — that's q.Try territory.
//
// Example:
//
//	func main() {
//	    server := q.Unwrap(q.Assemble[*Server](newConfig, newDB, newServer))
//	    server.Run()
//	}
func Unwrap[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// UnwrapE is the chain variant of Unwrap. The chain methods shape
// the err before panicking (.Wrap / .Wrapf / .Err / .ErrF) or
// recover with a fallback value (.Catch). All methods are plain
// runtime — UnwrapE is NOT rewritten by the preprocessor.
//
// Use it for the same contexts as Unwrap (main, init, tests) when
// you want richer error context on the panic, a wrapped sentinel
// for errors.Is detection, or a recovery path:
//
//	func main() {
//	    server := q.UnwrapE(q.Assemble[*Server](newConfig, newDB, newServer)).Wrap("server init")
//	    server.Run()
//	}
//
//	// .Catch lets the caller recover instead of panicking.
//	cfg := q.UnwrapE(loadConfig()).Catch(func(error) (*Config, error) {
//	    return defaultConfig(), nil
//	})
func UnwrapE[T any](v T, err error) UnwrapResult[T] {
	return UnwrapResult[T]{v: v, err: err}
}

// UnwrapResult carries a captured (T, error) pair for the q.UnwrapE
// chain. Methods either return v (when err is nil) or panic with the
// shaped error.
type UnwrapResult[T any] struct {
	v   T
	err error
}

// Err panics with the supplied replacement error when the captured
// err is non-nil; otherwise returns v.
func (r UnwrapResult[T]) Err(replacement error) T {
	if r.err != nil {
		panic(replacement)
	}
	return r.v
}

// ErrF panics with fn(capturedErr) when err is non-nil; otherwise
// returns v.
func (r UnwrapResult[T]) ErrF(fn func(error) error) T {
	if r.err != nil {
		panic(fn(r.err))
	}
	return r.v
}

// Wrap panics with fmt.Errorf("<msg>: %w", err) when err is non-nil;
// otherwise returns v.
func (r UnwrapResult[T]) Wrap(msg string) T {
	if r.err != nil {
		panic(fmt.Errorf("%s: %w", msg, r.err))
	}
	return r.v
}

// Wrapf panics with fmt.Errorf(format + ": %w", args..., err) when
// err is non-nil; otherwise returns v.
func (r UnwrapResult[T]) Wrapf(format string, args ...any) T {
	if r.err != nil {
		panic(fmt.Errorf(format+": %w", append(args, r.err)...))
	}
	return r.v
}

// Catch passes the captured err through fn when non-nil. fn returns
// either a recovered (T, nil) — used in place of the panic — or
// (zero, newErr) — newErr panics in place of the original.
func (r UnwrapResult[T]) Catch(fn func(error) (T, error)) T {
	if r.err != nil {
		v, err := fn(r.err)
		if err != nil {
			panic(err)
		}
		return v
	}
	return r.v
}

// Const builds a Catch handler that always recovers to the supplied
// value, ignoring the captured error. Pure runtime helper — not
// rewritten by the preprocessor. Useful as the fallback in any chain
// method that takes a func(error) (T, error):
//
//	n := q.TryE(strconv.Atoi(s)).Catch(q.Const(0))
//	conn := q.OpenE(dial(addr)).Catch(q.Const(fallbackConn)).DeferCleanup((*Conn).Close)
//
// q.Const fits the err-taking Catch shape used by ErrResult /
// OpenResultE / TraceResult / AwaitE chains. The no-arg-Catch shapes
// on q.NotNilE / q.OkE require a different signature; for those write
// the closure inline.
func Const[T any](v T) func(error) (T, error) {
	return func(error) (T, error) { return v, nil }
}

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
	v T
	// err carries the captured error that the chain method's
	// rewritten replacement bubbles. It's never read by the
	// run-time method bodies (which panic if reached) — the
	// rewriter erases every call site before the binary runs —
	// so the linter would otherwise flag it as unused.
	err error //nolint:unused // documented as part of the chain contract
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

// RecoverIs is a chain-continuing recovery: when the captured err
// matches sentinel via errors.Is, the chain's value becomes value
// and the err is cleared. Otherwise the err passes through to the
// next chain step. The intermediate result is still ErrResult[T],
// so RecoverIs MUST be followed by a terminal method (Err, ErrF,
// Catch, Wrap, Wrapf) — using it as the chain's last step is a
// build-time error from the preprocessor (the bubble would be
// silently swallowed otherwise).
//
// Example:
//
//	n := q.TryE(strconv.Atoi(s)).
//	    RecoverIs(strconv.ErrSyntax, 0).
//	    Wrapf("parsing %q", s)
//	// Returns 0 if s isn't a valid syntax; bubbles the wrapped err otherwise.
//
// Multiple RecoverIs / RecoverAs steps may be chained in source
// order; each runs its check only if no earlier step has already
// recovered.
func (r ErrResult[T]) RecoverIs(sentinel error, value T) ErrResult[T] {
	panicUnrewritten("q.TryE(...).RecoverIs")
	return r
}

// RecoverAs is the errors.As-flavoured chain-continuing recovery.
// When the captured err can be extracted into the type carried by
// typedNil (a typed-nil literal such as `(*MyErr)(nil)`), the
// chain's value becomes value and the err is cleared. The
// preprocessor extracts the target type syntactically from the
// typedNil arg at compile time, so it must be a typed-nil
// expression (e.g. `(*strconv.NumError)(nil)`); arbitrary error
// values are rejected with a diagnostic.
//
// Like RecoverIs, RecoverAs must be followed by a terminal method.
//
// Example:
//
//	n := q.TryE(strconv.Atoi(s)).
//	    RecoverAs((*strconv.NumError)(nil), -1).
//	    Wrapf("parsing %q", s)
func (r ErrResult[T]) RecoverAs(typedNil error, value T) ErrResult[T] {
	panicUnrewritten("q.TryE(...).RecoverAs")
	return r
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

// Check bubbles err when non-nil. Use it in positions where the call
// being guarded returns only an error (file.Close, db.Ping,
// validate(...)).  Reach for q.CheckE for chain-style custom error
// handling.
//
// q.Check is always an expression statement — it returns nothing, so
// `v := q.Check(...)` and similar are rejected by the Go compiler.
//
// Example:
//
//	func shutdown(conn *Conn) error {
//	    q.Check(conn.Close())
//	    q.Check(db.Ping())
//	    return nil
//	}
func Check(err error) {
	panicUnrewritten("q.Check")
}

// CheckE starts an error-only chain; see ErrResult for the vocabulary.
// The methods here return void — there is no value to thread through,
// only an error to shape.
func CheckE(err error) CheckResult {
	panicUnrewritten("q.CheckE")
	return CheckResult{}
}

// CheckResult carries a captured error for the q.CheckE chain. Like
// ErrResult.err, CheckResult.err is documented but never read at run
// time — the rewriter splices the captured error into the inlined
// bubble.
type CheckResult struct {
	err error //nolint:unused // documented as part of the chain contract
}

// Err bubbles the supplied constant error when the captured err is
// non-nil.
func (r CheckResult) Err(replacement error) {
	panicUnrewritten("q.CheckE(...).Err")
}

// ErrF bubbles fn(capturedErr).
func (r CheckResult) ErrF(fn func(error) error) {
	panicUnrewritten("q.CheckE(...).ErrF")
}

// Wrap bubbles fmt.Errorf("<msg>: %w", err).
func (r CheckResult) Wrap(msg string) {
	panicUnrewritten("q.CheckE(...).Wrap")
}

// Wrapf bubbles fmt.Errorf(format + ": %w", args..., err).
func (r CheckResult) Wrapf(format string, args ...any) {
	panicUnrewritten("q.CheckE(...).Wrapf")
}

// Catch transforms the captured error via fn. Returning nil suppresses
// the bubble (continue past the CheckE call); returning non-nil bubbles
// that error in place of the original. There is no value to recover,
// unlike ErrResult.Catch.
func (r CheckResult) Catch(fn func(error) error) {
	panicUnrewritten("q.CheckE(...).Catch")
}

// Open begins a resource acquisition chain: pass in a (T, error)-
// returning call, then chain `.DeferCleanup(cleanup)` to bubble on error
// and register `defer cleanup(resource)` on success in the enclosing
// function. Reach for q.OpenE for chain-style custom error handling
// around the bubble.
//
// Example:
//
//	conn := q.Open(dial(addr)).DeferCleanup((*Conn).Close)
//	// equivalent, post-rewrite, to:
//	//   conn, err := dial(addr)
//	//   if err != nil { return zero, err }
//	//   defer conn.Close()
func Open[T any](v T, err error) OpenResult[T] {
	panicUnrewritten("q.Open")
	return OpenResult[T]{}
}

// OpenE is Open with chainable error-shape methods. Shape methods
// (Err/ErrF/Wrap/Wrapf/Catch) return OpenResultE[T] so `.DeferCleanup` can
// still follow as the terminal. DeferCleanup is the only member that
// returns T; everything else in the chain is a pass-through modifier
// on the bubbled error.
//
// Example:
//
//	conn := q.OpenE(dial(addr)).Wrap("dialing").DeferCleanup((*Conn).Close)
func OpenE[T any](v T, err error) OpenResultE[T] {
	panicUnrewritten("q.OpenE")
	return OpenResultE[T]{}
}

// OpenResult is the plain Open handle — it exposes only .DeferCleanup
// (and .NoDeferCleanup) so IDE completion stays focused on the common
// case.
type OpenResult[T any] struct {
	v   T
	err error //nolint:unused // documented as part of the chain contract
}

// DeferCleanup bubbles err on failure; registers `defer cleanup(v)` in
// the enclosing function and returns v on success.
//
// Two forms:
//
//   - DeferCleanup(cleanup) — explicit cleanup. Two accepted shapes:
//       func(T)         → defer cleanup(v)
//       func(T) error   → defer wrapper that slog.Errors the close-time err
//     The cleanup MUST take the resource — q.Open's DeferCleanup is
//     scoped to the resource it wraps. For cleanups that don't need
//     the resource, write `defer myCleanup()` at the call site. The
//     preprocessor validates the argument at compile time; any other
//     shape is a build error. Wrap the cleanup yourself for different
//     handling on the close-time error — suppress, retry, or transform.
//   - DeferCleanup()        — no args; the preprocessor infers the
//     cleanup from T's type at compile time. Supported shapes:
//     bidirectional and send-only channels (rewrites to
//     `defer close(v)` — recv-only channels are rejected since the
//     consumer doesn't own close), types with a `Close() error`
//     method (rewrites to a deferred wrapper that logs the error via
//     `slog.Error`), and types with a `Close()` method (rewrites to
//     `defer v.Close()`). Any other T is a build error — pass an
//     explicit cleanup or use .NoDeferCleanup() to opt out.
//
// The cleanup parameter is `...any` so the source compiles whether
// the caller hands in `func(T)` or `func(T) error`; the preprocessor
// rejects anything else with a typed diagnostic. Calls with two-or-
// more args are also rejected by the preprocessor.
func (r OpenResult[T]) DeferCleanup(cleanup ...any) T {
	panicUnrewritten("q.Open(...).DeferCleanup")
	return r.v
}

// NoDeferCleanup bubbles err on failure and returns v on success
// without registering any deferred cleanup. Use it to make the
// "no cleanup needed" intent explicit at the call site, instead of
// passing a do-nothing function to .DeferCleanup. The bubble path is
// identical to .DeferCleanup's; only the success path differs.
func (r OpenResult[T]) NoDeferCleanup() T {
	panicUnrewritten("q.Open(...).NoDeferCleanup")
	return r.v
}

// WithScope bubbles err on failure; on success attaches the resource
// to scope so the scope owns its lifetime — when the scope closes,
// the cleanup fires.
//
// Two call shapes:
//
//   - WithScope(scope)            — auto-detect cleanup from T (same
//     shapes DeferCleanup() infers: bidirectional/send-only chan,
//     Close(), Close() error).
//   - WithScope(cleanup, scope)   — explicit cleanup (func(T) or
//     func(T) error).
//
// If the scope is already closed when the attach fires, the cleanup
// runs eagerly and q.ErrScopeClosed is bubbled — different from
// DeferCleanup which always succeeds. The `args` parameter is `...any`
// so the source compiles for either shape; the preprocessor enforces
// the (cleanup?, scope) ordering at build time.
func (r OpenResult[T]) WithScope(args ...any) T {
	panicUnrewritten("q.Open(...).WithScope")
	return r.v
}

// OpenResultE is the chain-capable Open handle. Shape methods return
// OpenResultE[T] so DeferCleanup can terminate the chain; DeferCleanup
// itself returns T.
type OpenResultE[T any] struct {
	v   T
	err error //nolint:unused // documented as part of the chain contract
}

// Err replaces the captured error with a constant.
func (r OpenResultE[T]) Err(replacement error) OpenResultE[T] {
	panicUnrewritten("q.OpenE(...).Err")
	return r
}

// ErrF transforms the captured error via fn(err) error.
func (r OpenResultE[T]) ErrF(fn func(error) error) OpenResultE[T] {
	panicUnrewritten("q.OpenE(...).ErrF")
	return r
}

// Wrap bubbles fmt.Errorf("<msg>: %w", err).
func (r OpenResultE[T]) Wrap(msg string) OpenResultE[T] {
	panicUnrewritten("q.OpenE(...).Wrap")
	return r
}

// Wrapf bubbles fmt.Errorf(format + ": %w", args..., err).
func (r OpenResultE[T]) Wrapf(format string, args ...any) OpenResultE[T] {
	panicUnrewritten("q.OpenE(...).Wrapf")
	return r
}

// Catch recovers or transforms: fn(err) returns (T, nil) to recover
// with a value (replaces the bubble, the recovered T feeds DeferCleanup)
// or (zero, newErr) to bubble newErr instead of the original.
func (r OpenResultE[T]) Catch(fn func(error) (T, error)) OpenResultE[T] {
	panicUnrewritten("q.OpenE(...).Catch")
	return r
}

// DeferCleanup bubbles the shaped error on failure; registers
// `defer cleanup(v)` in the enclosing function and returns v on
// success. Same explicit-cleanup shapes (`func(T)` or `func(T) error`)
// and same auto-cleanup inference as q.Open.DeferCleanup — see that
// doc for the supported T shapes.
func (r OpenResultE[T]) DeferCleanup(cleanup ...any) T {
	panicUnrewritten("q.OpenE(...).DeferCleanup")
	return r.v
}

// NoDeferCleanup bubbles the shaped error on failure and returns v
// on success without registering any deferred cleanup. Same
// semantics as q.OpenResult.NoDeferCleanup but composes with the
// shape methods (Wrap/Wrapf/Err/ErrF/Catch) on q.OpenE.
func (r OpenResultE[T]) NoDeferCleanup() T {
	panicUnrewritten("q.OpenE(...).NoDeferCleanup")
	return r.v
}

// WithScope bubbles the shaped error on failure; on success attaches
// the resource to scope. Same semantics and call shapes as
// q.OpenResult.WithScope, composed with the shape methods (Wrap /
// Wrapf / Err / ErrF / Catch).
func (r OpenResultE[T]) WithScope(args ...any) T {
	panicUnrewritten("q.OpenE(...).WithScope")
	return r.v
}

// PanicError wraps a recovered panic value and the stack captured
// at recovery time. Produced by q.Recover and by the default
// q.RecoverE path when no user-supplied mapper is provided. Callers
// recover the original panic data with:
//
//	var pe *q.PanicError
//	if errors.As(err, &pe) { fmt.Println(pe.Value, string(pe.Stack)) }
type PanicError struct {
	Value any
	Stack []byte
}

// Error renders the panic value and a short stack hint. The full
// stack is available on the Stack field.
func (e *PanicError) Error() string {
	return fmt.Sprintf("q: recovered panic: %v", e.Value)
}

// Recover is the runtime helper paired with `defer q.Recover(&err)`.
// When a panic is in flight at defer-time, the recovered value and
// a debug.Stack() snapshot are wrapped in *PanicError and stored
// via errPtr. Plain runtime function — Go's recover() sees the
// panic because Recover IS the deferred function. Calling Recover
// without defer is a no-op.
//
// Two call shapes are accepted:
//
//   - `defer q.Recover(&err)` — explicit form, pure runtime.
//   - `defer q.Recover()`     — zero-arg auto form. The preprocessor
//     rewrites the call to pass `&err` from the enclosing function's
//     error-slot automatically, and — when that slot is unnamed —
//     injects a named return on the signature. The enclosing
//     function must have the built-in `error` as its last return.
//
// Example (auto form):
//
//	func doWork() error {      // becomes `(_qErr error)` post-rewrite
//	    defer q.Recover()
//	    riskyPanics()
//	    return nil
//	}
func Recover(errPtr ...*error) {
	r := recover()
	if r == nil {
		return
	}
	if len(errPtr) == 0 {
		panicUnrewritten("q.Recover")
	}
	*errPtr[0] = &PanicError{Value: r, Stack: debug.Stack()}
}

// RecoverE begins a RecoverE chain. The chain method decides how
// the recovered panic (if any) maps to the error stored via
// errPtr. Like Recover, the chain's terminal method IS the
// deferred function, so recover() catches the panic correctly.
//
// Two call shapes mirror Recover:
//
//   - `defer q.RecoverE(&err).<Method>(args)` — explicit, pure runtime.
//   - `defer q.RecoverE().<Method>(args)`     — zero-arg auto form.
//     The preprocessor rewrites the call to inject `&err` and names
//     the signature's error return when necessary.
//
// Example (auto form):
//
//	func doWork() error {
//	    defer q.RecoverE().Map(func(r any) error { return &MyErr{Cause: r} })
//	    riskyPanics()
//	    return nil
//	}
func RecoverE(errPtr ...*error) RecoverResult {
	if len(errPtr) == 0 {
		return RecoverResult{}
	}
	return RecoverResult{errPtr: errPtr[0]}
}

// RecoverResult carries the errPtr through the q.RecoverE chain.
// The terminal method (Map, Err, ErrF, Wrap, Wrapf) is the actual
// deferred function.
type RecoverResult struct {
	errPtr *error
}

// Map runs fn on the recovered panic value; the returned error is
// stored via errPtr. Use for custom panic-shape translation.
func (r RecoverResult) Map(fn func(any) error) {
	rec := recover()
	if rec == nil {
		return
	}
	if r.errPtr == nil {
		panicUnrewritten("q.RecoverE(...).Map")
	}
	*r.errPtr = fn(rec)
}

// Err stores the supplied replacement error on panic, discarding
// the original panic value and stack.
func (r RecoverResult) Err(replacement error) {
	rec := recover()
	if rec == nil {
		return
	}
	if r.errPtr == nil {
		panicUnrewritten("q.RecoverE(...).Err")
	}
	*r.errPtr = replacement
}

// ErrF transforms the default *PanicError wrapper via fn before
// storing. Useful when the caller wants to prepend context but
// still preserve the original panic metadata.
func (r RecoverResult) ErrF(fn func(*PanicError) error) {
	rec := recover()
	if rec == nil {
		return
	}
	if r.errPtr == nil {
		panicUnrewritten("q.RecoverE(...).ErrF")
	}
	*r.errPtr = fn(&PanicError{Value: rec, Stack: debug.Stack()})
}

// Wrap prefixes the default PanicError with msg via fmt.Errorf.
func (r RecoverResult) Wrap(msg string) {
	rec := recover()
	if rec == nil {
		return
	}
	if r.errPtr == nil {
		panicUnrewritten("q.RecoverE(...).Wrap")
	}
	*r.errPtr = fmt.Errorf("%s: %w", msg, &PanicError{Value: rec, Stack: debug.Stack()})
}

// Wrapf prefixes the default PanicError with a formatted message.
func (r RecoverResult) Wrapf(format string, args ...any) {
	rec := recover()
	if rec == nil {
		return
	}
	if r.errPtr == nil {
		panicUnrewritten("q.RecoverE(...).Wrapf")
	}
	*r.errPtr = fmt.Errorf(format+": %w", append(args, &PanicError{Value: rec, Stack: debug.Stack()})...)
}

// Future is the promise-like handle returned by q.Async. Internal
// state: a buffered channel the spawned goroutine sends the result
// on, consumed at-most-once by q.Await / q.AwaitRaw.
type Future[T any] struct {
	done chan futureResult[T]
}

type futureResult[T any] struct {
	v   T
	err error
}

// Async spawns fn in a goroutine and returns a Future that q.Await
// or q.AwaitRaw can pull the result from. Plain runtime function —
// not rewritten by the preprocessor. Usable standalone.
func Async[T any](fn func() (T, error)) Future[T] {
	f := Future[T]{done: make(chan futureResult[T], 1)}
	go func() {
		v, err := fn()
		f.done <- futureResult[T]{v: v, err: err}
	}()
	return f
}

// AwaitRaw blocks on the Future and returns its (T, error) result.
// Plain runtime function (not rewritten). The preprocessor rewrites
// q.Await / q.AwaitE internally into calls to AwaitRaw — user code
// may also call it directly when they want the raw tuple.
func AwaitRaw[T any](f Future[T]) (T, error) {
	r := <-f.done
	return r.v, r.err
}

// Await blocks on the Future and forwards the value; the
// preprocessor rewrites the call site into the inlined `v, err :=
// q.AwaitRaw(f); if err != nil { return zero, err }` shape. Reach
// for q.AwaitE for chain-style custom error handling on the
// await's bubble.
func Await[T any](f Future[T]) T {
	panicUnrewritten("q.Await")
	var zero T
	return zero
}

// AwaitE is the chain variant of Await; reuses ErrResult[T] so the
// chain vocabulary is identical to TryE.
func AwaitE[T any](f Future[T]) ErrResult[T] {
	panicUnrewritten("q.AwaitE")
	return ErrResult[T]{}
}

// DebugPrintln prints v to q.DebugWriter (defaults to os.Stderr)
// prefixed with the call-site file:line and the source text of the
// argument expression, then returns v unchanged so the call can sit
// mid-expression. Go's missing `dbg!` / `println!`. Usable anywhere
// a value expression is valid:
//
//	return q.DebugPrintln(loadUser(q.DebugPrintln(id)))
//
// Both prints fire in source order, then the return flows through.
// Only the preprocessor knows the source text and file:line —
// without it, q.DebugPrintln is a panic stub. Every rewritten site
// calls q.DebugPrintlnAt internally, which is also exported for
// direct use when a custom label is wanted.
func DebugPrintln[T any](v T) T {
	panicUnrewritten("q.DebugPrintln")
	return v
}

// DebugPrintlnAt is the runtime half of q.DebugPrintln. The
// preprocessor rewrites every `q.DebugPrintln(x)` call site into
// `q.DebugPrintlnAt("<file>:<line> <src>", x)`, but users who want a
// custom label (or who need to construct the label at runtime) can
// call DebugPrintlnAt directly without going through the
// preprocessor path.
func DebugPrintlnAt[T any](label string, v T) T {
	_, _ = fmt.Fprintf(DebugWriter, "%s = %+v\n", label, v)
	return v
}

// DebugSlogAttr returns a slog.Attr keyed by the call-site
// `<file>:<line> <src>` label captured at compile time, with v as
// the value. Use it to attach a labelled value to a structured-log
// call without retyping the source expression as a key:
//
//	slog.Info("loaded", q.DebugSlogAttr(userID))
//	// → slog.Info("loaded", slog.Any("main.go:42 userID", userID))
//
// The preprocessor rewrites every q.DebugSlogAttr call site into
// the equivalent slog.Any expression at compile time. There is no
// runtime helper for this one — the rewrite expands directly to
// stdlib slog, so the value path is purely a stdlib slog call.
//
// Unlike q.DebugPrintln, q.DebugSlogAttr does not pass v through:
// it returns a slog.Attr suitable for the variadic args of slog.Info
// / slog.Error / etc. For mid-expression instrumentation reach for
// q.DebugPrintln.
func DebugSlogAttr[T any](v T) slog.Attr {
	panicUnrewritten("q.DebugSlogAttr")
	return slog.Attr{}
}

// SlogAttr returns a slog.Attr keyed by the source text of v
// (captured at compile time), with v as the value. Use it to
// attach a labelled value to a structured-log call without
// retyping the variable name as the slog key:
//
//	slog.Info("loaded", q.SlogAttr(userID))
//	// → slog.Info("loaded", slog.Any("userID", userID))
//
// Unlike q.DebugSlogAttr, q.SlogAttr does NOT include the call-site
// file:line in the key — it's the production-grade slog helper for
// attaching named values, and a clean key matches typical slog
// output expectations. Pair with q.SlogFile / q.SlogLine when you
// want location info as separate attrs.
//
// The preprocessor rewrites every q.SlogAttr call site directly to
// slog.Any at compile time; no q runtime helper sits on the value
// path. The log/slog import is auto-injected when this family
// appears.
func SlogAttr[T any](v T) slog.Attr {
	panicUnrewritten("q.SlogAttr")
	return slog.Attr{}
}

// SlogFile returns a slog.Attr with key "file" and the basename of
// the call site's source file as value (e.g. "main.go"). Captured
// at compile time. Pair with q.SlogLine for "log this with where
// it was emitted" without writing the location info by hand:
//
//	slog.Info("processed", q.SlogFile(), q.SlogLine())
//	// → slog.Info("processed", slog.Any("file", "main.go"), slog.Any("line", 42))
//
// Production-friendly counterpart to runtime.Caller / debug.Stack:
// the location is constant per call site, evaluated once at
// compile time, and shows up as ordinary slog attrs in your log
// output.
func SlogFile() slog.Attr {
	panicUnrewritten("q.SlogFile")
	return slog.Attr{}
}

// SlogLine returns a slog.Attr with key "line" and the integer line
// number of the call site as value. See q.SlogFile.
func SlogLine() slog.Attr {
	panicUnrewritten("q.SlogLine")
	return slog.Attr{}
}

// SlogFileLine returns a slog.Attr with key "file" and a value of
// the form "<basename>:<line>" — the same compile-time capture as
// q.SlogFile + q.SlogLine combined into one attr. Use it when you
// want a single, parseable location string per log record:
//
//	slog.Info("event", q.SlogFileLine())
//	// → slog.Info("event", slog.Any("file", "main.go:42"))
func SlogFileLine() slog.Attr {
	panicUnrewritten("q.SlogFileLine")
	return slog.Attr{}
}

// File returns the basename of the call-site source file as a
// plain string (e.g. "main.go"). Captured at compile time. Use
// this when you want the location info as a primitive value
// rather than a slog.Attr.
func File() string {
	panicUnrewritten("q.File")
	return ""
}

// Line returns the integer line number of the call site as a
// plain int. Captured at compile time.
func Line() int {
	panicUnrewritten("q.Line")
	return 0
}

// FileLine returns "<basename>:<line>" as a plain string, e.g.
// "main.go:42". Captured at compile time.
func FileLine() string {
	panicUnrewritten("q.FileLine")
	return ""
}

// Expr returns the literal source text of its argument as a
// string, captured at compile time. The argument is type-checked
// (so it must be valid Go) but its runtime value is discarded:
//
//	q.Expr(a + b)         // → "a + b"
//	q.Expr(user.Email)    // → "user.Email"
//	q.Expr(items[i*2])    // → "items[i*2]"
//
// Useful for self-documenting error messages or labels that
// reflect the exact source spelling of an expression. The type
// parameter is `any`, so any expression form is accepted.
func Expr[T any](v T) string {
	panicUnrewritten("q.Expr")
	return ""
}

// Recv receives from ch and forwards the value; the preprocessor
// rewrites the call site into the inlined `v, _ok := <-ch; if !_ok
// { return zero, q.ErrChanClosed }` shape. Use q.RecvE to supply a
// richer error.
func Recv[T any](ch <-chan T) T {
	panicUnrewritten("q.Recv")
	var zero T
	return zero
}

// RecvE wraps a channel receive into an OkResult (shared with Ok),
// so the full OkE chain vocabulary shapes the bubble when the
// channel is closed.
func RecvE[T any](ch <-chan T) OkResult[T] {
	panicUnrewritten("q.RecvE")
	return OkResult[T]{}
}

// As asserts x holds a T and forwards it; the preprocessor rewrites
// the call site into the inlined `v, _ok := x.(T); if !_ok { return
// zero, q.ErrBadTypeAssert }` shape. Use q.AsE to supply a richer error.
func As[T any](x any) T {
	panicUnrewritten("q.As")
	var zero T
	return zero
}

// AsE wraps a type assertion into an OkResult so the OkE chain
// vocabulary shapes the bubble when the assertion fails.
func AsE[T any](x any) OkResult[T] {
	panicUnrewritten("q.AsE")
	return OkResult[T]{}
}

// Lock acquires l and registers a deferred Unlock in the enclosing
// function. Always an expression statement — returns nothing.
// Accepts any sync.Locker (*sync.Mutex, *sync.RWMutex for the write
// side, rwm.RLocker() for the read side, user-defined types).
//
// Example:
//
//	func (s *store) Set(k, v string) {
//	    q.Lock(&s.mu)
//	    s.data[k] = v
//	}
func Lock(l sync.Locker) {
	panicUnrewritten("q.Lock")
}

// TODO panics with "q.TODO <file>:<line>[: <msg>]" to mark an
// unfinished branch. Always an expression statement. Reach for
// q.Unreachable for code paths the author believes cannot execute.
func TODO(msg ...string) {
	panicUnrewritten("q.TODO")
}

// Unreachable panics with "q.Unreachable <file>:<line>[: <msg>]" to
// mark code paths the author believes cannot execute. Always an
// expression statement.
func Unreachable(msg ...string) {
	panicUnrewritten("q.Unreachable")
}

// Require returns when cond is true; otherwise the preprocessor
// rewrites the call site to bubble an error of the form
//
//	errors.New("q.Require failed <file>:<line>[: <msg>]")
//
// to the enclosing function's error return. Always an expression
// statement. Use for runtime preconditions where reporting via
// error is preferable to crashing the process — q's stance is that
// the library's job is returning errors, not generating panics.
func Require(cond bool, msg ...string) {
	panicUnrewritten("q.Require")
}

// Trace forwards v when err is nil; otherwise the preprocessor
// rewrites the call site to bubble an error prefixed with the call
// site's file:line captured at compile time. Plain Go can't express
// this — runtime code has no access to its own source location
// without a stack walk. Reach for q.TraceE when the prefix needs to
// compose with the standard error-shape vocabulary.
//
// Example:
//
//	func loadUser(id int) (User, error) {
//	    row := q.Trace(db.Query(id)) // bubble prefixed with "users.go:42: "
//	    ...
//	}
func Trace[T any](v T, err error) T {
	panicUnrewritten("q.Trace")
	return v
}

// TraceE is the chain variant of Trace. Each shape method composes
// over the location prefix — `q.TraceE(call).Wrap("ctx")` bubbles
// `"file.go:42: ctx: <inner>"`. Mirrors TryE's vocabulary.
func TraceE[T any](v T, err error) TraceResult[T] {
	panicUnrewritten("q.TraceE")
	return TraceResult[T]{}
}

// TraceResult carries a captured (value, err) pair for q.TraceE.
// Every method bubbles with a call-site `file:line` prefix injected
// by the preprocessor.
type TraceResult[T any] struct {
	v   T
	err error //nolint:unused // documented as part of the chain contract
}

// Err bubbles the supplied replacement error, still prefixed with
// the call-site file:line.
func (r TraceResult[T]) Err(replacement error) T {
	panicUnrewritten("q.TraceE(...).Err")
	return r.v
}

// ErrF bubbles fn(capturedErr), prefixed with the call-site
// file:line.
func (r TraceResult[T]) ErrF(fn func(error) error) T {
	panicUnrewritten("q.TraceE(...).ErrF")
	return r.v
}

// Catch recovers or transforms; the returned error (if any) is
// still prefixed with the call-site file:line.
func (r TraceResult[T]) Catch(fn func(error) (T, error)) T {
	panicUnrewritten("q.TraceE(...).Catch")
	return r.v
}

// Wrap is fmt.Errorf("<file>:<line>: <msg>: %w", err) sugar.
func (r TraceResult[T]) Wrap(msg string) T {
	panicUnrewritten("q.TraceE(...).Wrap")
	return r.v
}

// Wrapf is fmt.Errorf("<file>:<line>: <format>: %w", args..., err)
// sugar.
func (r TraceResult[T]) Wrapf(format string, args ...any) T {
	panicUnrewritten("q.TraceE(...).Wrapf")
	return r.v
}

// Ok forwards v when ok is true; the preprocessor rewrites the call
// site into the inlined `if !ok { return zero, q.ErrNotOk }` shape.
// Use Ok for comma-ok patterns: map lookups, type assertions, channel
// receives. Reach for q.OkE to provide a richer error.
//
// Example:
//
//	func findUser(id int) (User, error) {
//	    user := q.Ok(users[id])         // map lookup (v, ok)
//	    admin := q.Ok(user.(Admin))     // type assertion (v, ok)
//	    return admin, nil
//	}
func Ok[T any](v T, ok bool) T {
	panicUnrewritten("q.Ok")
	return v
}

// OkE wraps a (T, bool) pair into an OkResult so the caller can chain
// a custom error handler. The full chain — q.OkE(call).Method(…) —
// is rewritten as one expression by the preprocessor. Mirrors
// NotNilE's vocabulary: there is no source error on the false-ok
// branch, so ErrF takes a thunk and Wrap/Wrapf build the error from
// scratch (errors.New / fmt.Errorf without %w).
func OkE[T any](v T, ok bool) OkResult[T] {
	panicUnrewritten("q.OkE")
	return OkResult[T]{}
}

// OkResult carries a captured (value, ok) pair for the q.OkE chain.
// The receiver fields are extracted by the preprocessor when emitting
// the rewritten if-not-ok-then-return shape; the struct itself is
// never materialized in production code.
type OkResult[T any] struct {
	v T
	// ok is documented as part of the chain contract — the rewriter
	// erases every call site before the binary runs, so the run-time
	// method bodies (which panic if reached) never read it.
	ok bool //nolint:unused // documented as part of the chain contract
}

// Err bubbles the supplied constant error when the captured ok is
// false.
func (r OkResult[T]) Err(replacement error) T {
	panicUnrewritten("q.OkE(...).Err")
	return r.v
}

// ErrF computes the bubble error via fn — useful when the error needs
// runtime work to assemble. No captured source error (the not-ok
// branch has none), so fn takes no arguments.
func (r OkResult[T]) ErrF(fn func() error) T {
	panicUnrewritten("q.OkE(...).ErrF")
	return r.v
}

// Catch handles a not-ok value via fn, which returns either a
// recovered (T, nil) — used in place of the bubble — or a new error
// (zero, err) — bubbled. Mirrors NotNilE.Catch's shape.
func (r OkResult[T]) Catch(fn func() (T, error)) T {
	panicUnrewritten("q.OkE(...).Catch")
	return r.v
}

// Wrap bubbles errors.New(msg). There is no source error to %w-wrap
// on the not-ok branch; the message stands alone.
func (r OkResult[T]) Wrap(msg string) T {
	panicUnrewritten("q.OkE(...).Wrap")
	return r.v
}

// Wrapf bubbles fmt.Errorf(format, args...). No %w is appended —
// there is no source error on the not-ok branch — so the supplied
// format is the full message.
//
// Example:
//
//	user := q.OkE(users[id]).Wrapf("no user %d", id)
//	// rewrites to: if !ok { return zero, fmt.Errorf("no user %d", id) }
func (r OkResult[T]) Wrapf(format string, args ...any) T {
	panicUnrewritten("q.OkE(...).Wrapf")
	return r.v
}

// RecvRawCtx is the runtime helper for q.RecvCtx. select-blocks on
// ch and ctx.Done(); returns (v, nil) on a successful receive,
// (zero, ErrChanClosed) on a closed channel, and (zero, ctx.Err())
// on context cancellation. Plain runtime function — callable
// directly when the raw tuple is wanted.
func RecvRawCtx[T any](ctx context.Context, ch <-chan T) (T, error) {
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case v, ok := <-ch:
		if !ok {
			var zero T
			return zero, ErrChanClosed
		}
		return v, nil
	}
}

// CheckCtx is a context-cancellation checkpoint. Returns nothing —
// only valid as an expression statement. The preprocessor rewrites
// the call site into `if err := ctx.Err(); err != nil { return zero,
// err }`, bubbling either context.Canceled or context.DeadlineExceeded
// out of the enclosing function. Reach for q.CheckCtxE for chain-style
// error shaping around the bubble. See `docs/api/checkctx.md`.
func CheckCtx(ctx context.Context) {
	panicUnrewritten("q.CheckCtx")
}

// CheckCtxE is the chain variant of q.CheckCtx. Reuses CheckResult so
// the chain vocabulary is identical to q.CheckE — Err / ErrF / Wrap /
// Wrapf / Catch — applied to ctx.Err() before the bubble fires.
func CheckCtxE(ctx context.Context) CheckResult {
	panicUnrewritten("q.CheckCtxE")
	return CheckResult{}
}

// RecvCtx receives from ch while honouring ctx cancellation. The
// preprocessor rewrites the call site into `v, err := q.RecvRawCtx(ctx,
// ch); if err != nil { return zero, err }`. Use q.RecvCtxE to shape
// the bubbled error with the ErrResult vocabulary.
func RecvCtx[T any](ctx context.Context, ch <-chan T) T {
	panicUnrewritten("q.RecvCtx")
	var zero T
	return zero
}

// RecvCtxE is the chain variant of RecvCtx. Reuses ErrResult[T] so
// the chain vocabulary is identical to TryE — the bubbled error is
// either ctx.Err() or q.ErrChanClosed, shaped by the chain method.
func RecvCtxE[T any](ctx context.Context, ch <-chan T) ErrResult[T] {
	panicUnrewritten("q.RecvCtxE")
	return ErrResult[T]{}
}

// AwaitRawCtx is the runtime helper for q.AwaitCtx. Blocks on the
// Future's result channel and ctx.Done(); returns the Future's
// (v, err) on completion or (zero, ctx.Err()) on cancellation. If
// ctx fires first, the underlying goroutine continues until fn
// returns on its own — Go has no goroutine-kill. Thread the same
// ctx into the q.Async closure when early cancellation of the
// spawned work is needed.
func AwaitRawCtx[T any](ctx context.Context, f Future[T]) (T, error) {
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case r := <-f.done:
		return r.v, r.err
	}
}

// AwaitAllRaw waits for every future to complete in parallel, then
// returns the collected values in the order the futures were passed.
// Bubbles the first error observed — either a future's own error —
// and returns (nil, err) immediately; remaining futures' goroutines
// keep running until they finish on their own (Go has no
// goroutine-kill).
//
// Plain runtime function (NOT rewritten by the preprocessor),
// callable directly when the raw ([]T, error) tuple is wanted.
func AwaitAllRaw[T any](futures ...Future[T]) ([]T, error) {
	if len(futures) == 0 {
		return nil, nil
	}
	type indexed struct {
		i   int
		v   T
		err error
	}
	ch := make(chan indexed, len(futures))
	for i, f := range futures {
		go func(i int, f Future[T]) {
			v, err := AwaitRaw(f)
			ch <- indexed{i: i, v: v, err: err}
		}(i, f)
	}
	results := make([]T, len(futures))
	for range len(futures) {
		r := <-ch
		if r.err != nil {
			return nil, r.err
		}
		results[r.i] = r.v
	}
	return results, nil
}

// AwaitAllRawCtx is AwaitAllRaw with context cancellation: if ctx
// fires before all futures complete, returns (nil, ctx.Err())
// immediately (without aggregating the pending futures' Ctx-error
// duplicates). Same goroutine-leak caveat as AwaitRawCtx — thread
// ctx into each q.Async closure for true early cancellation of the
// spawned work.
func AwaitAllRawCtx[T any](ctx context.Context, futures ...Future[T]) ([]T, error) {
	if len(futures) == 0 {
		return nil, nil
	}
	type indexed struct {
		i   int
		v   T
		err error
	}
	ch := make(chan indexed, len(futures))
	for i, f := range futures {
		go func(i int, f Future[T]) {
			v, err := AwaitRaw(f)
			ch <- indexed{i: i, v: v, err: err}
		}(i, f)
	}
	results := make([]T, len(futures))
	for range len(futures) {
		select {
		case r := <-ch:
			if r.err != nil {
				return nil, r.err
			}
			results[r.i] = r.v
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return results, nil
}

// AwaitAll waits for every future to succeed and returns their
// collected values in input order. The preprocessor rewrites the
// call site into `vs, err := q.AwaitAllRaw(futures...); if err !=
// nil { return zero, err }`.
func AwaitAll[T any](futures ...Future[T]) []T {
	panicUnrewritten("q.AwaitAll")
	return nil
}

// AwaitAllE is the chain variant of AwaitAll. Reuses ErrResult
// with element type []T — the bubbled error is whichever error
// AwaitAllRaw saw first.
func AwaitAllE[T any](futures ...Future[T]) ErrResult[[]T] {
	panicUnrewritten("q.AwaitAllE")
	return ErrResult[[]T]{}
}

// AwaitAllCtx is AwaitAll with context cancellation; the rewriter
// emits q.AwaitAllRawCtx as the inner helper.
func AwaitAllCtx[T any](ctx context.Context, futures ...Future[T]) []T {
	panicUnrewritten("q.AwaitAllCtx")
	return nil
}

// AwaitAllCtxE is the chain variant of AwaitAllCtx.
func AwaitAllCtxE[T any](ctx context.Context, futures ...Future[T]) ErrResult[[]T] {
	panicUnrewritten("q.AwaitAllCtxE")
	return ErrResult[[]T]{}
}

// AwaitAnyRaw returns the first future to complete successfully.
// If every future returns an error, returns (zero, errors.Join(…))
// of each collected error in completion order.
//
// Plain runtime function (NOT rewritten by the preprocessor).
func AwaitAnyRaw[T any](futures ...Future[T]) (T, error) {
	var zero T
	if len(futures) == 0 {
		return zero, errors.New("q.AwaitAny: no futures to await")
	}
	type indexed struct {
		v   T
		err error
	}
	ch := make(chan indexed, len(futures))
	for _, f := range futures {
		go func(f Future[T]) {
			v, err := AwaitRaw(f)
			ch <- indexed{v: v, err: err}
		}(f)
	}
	var errs []error
	for range len(futures) {
		r := <-ch
		if r.err == nil {
			return r.v, nil
		}
		errs = append(errs, r.err)
	}
	return zero, errors.Join(errs...)
}

// AwaitAnyRawCtx is AwaitAnyRaw with context cancellation: if ctx
// fires before any success, returns (zero, ctx.Err()) once — any
// already-collected per-future errors are discarded in favour of
// ctx.Err().
func AwaitAnyRawCtx[T any](ctx context.Context, futures ...Future[T]) (T, error) {
	var zero T
	if len(futures) == 0 {
		return zero, errors.New("q.AwaitAnyCtx: no futures to await")
	}
	type indexed struct {
		v   T
		err error
	}
	ch := make(chan indexed, len(futures))
	for _, f := range futures {
		go func(f Future[T]) {
			v, err := AwaitRaw(f)
			ch <- indexed{v: v, err: err}
		}(f)
	}
	var errs []error
	for range len(futures) {
		select {
		case r := <-ch:
			if r.err == nil {
				return r.v, nil
			}
			errs = append(errs, r.err)
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}
	return zero, errors.Join(errs...)
}

// AwaitAny returns the first future to succeed. If every future
// fails, bubbles an errors.Join of each failure. Preprocessor-
// rewritten like q.Await.
func AwaitAny[T any](futures ...Future[T]) T {
	panicUnrewritten("q.AwaitAny")
	var zero T
	return zero
}

// AwaitAnyE is the chain variant of AwaitAny.
func AwaitAnyE[T any](futures ...Future[T]) ErrResult[T] {
	panicUnrewritten("q.AwaitAnyE")
	return ErrResult[T]{}
}

// AwaitAnyCtx is AwaitAny with context cancellation.
func AwaitAnyCtx[T any](ctx context.Context, futures ...Future[T]) T {
	panicUnrewritten("q.AwaitAnyCtx")
	var zero T
	return zero
}

// AwaitAnyCtxE is the chain variant of AwaitAnyCtx.
func AwaitAnyCtxE[T any](ctx context.Context, futures ...Future[T]) ErrResult[T] {
	panicUnrewritten("q.AwaitAnyCtxE")
	return ErrResult[T]{}
}

// AwaitCtx blocks on a Future while honouring ctx cancellation.
// The preprocessor rewrites the call site into the inlined `v, err
// := q.AwaitRawCtx(ctx, f); if err != nil { return zero, err }`
// shape. Reach for q.AwaitCtxE for chain-style custom error handling.
func AwaitCtx[T any](ctx context.Context, f Future[T]) T {
	panicUnrewritten("q.AwaitCtx")
	var zero T
	return zero
}

// AwaitCtxE is the chain variant of AwaitCtx; reuses ErrResult[T]
// so the chain vocabulary is identical to TryE / AwaitE.
func AwaitCtxE[T any](ctx context.Context, f Future[T]) ErrResult[T] {
	panicUnrewritten("q.AwaitCtxE")
	return ErrResult[T]{}
}

// Timeout derives a child context cancelled after dur. The
// preprocessor rewrites `ctx = q.Timeout(ctx, 5*time.Second)` (or
// `newCtx := q.Timeout(ctx, 5*time.Second)`) into the two-line
// idiom `ctx, _qCancelN := context.WithTimeout(ctx, dur); defer
// _qCancelN()` — the cancel function is hidden and auto-deferred
// in the enclosing function. Only valid in define (`:=`) or
// assign (`=`) position with a single LHS.
//
// Example:
//
//	ctx = q.Timeout(ctx, 5*time.Second)
//	reply := q.Try(call(ctx))
//
// For "cancel early from another goroutine" flows, write
// `ctx, cancel := context.WithCancel(parent)` by hand — q.Timeout
// hides the cancel function, which is the wrong default when
// outside code needs to invoke it.
func Timeout(ctx context.Context, dur time.Duration) context.Context {
	panicUnrewritten("q.Timeout")
	return ctx
}

// Deadline derives a child context cancelled at t. Same shape as
// Timeout but takes a time.Time for propagating an inherited
// deadline (e.g. from an HTTP header or parent job) rather than a
// fresh relative timeout.
func Deadline(ctx context.Context, t time.Time) context.Context {
	panicUnrewritten("q.Deadline")
	return ctx
}

// RecvAnyRaw performs a dynamic N-way select over the supplied
// channels and returns the first value received. On any channel
// close, returns (zero, ErrChanClosed). If len(chans) == 0,
// returns a descriptive error (a 0-way select would block forever).
//
// Uses reflect.Select under the hood — necessary because the
// channel count is runtime-sized. Plain runtime function (NOT
// rewritten by the preprocessor).
func RecvAnyRaw[T any](chans ...<-chan T) (T, error) {
	var zero T
	if len(chans) == 0 {
		return zero, errors.New("q.RecvAny: no channels to select on")
	}
	cases := make([]reflect.SelectCase, len(chans))
	for i, c := range chans {
		cases[i] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(c)}
	}
	_, rv, ok := reflect.Select(cases)
	if !ok {
		return zero, ErrChanClosed
	}
	return rv.Interface().(T), nil
}

// RecvAnyRawCtx is RecvAnyRaw with ctx cancellation. If ctx fires
// before any channel delivers, returns (zero, ctx.Err()).
func RecvAnyRawCtx[T any](ctx context.Context, chans ...<-chan T) (T, error) {
	var zero T
	if len(chans) == 0 {
		return zero, errors.New("q.RecvAnyCtx: no channels to select on")
	}
	cases := make([]reflect.SelectCase, len(chans)+1)
	for i, c := range chans {
		cases[i] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(c)}
	}
	cases[len(chans)] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())}
	idx, rv, ok := reflect.Select(cases)
	if idx == len(chans) {
		return zero, ctx.Err()
	}
	if !ok {
		return zero, ErrChanClosed
	}
	return rv.Interface().(T), nil
}

// RecvAny returns the first value received across the supplied
// channels. Preprocessor-rewritten as Try-like bubble over
// q.RecvAnyRaw; reach for q.RecvAnyCtx when ctx cancellation
// should bail early, q.RecvAnyE / q.RecvAnyCtxE for chain-style
// error shaping (including "ignore close, keep waiting" via
// Catch+recover).
func RecvAny[T any](chans ...<-chan T) T {
	panicUnrewritten("q.RecvAny")
	var zero T
	return zero
}

// RecvAnyE is the chain variant of RecvAny.
func RecvAnyE[T any](chans ...<-chan T) ErrResult[T] {
	panicUnrewritten("q.RecvAnyE")
	return ErrResult[T]{}
}

// RecvAnyCtx is RecvAny with ctx cancellation.
func RecvAnyCtx[T any](ctx context.Context, chans ...<-chan T) T {
	panicUnrewritten("q.RecvAnyCtx")
	var zero T
	return zero
}

// RecvAnyCtxE is the chain variant of RecvAnyCtx.
func RecvAnyCtxE[T any](ctx context.Context, chans ...<-chan T) ErrResult[T] {
	panicUnrewritten("q.RecvAnyCtxE")
	return ErrResult[T]{}
}

// Drain receives from ch until it closes, returning the collected
// values in reception order. Plain runtime function — no error
// path (the only way to fail is ctx cancellation, and this form
// doesn't take one). If ch never closes, Drain blocks forever;
// reach for q.DrainCtx when a cancellable wait is needed.
func Drain[T any](ch <-chan T) []T {
	var out []T
	for v := range ch {
		out = append(out, v)
	}
	return out
}

// DrainAll drains every supplied channel concurrently until all
// close, returning the per-channel collected values in input order.
// Plain runtime function — same no-error-path semantics as Drain.
func DrainAll[T any](chans ...<-chan T) [][]T {
	results := make([][]T, len(chans))
	var wg sync.WaitGroup
	for i, ch := range chans {
		wg.Add(1)
		go func(i int, ch <-chan T) {
			defer wg.Done()
			results[i] = Drain(ch)
		}(i, ch)
	}
	wg.Wait()
	return results
}

// DrainRawCtx receives from ch until it closes or ctx cancels. On
// cancel, returns (nil, ctx.Err()) — the already-gathered values
// are discarded on the bubble path.
func DrainRawCtx[T any](ctx context.Context, ch <-chan T) ([]T, error) {
	var out []T
	for {
		select {
		case v, ok := <-ch:
			if !ok {
				return out, nil
			}
			out = append(out, v)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// DrainCtx drains ch until close or ctx cancellation. Preprocessor-
// rewritten as Try-like bubble over q.DrainRawCtx. Bubbles ctx.Err()
// on cancel.
func DrainCtx[T any](ctx context.Context, ch <-chan T) []T {
	panicUnrewritten("q.DrainCtx")
	return nil
}

// DrainCtxE is the chain variant of DrainCtx.
func DrainCtxE[T any](ctx context.Context, ch <-chan T) ErrResult[[]T] {
	panicUnrewritten("q.DrainCtxE")
	return ErrResult[[]T]{}
}

// DrainAllRawCtx drains every supplied channel concurrently until
// all close or ctx cancels. On cancel, returns (nil, ctx.Err()) —
// partial per-channel results are discarded on the bubble path.
// Background goroutines continue draining until each source closes
// (Go has no goroutine-kill); thread ctx into the producer side
// for true early shutdown.
func DrainAllRawCtx[T any](ctx context.Context, chans ...<-chan T) ([][]T, error) {
	if len(chans) == 0 {
		return nil, nil
	}
	type indexed struct {
		i  int
		vs []T
	}
	ch := make(chan indexed, len(chans))
	for i, c := range chans {
		go func(i int, c <-chan T) {
			var out []T
			for {
				select {
				case v, ok := <-c:
					if !ok {
						ch <- indexed{i: i, vs: out}
						return
					}
					out = append(out, v)
				case <-ctx.Done():
					ch <- indexed{i: i, vs: out}
					return
				}
			}
		}(i, c)
	}
	results := make([][]T, len(chans))
	for range len(chans) {
		select {
		case r := <-ch:
			results[r.i] = r.vs
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return results, nil
}

// DrainAllCtx drains every channel until all close or ctx cancels.
// Preprocessor-rewritten as Try-like bubble over q.DrainAllRawCtx.
func DrainAllCtx[T any](ctx context.Context, chans ...<-chan T) [][]T {
	panicUnrewritten("q.DrainAllCtx")
	return nil
}

// DrainAllCtxE is the chain variant of DrainAllCtx.
func DrainAllCtxE[T any](ctx context.Context, chans ...<-chan T) ErrResult[[]T] {
	panicUnrewritten("q.DrainAllCtxE")
	return ErrResult[[]T]{}
}

// ToErr adapts a `(T, *E)` call — where `*E` satisfies `error` —
// into `(T, error)` with a nil-check that collapses a typed-nil
// `*E` into a literal nil error. Use this to unblock a call
// rejected by q's typed-nil-interface guard:
//
//	// Foo declared as `func Foo() (int, *MyErr)`.
//	v := q.Try(q.ToErr(Foo()))
//
// Unlike every other helper in this package, ToErr is a plain
// runtime function — it is NOT rewritten by the preprocessor. Its
// body executes at runtime. The `interface{ *E; error }`
// constraint forces `*E` to implement error at compile time, so
// Go's type inference can figure out T/E/P from the call, and
// misuse (passing a non-error pointer) is caught statically.
//
// ToErr is intentionally small and standalone — it's also useful
// outside q, for any API that returns a concrete error pointer
// and gets assigned into an `error` slot elsewhere.
func ToErr[T any, E any, P interface {
	*E
	error
}](v T, e P) (T, error) {
	if e == nil {
		return v, nil
	}
	return v, e
}
