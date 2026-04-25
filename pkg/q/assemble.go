package q

// assemble.go — ZIO ZLayer-style auto-derived dependency injection at
// preprocess time. List the recipe functions or inline values; the
// preprocessor reads each recipe's signature, builds a dep graph
// keyed by output type, topo-sorts it, and emits a flat sequence of
// calls building the requested target T. Always returns (T, error);
// compose at the call site:
//
//	server, err := q.Assemble[*Server](newConfig, newDB, newServer)
//	server      := q.Try(q.Assemble[*Server](...))    // inside (T, error)-returning fn
//	server      := q.Unwrap(q.Assemble[*Server](...)) // panic on err (main, init, tests)
//	server      := q.TryE(q.Assemble[*Server](...)).Wrap("server init") // chain shape
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
//     the first return value provides its type. (T, error) returns let the
//     recipe bubble its own failure into the assembly's error path.
//
//   - An inline value (precomputed value / constant / call result) — any
//     non-function expression. Its type IS the provided type; no inputs
//     required. The ZIO `ZLayer.succeed` analogue. Useful for config
//     overrides, test injections, and threading external state.
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
)

// Assemble builds T from the supplied recipes. Always returns
// (T, error); compose with q.Try when you want bare T.
//
// The preprocessor resolves the dep graph at compile time, topo-sorts
// the recipes, and emits the inlined construction. The runtime body
// is unreachable in a successful build.
//
//	server, err := q.Assemble[*Server](newConfig, newDB, newServer)
//
//	// Or compose with q.Try at the call site:
//	server := q.Try(q.Assemble[*Server](newConfig, newDB, newServer))
func Assemble[T any](recipes ...any) (T, error) {
	panicUnrewritten("q.Assemble")
	var zero T
	return zero, nil
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
