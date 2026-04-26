# `q.Lazy` — deferred evaluation of arbitrary expressions

`q.Lazy(<expr>)` reads as if the expression were evaluated eagerly,
but the preprocessor wraps it in a thunk closure so evaluation defers
until the first `.Value()` call. Subsequent calls return the cached
result. Same source-splicing trick as `q.Tern`, with one branch
instead of three.

```go
l := q.Lazy(expensiveLookup(key))
// expensiveLookup(key) has NOT run yet.

if condition {
    v := l.Value() // first .Value() runs the thunk; v is the result
    _ = v
}
// If `condition` is false, expensiveLookup(key) never runs at all.
```

## Surface

```go
func Lazy[T any](v T) *LazyValue[T]
func LazyFromThunk[T any](thunk func() T) *LazyValue[T]
func (*LazyValue[T]) Value() T              // sync.Once-backed first-eval
func (*LazyValue[T]) IsForced() bool        // diagnostic: has the thunk run?

func LazyE[T any](v T, err error) *LazyValueE[T]
func LazyEFromThunk[T any](thunk func() (T, error)) *LazyValueE[T]
func (*LazyValueE[T]) Value() (T, error)    // sync.Once-backed; error is cached too
func (*LazyValueE[T]) IsForced() bool       // diagnostic
```

`q.Lazy(<expr>)` and `q.LazyE(<call>)` are the user-facing entries —
both get rewritten by the preprocessor. `q.LazyFromThunk(<thunk>)` /
`q.LazyEFromThunk(<thunk>)` are plain runtime constructors (not
rewritten); reach for them when you genuinely have a hand-written
thunk and want the same memoised semantics without going through the
sugar.

## What the rewriter does

```go
l := q.Lazy(calculateValue())
```

rewrites to:

```go
l := q.LazyFromThunk(func() int { return calculateValue() })
```

For `q.LazyE`:

```go
cfgL := q.LazyE(loadConfig())  // loadConfig() (Config, error)
```

rewrites to:

```go
cfgL := q.LazyEFromThunk(func() (Config, error) { return loadConfig() })
```

The thunk captures whatever locals the spliced expression referenced
(normal Go closure semantics). T is inferred from the value
expression's static type; the explicit form `q.Lazy[T](v)` /
`q.LazyE[T](call)` is also accepted.

## Properties

- **Lazy first-eval.** The wrapped expression runs only on the first
  `.Value()` call. If `.Value()` is never called, the expression
  never runs.
- **Memoised.** Subsequent `.Value()` calls return the cached result
  — no re-evaluation.
- **Concurrency-safe first-eval.** `.Value()` is `sync.Once`-backed.
  Many goroutines racing on the first call resolve to a single thunk
  execution; later callers see the memoised result.
- **Closure capture.** The thunk captures locals by reference, just
  like any Go closure. Mutating a captured local between the
  `q.Lazy(<expr>)` call and the first `.Value()` means the thunk
  sees the mutated value.

## Examples

```go
// Defer an expensive lookup until it's actually needed:
cfg := q.Lazy(loadConfigFromDisk())
if userRequested {
    settings := cfg.Value()
    // ...
}
// loadConfigFromDisk() never ran if userRequested was false.

// Force-eval and reuse:
total := q.Lazy(sumLargeSlice(items))
fmt.Println("total:", total.Value())   // computes once
fmt.Println("again:", total.Value())   // cached

// Diagnostic check (e.g., in a debug-trace prelude):
if cfg.IsForced() {
    log.Println("config was loaded this request")
}

// Hand-written thunk via q.LazyFromThunk — useful when the value
// requires arguments computed elsewhere:
mk := q.LazyFromThunk(func() *DB { return connect(host, port) })
db := mk.Value()
```

## Pairing q.LazyE with q.Try

The natural consumer-side shape:

```go
cfgL := q.LazyE(loadConfig())
// ... later, possibly never:
cfg := q.Try(cfgL.Value())  // bubbles loadConfig's error if it failed
```

The error is cached after the first call — repeated `.Value()`
returns the same `(T, error)` pair. The thunk does not retry.

## Caveats

- **Recursive `.Value()` from inside the thunk deadlocks.** This is
  standard `sync.Once` behaviour; we don't guard against it.
  Structure your thunks to not re-enter their own `.Value()`.
- **Cached errors don't retry.** `q.LazyE` is "memoise the first
  outcome." For "retry on failure" semantics, layer your own retry
  loop around `q.LazyEFromThunk` or use a different abstraction.

## See also

- [`q.Tern`](tern.md) — the same source-splicing trick with two
  branches; reach for `q.Tern` when picking between expressions.
- [`q.AtCompileTime`](atcompiletime.md) — lazy at the *preprocessor*
  level (evaluates once at build time, ships the result). Different
  axis from `q.Lazy` (lazy at *runtime*, on first access).
