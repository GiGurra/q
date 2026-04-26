package q

// assemble.go — ZIO ZLayer-style auto-derived dependency injection at
// preprocess time. List the recipe functions or inline values; the
// preprocessor reads each recipe's signature, builds a dep graph
// keyed by output type, topo-sorts it, and emits a flat sequence of
// calls building the requested target T. Returns AssemblyResult[T];
// pick the chain terminator to set the resource-lifetime policy:
//
//	server, err := q.Assemble[*Server](newConfig, newDB, newServer).DeferCleanup()
//	server      := q.Try(q.Assemble[*Server](...).DeferCleanup())    // inside (T, error)-returning fn
//	server      := q.Unwrap(q.Assemble[*Server](...).DeferCleanup()) // panic on err (main, init, tests)
//	server      := q.TryE(q.Assemble[*Server](...).DeferCleanup()).Wrap("server init") // chain shape
//
//	server, shutdown, err := q.Assemble[*Server](...).NoDeferCleanup() // caller owns shutdown
//
// context.Context isn't special — it's just another dependency. If
// a recipe takes context.Context as an input, supply ctx as an
// inline-value recipe; the resolver matches it via interface
// satisfaction. When ctx is supplied AND q.WithAssemblyDebug has
// been called on it, the rewriter emits per-step trace output to
// the configured writer. ctx supplied without any consumer is also
// fine — q.Assemble exempts context.Context from the unused-recipe
// check so passing ctx purely for assembly-config (debug, future
// hooks) doesn't fail the build.
//
// Recipes can be any of:
//
//   - A function reference — top-level func, package-qualified func, method
//     value, or any function-typed expression. Inputs become required deps;
//     the first return value provides its type. Five return shapes are
//     accepted: (T), (T, error), (T, func()), (T, func(), error), and any
//     of those wrapped via q.PermitNil(...) to opt out of the runtime nil-
//     check. Resource shapes (with cleanup func) feed onto the cleanup
//     chain; pure ones don't.
//
//   - An inline value (precomputed value / constant / call result) — any
//     non-function expression. Its type IS the provided type; no inputs
//     required. The ZIO `ZLayer.succeed` analogue. Useful for config
//     overrides, test injections, and threading external state. Inline
//     values are caller-owned: never auto-closed even if T has a Close
//     shape.
//
// Auto-detected cleanup. When a function recipe returns a type with a
// recognisable close shape (Close() / Close() error / writable channel —
// not recv-only) and the recipe didn't already declare an explicit
// cleanup, the resolver synthesises one. Receive-only channels are never
// auto-closed since closing is the sender's responsibility (and Go itself
// rejects close() on a recv-only channel).
//
// recipes is `...any` because Go's type system can't express "any function
// with any number of inputs and one output". The preprocessor's typecheck
// pass takes over validation — same shape as q.Match's `value any`. Errors
// in the recipe set (missing dep, duplicate provider, cycle, unused recipe,
// unsatisfiable target, ambiguous interface input) surface as build-time
// diagnostics with file:line:col plus a dependency-tree visualisation.

import (
	"context"
	"io"
	"log/slog"
)

// LogCloseErr is the auto-cleanup error sink used by q.Assemble's
// auto-detected resource cleanups when T's Close() returns an
// error. Surfacing failed teardown via slog.Error means a flaky
// shutdown is loud rather than silent.
//
// Users with a custom logging story can replace q.LogCloseErr by
// shadowing it in their own package — but the more idiomatic
// approach is to write an explicit `(T, func(), error)` recipe that
// handles the close in whatever way fits.
func LogCloseErr(err error, recipe string) {
	if err == nil {
		return
	}
	slog.Error("q: assemble auto-cleanup Close failed", "recipe", recipe, "err", err)
}

// AssemblyResult is the chain handle returned by Assemble /
// AssembleAll / AssembleStruct. Pick a chain terminator to choose
// the resource-lifetime policy:
//
//   - .DeferCleanup()   — returns (T, error). Cleanups fire automatically
//                        via a `defer` injected into the enclosing
//                        function, in REVERSE topo order. The fast path
//                        for "build it, use it, the function takes care
//                        of teardown when it returns".
//
//   - .NoDeferCleanup() — returns (T, func(), error). Caller takes manual
//                        ownership of the shutdown closure (idempotent
//                        via sync.OnceFunc). Use when the assembled
//                        value's lifetime spans more than the enclosing
//                        function — e.g. main() that hands `shutdown` to
//                        a signal handler.
//
// Cleanups can come from two sources: explicit recipes returning
// (T, func(), error), or auto-detected from T's type (Close() /
// Close() error / channel types). Pure (T, error) and bare-T
// recipes whose T isn't auto-cleanup-able simply add nothing to
// the chain — the same call composes cleanly with both.
type AssemblyResult[T any] struct {
	v T //nolint:unused // populated by the preprocessor-generated IIFE
}

