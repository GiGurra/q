# Lifetime container: `q.Scope`

`q.Scope` is a runtime cache + cleanup container. Resources, subscopes, and assembly outputs attach to it; closing the scope fires every cleanup in reverse-registration order. It is the lifetime primitive that `q.Assemble[T](...).WithScope(s)` leans on for sharing built deps across calls.

Construction is a chain on `q.NewScope()`:

```go
scope := q.NewScope().DeferCleanup()             // close on enclosing function return
scope, shutdown := q.NewScope().NoDeferCleanup() // caller-managed close
scope := q.NewScope().BoundTo(ctx)               // close when ctx is cancelled
```

The chain choice picks the close trigger; the value handed back is always the same `*Scope` (or `*Scope` + a manual close func). Bare `q.NewScope()` without a chain is also valid — the caller manages the lifetime via `scope.Close()` directly.

## Background — what scopes are for

A scope holds two things:

- A **typed cache** keyed by Go type identity (`go/types` canonical strings). `q.Assemble[T](...).WithScope(s)` consults the cache before invoking each recipe, so subsequent assemblies in the same scope reuse already-built deps.
- An **ordered cleanup list**. Anything attached to the scope (closers, custom closures, even other scopes) gets a slot here. `scope.Close()` walks the list in reverse and fires each entry exactly once.

Scopes are not tied to program startup. They generalise over per-request, per-tenant, per-session, per-test lifetimes — any time you'd want to group "these resources live together, close together":

```go
// Per-request: handler-built deps share a request-scoped scope.
func handle(w http.ResponseWriter, r *http.Request) {
    scope := q.NewScope().BoundTo(r.Context())
    server := q.Try(q.Assemble[*Server](recipes...).WithScope(scope))
    // server, db, cache all close when r.Context() cancels.
}

// Per-tenant: long-lived scope, close on tenant deletion.
func tenant(id string) *Scope {
    return q.NewScope() // caller calls scope.Close() at deletion.
}

// Per-test: t.Cleanup fires the close.
func TestThing(t *testing.T) {
    scope := q.NewScope()
    t.Cleanup(scope.Close)
    // ...
}
```

