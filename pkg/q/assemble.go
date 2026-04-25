package q

// assemble.go — ZIO ZLayer-style auto-derived dependency injection at
// preprocess time. List the recipe functions and inline values; the
// preprocessor reads each recipe's signature, builds a dep graph
// keyed by output type, topo-sorts it, and emits a flat sequence of
// calls building the requested target T.
//
// Three forms — same shape as the rest of the bubble family, with
// the same E-variant chain vocabulary:
//
//   - q.Assemble[T](recipes...)      — pure assembly; every recipe is f(deps...) T
//   - q.AssembleErr[T](recipes...)   — at least one recipe is f(deps...) (T, error);
//                                       composes with q.Try / q.TryE
//   - q.AssembleE[T](recipes...).<Method>(...) — chain variant; .Err / .ErrF /
//                                       .Wrap / .Wrapf / .Catch shape the bubble
//
// Example:
//
//	type Config struct{ DB string }
//	type DB struct{ cfg *Config }
//	type Server struct{ db *DB; cfg *Config }
//
//	func newConfig() *Config              { return &Config{DB: "..."} }
//	func newDB(c *Config) (*DB, error)    { return &DB{cfg: c}, nil }
//	func newServer(db *DB, c *Config) *Server { return &Server{db: db, cfg: c} }
//
//	server := q.Try(q.AssembleErr[*Server](newConfig, newDB, newServer))
//
// Recipes can be any of:
//
//   - A function reference (top-level func, package-qualified func, method value,
//     or any function-typed expression). Inputs are required deps; the first
//     return value provides its type. (T, error)-returning recipes mark the
//     graph as errored and are only valid in q.AssembleErr / q.AssembleE.
//   - An inline value — any non-function expression. Its type IS the provided
//     type; no deps required. The direct ZIO `ZLayer.succeed` analogue.
//
// recipes is `...any` because Go's type system can't express "any function
// with any number of inputs and one output". The preprocessor's typecheck
// pass takes over validation — same shape as q.Match's `value any`. Errors
// in the recipe set (missing dep, duplicate provider, cycle, unused recipe,
// unsatisfiable target) surface as build-time diagnostics with file:line:col.

// Assemble builds T from the supplied recipes. Every recipe must be
// pure (f(deps...) T); use q.AssembleErr if any recipe can return an
// error.
//
// The preprocessor resolves the dep graph at compile time, topo-sorts
// the recipes, and emits the inlined construction. The runtime body
// is unreachable in a successful build.
func Assemble[T any](recipes ...any) T {
	panicUnrewritten("q.Assemble")
	var zero T
	return zero
}

// AssembleErr is q.Assemble with errored recipes. At least one recipe
// must return (T, error); on the bubble path the (T, error) pair
// propagates from the IIFE the rewriter emits. Compose with q.Try /
// q.TryE to bubble the failure to the enclosing function:
//
//	server := q.Try(q.AssembleErr[*Server](newConfig, newDB, newServer))
//
// If every recipe is pure, prefer q.Assemble.
func AssembleErr[T any](recipes ...any) (T, error) {
	panicUnrewritten("q.AssembleErr")
	var zero T
	return zero, nil
}

// AssembleE is the chain variant of AssembleErr. Reuses ErrResult[T]
// so the chain vocabulary is identical to TryE / AwaitE — .Err,
// .ErrF, .Wrap, .Wrapf, .Catch shape the bubble before it propagates
// to the enclosing function.
//
//	server := q.AssembleE[*Server](newConfig, newDB, newServer).Wrap("server init")
func AssembleE[T any](recipes ...any) ErrResult[T] {
	panicUnrewritten("q.AssembleE")
	return ErrResult[T]{}
}