// DeferCleanup fires the assembled resource's cleanups via a `defer`
// injected into the enclosing function (reverse topo order).
// Returns (T, error). The runtime body is unreachable in a
// successful build.
//
//	func boot() (*Server, error) {
//	    server, err := q.Assemble[*Server](newConfig, openDB, newServer).DeferCleanup()
//	    if err != nil { return nil, err }
//	    return server, nil
//	}
//	// db.Close() runs when boot returns, regardless of err path.
//
// Compose with q.Try / q.Unwrap to drop the err:
//
//	server := q.Try(q.Assemble[*Server](recipes...).DeferCleanup())
func (r AssemblyResult[T]) DeferCleanup() (T, error) {
	panicUnrewritten("q.Assemble[...].DeferCleanup")
	var zero T
	return zero, nil
}

// NoDeferCleanup returns (T, shutdown, error) without any defer-
// injection. The caller controls when shutdown fires. The closure
// is idempotent (wraps sync.OnceFunc); duplicate calls are safe.
//
//	server, shutdown, err := q.Assemble[*Server](recipes...).NoDeferCleanup()
//	if err != nil { log.Fatal(err) }
//	defer shutdown()
//	context.AfterFunc(ctx, shutdown) // ctx cancel also triggers
func (r AssemblyResult[T]) NoDeferCleanup() (T, func(), error) {
	panicUnrewritten("q.Assemble[...].NoDeferCleanup")
	var zero T
	return zero, func() {}, nil
}

// WithScope binds the assembly to a q.Scope. Each step consults
// the scope's cache before invoking its recipe; built deps and
// their cleanups register with the scope on the assembly's
// success path. Mutually exclusive with .DeferCleanup() /
// .NoDeferCleanup() — the scope is the sole lifetime owner.
//
//	scope := q.NewScope().DeferCleanup()
//	server := q.Try(q.Assemble[*Server](recipes...).WithScope(scope))
//
// Cache hits skip both the recipe call AND the cleanup
// registration. Cache misses build fresh and stage the cleanup
// for atomic registration with the scope on success.
//
// If the scope is closed before or during the assembly, returns
// (zero, q.ErrScopeClosed). Fresh deps built in this call before
// the close fire their staged cleanups locally; pre-cached deps
// are not affected (their cleanups were registered earlier and
// fire when the scope itself closes).
//
// Concurrent assemblies in the same scope may both build the same
// fresh type before either commits — last-writer-wins on the
// cache, both cleanups end up registered. Document the orphaning
// risk; use a singleflight wrapper in the recipe if exact-once
// build is required.
func (r AssemblyResult[T]) WithScope(s *Scope) (T, error) {
	panicUnrewritten("q.Assemble[...].WithScope")
	var zero T
	return zero, nil
}

// Assemble builds T from the supplied recipes. Returns an
// AssemblyResult[T]; pick `.DeferCleanup()` or `.NoDeferCleanup()` to
// terminate the chain and choose the resource-lifetime policy.
//
// The preprocessor resolves the dep graph at compile time, topo-
// sorts the recipes, and emits the inlined construction. Recipes
// that produce closeable resources (whether by explicit
// `(T, func(), error)` shape or by T having a Close() method)
// have their cleanups collected and fired in reverse topo order
// during shutdown.
//
//	// Auto-defer pattern (most common):
//	server, err := q.Assemble[*Server](newConfig, openDB, newServer).DeferCleanup()
//
//	// Manual control (graceful shutdown spans main's lifetime):
//	server, shutdown, err := q.Assemble[*Server](recipes...).NoDeferCleanup()
//	defer shutdown()
//
//	// Compose with q.Try:
//	server := q.Try(q.Assemble[*Server](recipes...).DeferCleanup())
func Assemble[T any](recipes ...any) AssemblyResult[T] {
	panicUnrewritten("q.Assemble")
	return AssemblyResult[T]{}
}