The model is borrowed from [ZIO Scope](https://zio.dev/reference/contextual/scope/) — a runtime resource manager that owns finalisers and fires them on close. The cooking metaphor that q's `Assemble` uses for recipes maps cleanly onto ZIO's `ZLayer`; `Scope` carries the same name as its inspiration because the role is identical.

## Construction terminators

### `.DeferCleanup()` — auto-defer (the fast path)

Returns `*Scope`. The preprocessor injects `defer scope.Close()` into the *enclosing function* so cleanups fire when that function returns:

```go
func boot() error {
    scope := q.NewScope().DeferCleanup()
    server := q.Try(q.Assemble[*Server](recipes...).WithScope(scope))
    server.Run()
    return nil
}
// scope.Close() fires when boot returns, regardless of err path.
```

The chain *must* be applied directly to `q.NewScope()` — the rewriter recognises the literal `q.NewScope().DeferCleanup()` shape. Calling `.DeferCleanup()` on a scope passed in as a parameter is rejected.

### `.NoDeferCleanup()` — caller-managed close

Returns `(*Scope, func())`. The closure wraps `scope.Close` and is idempotent (sync.Once-backed) — calling it more than once is safe. Useful when the scope's lifetime spans more than one function:

```go
func main() {
    scope, shutdown := q.NewScope().NoDeferCleanup()
    defer shutdown()
    context.AfterFunc(ctx, shutdown) // optional: ctx cancel also triggers
    server := q.Unwrap(q.Assemble[*Server](recipes...).WithScope(scope))
    server.Run()
}
```

### `.BoundTo(ctx)` — close on ctx cancellation

Returns `*Scope` wired to `context.AfterFunc(ctx, scope.Close)`. The most common shape for per-request and per-session scopes:

```go
func handle(w http.ResponseWriter, r *http.Request) {
    scope := q.NewScope().BoundTo(r.Context())
    db := q.Try(q.Assemble[*DB](recipes...).WithScope(scope))
    // db.Close() fires when r.Context() is cancelled — request end, timeout, etc.
}
```

Direct `scope.Close()` remains valid; both paths are idempotent.

## Attaching things — `Attach`, `AttachE`, `AttachFn`, `AttachFnE`

Beyond `q.Assemble[T](...).WithScope(s)`, anything implementing `Close()` (or `Close() error`) — including subscopes — can be attached manually:

```go
// Closer with void Close().
scope.Attach(myWorker)
// Closer with Close() error — errors routed through q.LogCloseErr.
scope.AttachE(myDB)
// Custom closure — handle for later Detach.
scope.AttachFn(myConn, func() { myConn.Drain(); myConn.Close() })
// Error-returning custom closure — error routed through q.LogCloseErr.
scope.AttachFnE(myStream, func() error { return myStream.Flush() })
```

Each Attach* call returns `error`: `q.ErrScopeClosed` if the scope has already closed (the caller is then expected to fire the cleanup directly).

`*Scope` itself implements `Close()`, so subscopes nest naturally:

```go
parent := q.NewScope().BoundTo(ctx)
child := q.NewScope()
parent.Attach(child)
// when parent closes → child closes → its members close.
```

### `Detach(handle) bool`

Attach* takes a handle (the value passed in for `Attach` / `AttachE`, or the explicit handle arg for `AttachFn` / `AttachFnE`). Pass that handle back to remove the cleanup before close runs:

```go
scope.AttachFn(conn, conn.Close)
// later, transfer ownership elsewhere:
if scope.Detach(conn) {
    // cleanup is no longer registered; conn is the caller's again.
}
```

`handle` must be `==` comparable (pointers, comparable structs, interface holding such). Detach matches the **first** registered cleanup under that handle; repeated calls peel off duplicates in reverse-registration order.

`Detach` is a no-op (returns false) on a closed scope — the cleanup list was already cleared and fired.

### Close ordering — strict LIFO

Cleanups fire in reverse-registration order. There is no priority/before-after API today; if a workload demands non-LIFO ordering (logger that should outlive other cleanups, drain signal that fires first), open an issue with the use case so we can shape the surface around real evidence.

## Concurrency

All `*Scope` operations are safe under concurrent access. `Commit` and `Attach*` serialise their writes under one lock acquisition, so a concurrent `Close` either sees the entire batch or none of it. `Close` itself takes the lock just long enough to flip the closed flag and snapshot the cleanup list, then fires cleanups outside the lock — meaning a cleanup is free to call back into the scope (e.g. `parent.Detach(self)` from a child) without deadlocking.

Closing mid-flight while a `q.Assemble[T](...).WithScope(s)` is running is supported: the in-flight assembly observes the closed state on its next `Load` or at the final `Commit`, fires any locally-built fresh deps, and returns `q.ErrScopeClosed`. Cached entries that were already in the scope when it closed get cleaned up by the close itself.

## Internal: `q.ScopeEntry` and `Commit`

`q.Assemble`'s `.WithScope(s)` chain rewrites into a sequence of `s.Load(key)` cache reads followed by a single `s.Commit(entries, child)` write that publishes any freshly-built deps. `q.ScopeEntry` is the record shape used by that batch — one entry per fresh build, carrying the cache key plus the cleanup closure. End users never construct these directly; they're documented here so the ABI between `q.Assemble`'s rewrite output and the `*Scope` runtime is discoverable.

## See also

- [`q.Assemble`](assemble.md) — the recipe-driven DI framework whose `.WithScope(scope)` chain leaf is the primary consumer of scopes.
- [`q.Open`](open.md) — single-resource lifetime helper. Useful when one resource and one cleanup is the whole pattern.
- [ZIO Scope](https://zio.dev/reference/contextual/scope/) — the conceptual ancestor.