// AssembleAll is the multi-provider sibling of Assemble. When several
// recipes legitimately produce values assignable to T (plugins,
// handlers, middlewares — any aggregation pattern), q.Assemble would
// reject with "duplicate provider". AssembleAll opts into the multi-
// provider shape: every assignable recipe contributes one slice
// element in declaration order.
//
// Returns AssemblyResult[[]T]; pick .DeferCleanup() or .NoDeferCleanup() to
// terminate. Same lifetime semantics as q.Assemble.
//
//	plugins, err := q.AssembleAll[Plugin](
//	    newAuthPlugin, newLoggingPlugin, newMetricsPlugin,
//	).DeferCleanup()
func AssembleAll[T any](recipes ...any) AssemblyResult[[]T] {
	panicUnrewritten("q.AssembleAll")
	return AssemblyResult[[]T]{}
}

// PermitNil wraps an Assemble recipe to opt the recipe out of the
// runtime nil-check the rewriter would otherwise emit on the
// recipe's bound _qDep<N> value. Use it when nil IS a valid output
// of the recipe — for example, an optional-dependency ctor where
// downstream consumers are written to handle a nil input.
//
//	// newOptionalCache may legitimately return nil ("no cache configured")
//	cache, err := q.Assemble[*Cache](newConfig, q.PermitNil(newOptionalCache)).DeferCleanup()
//
// PermitNil is a typed identity at runtime — outside the
// preprocessor it just returns recipe unchanged. The preprocessor
// detects q.PermitNil(<recipe>) at scan time, unwraps to <recipe>,
// and marks the resulting Assemble step so the nil-check is
// skipped. Works with both function-reference and inline-value
// recipes.
//
// PermitNil affects ONLY the nil-check on the recipe's own output.
// A nil input to a downstream consumer recipe doesn't change the
// consumer's behaviour — that's the consumer's problem. Recipes
// whose output type can't hold nil (struct values, basic types,
// arrays) pass through unchanged: there's no nil-check to skip.
func PermitNil[T any](recipe T) T { return recipe }

// AssembleStruct is the field-decomposition sibling of Assemble. T
// must be a struct; each field is treated as a separate dep target.
// The preprocessor finds a recipe per field type, builds the shared
// dep graph once, and packs the results into the struct.
//
// Returns AssemblyResult[T]; pick .DeferCleanup() or .NoDeferCleanup() to
// terminate. Same lifetime semantics as q.Assemble.
//
//	type App struct {
//	    Server *Server
//	    Worker *Worker
//	}
//	app, err := q.AssembleStruct[App](newConfig, openDB, newServer, newWorker).DeferCleanup()
//
// q.AssembleStruct does NOT honor a recipe whose output IS T —
// choosing this entry IS the signal that you want field
// decomposition.
func AssembleStruct[T any](recipes ...any) AssemblyResult[T] {
	panicUnrewritten("q.AssembleStruct")
	return AssemblyResult[T]{}
}

// assemblyDebugKey is the unexported context-value key that
// WithAssemblyDebug attaches the destination writer under. Using a
// dedicated unexported struct type guarantees no key collision with
// user code.
type assemblyDebugKey struct{}

// WithAssemblyDebug returns ctx with assembly trace output enabled.
// When this ctx is then passed to q.Assemble as an inline-value
// recipe (whether or not any other recipe consumes it), the rewriter
// emits per-step trace output to q.DebugWriter (defaults to
// os.Stderr) — recipe label per call. Useful for diagnosing "why
// did X get the wrong dep" without re-running with a debugger.
//
//	ctx := q.WithAssemblyDebug(context.Background())
//	server := q.Unwrap(q.Assemble[*Server](ctx, newConfig, newDB, newServer))
//
// Use WithAssemblyDebugWriter to redirect output to a custom writer
// (test buffer, log file, etc.).
func WithAssemblyDebug(ctx context.Context) context.Context {
	return context.WithValue(ctx, assemblyDebugKey{}, DebugWriter)
}

// WithAssemblyDebugWriter is WithAssemblyDebug with a caller-supplied
// destination writer. Mostly useful for tests where stdout/stderr
// shouldn't be mutated and a bytes.Buffer is the assertion target.
func WithAssemblyDebugWriter(ctx context.Context, w io.Writer) context.Context {
	return context.WithValue(ctx, assemblyDebugKey{}, w)
}

// AssemblyDebugWriter returns the writer registered via
// WithAssemblyDebug or WithAssemblyDebugWriter, or nil when neither
// has been called on this ctx (or any of its ancestors). The
// rewritten q.Assemble IIFE consults it once per call to gate trace
// output: the conditional `if w := q.AssemblyDebugWriter(ctx); w !=
// nil { ... }` adds one ctx.Value lookup to the no-debug path,
// which is microseconds.
func AssemblyDebugWriter(ctx context.Context) io.Writer {
	if ctx == nil {
		return nil
	}
	w, _ := ctx.Value(assemblyDebugKey{}).(io.Writer)
	return w
}
