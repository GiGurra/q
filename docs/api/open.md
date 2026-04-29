# `q.Open` and `q.OpenE`

Resource acquisition with defer-on-success cleanup — the `(T, error)` bubble plus a registered `defer cleanup(resource)` on the success path.

## Signatures

```go
func Open[T any](v T, err error) OpenResult[T]
func OpenE[T any](v T, err error) OpenResultE[T]

func (OpenResult[T])  DeferCleanup(cleanup ...any) T   // 0 or 1 args; cleanup is func(T) or func(T) error
func (OpenResult[T])  NoDeferCleanup() T
func (OpenResult[T])  WithScope(args ...any) T          // (scope) or (cleanup, scope)
func (OpenResultE[T]) DeferCleanup(cleanup ...any) T   // 0 or 1 args; cleanup is func(T) or func(T) error
func (OpenResultE[T]) NoDeferCleanup() T
func (OpenResultE[T]) WithScope(args ...any) T          // (scope) or (cleanup, scope)
```

`.DeferCleanup(...)`, `.NoDeferCleanup()`, and `.WithScope(...)` are the three terminals — what actually returns `T`. `q.Open(v, err)` on its own returns an `OpenResult[T]` that exposes nothing else; you always chain one terminal onto it. (Why a method, not an extra arg: Go's multi-return spread only fires when the multi-return call is the sole argument, so `q.Open(call(), cleanup)` won't compile. The terminal-method shape side-steps that.)

`.DeferCleanup` accepts zero or one cleanup function:

- `DeferCleanup(cleanup)` — explicit cleanup. Two accepted shapes (the preprocessor validates at compile time and rejects anything else with a typed diagnostic):

  | Shape                  | Generated defer |
  |------------------------|-----------------|
  | `func(T)`              | `defer cleanup(v)` |
  | `func(T) error`        | wrapped defer that `slog.Error`s the close-time err |

  ```go
  q.Open(dial(addr)).DeferCleanup((*Conn).Close)     // func(T)         (Close is void here)
  q.Open(open(path)).DeferCleanup((*os.File).Close)  // func(T) error   (Close returns error)
  ```

  q.Open's `DeferCleanup` is intentionally scoped to the resource it wraps — it does not accept no-arg cleanups (`func()` / `func() error`) or arbitrary call expressions. For cleanups that don't need the resource, write `defer myCleanup()` at the q.Open call site directly. Wrap the cleanup yourself if you need different handling on the close-time error — suppress, retry, or transform.
- `DeferCleanup()` — no args; the preprocessor infers the cleanup from T at compile time.

The signature is `DeferCleanup(cleanup ...any) T` — Go itself can't pick between the two cleanup shapes via overloading, so the parameter is `any` and the rewriter / typecheck pass is what enforces the constraint.

## What `q.Open` does

```go
conn := q.Open(dial(addr)).DeferCleanup((*Conn).Close)
```

rewrites to:

```go
conn, _qErr1 := dial(addr)
if _qErr1 != nil {
    return /* zeros */, _qErr1
}
defer ((*Conn).Close)(conn)
```

On error: bubble, no cleanup registered (nothing was acquired).
On success: register the deferred cleanup so it fires when the enclosing function returns (whether via normal return or via a later bubble).

## `.DeferCleanup()` (zero args) — preprocessor infers the cleanup

For the common case (a `Closer`-shaped resource or a channel), let the preprocessor figure it out:

```go
ch   := q.Open(makeChan()).DeferCleanup()    // → defer close(ch)
file := q.Open(os.Open(path)).DeferCleanup() // → deferred wrapper that slog.Errors any close-time err
db   := q.Open(sql.Open(...)).DeferCleanup() // → same shape (Close() error → slog wrapper)
```

The typecheck pass inspects the resource type T at compile time and dispatches:

| T's shape                   | Generated defer                                            |
|-----------------------------|------------------------------------------------------------|
| channel type (`chan X`, `chan<- X`, `<-chan X`) | `defer close(v)`                       |
| `Close() error` method      | `defer func() { if err := v.Close(); err != nil { slog.Error("q.Open: cleanup returned error", "err", err) } }()` — pass an explicit `func(T) error` wrapper if you need different handling |
| `Close()` method (no return)| `defer v.Close()`                                          |
| anything else               | build error — pass an explicit cleanup or `.NoDeferCleanup()`   |

Auto-cleanup composes with the OpenE shape methods:

```go
file := q.OpenE(os.Open(path)).Wrap("loading config").DeferCleanup()
```

If the type doesn't expose a recognised cleanup shape (no `Close`, not a channel), the preprocessor surfaces a `file:line:col: q: …` diagnostic naming the type and pointing at the two acceptable fixes (explicit cleanup function or `.NoDeferCleanup()`).

## `.NoDeferCleanup()` — opt-in "no cleanup" terminal

Some resources don't need a cleanup at the call site — for example, you might be passing the value off to a long-lived owner that handles teardown elsewhere, or the type genuinely has nothing to release. Spell that intent explicitly with `.NoDeferCleanup()`:

```go
val := q.Open(loadValue(key)).NoDeferCleanup()
// rewrites to:
//     val, _qErr1 := loadValue(key)
//     if _qErr1 != nil { return /* zeros */, _qErr1 }
//     // no defer
```

`.NoDeferCleanup()` shares the bubble path with `.DeferCleanup(...)` — only the success-defer line is omitted. Composes with the OpenE shape methods just like Release does:

```go
val := q.OpenE(loadValue(key)).Wrap("loading").NoDeferCleanup()
```

Why a separate terminal instead of `DeferCleanup(q.NoDeferCleanup)` (a no-op cleanup)? Spelling it as a method makes the intent obvious in code review — "we acquired this and we're not closing it, here's the call that says so" — instead of needing to look up what `q.NoDeferCleanup` does in the docs.

## `.WithScope(scope)` — hand the lifetime to a `*q.Scope`

Routes the cleanup through `scope.Attach*` instead of a function-local `defer`. Use it when the resource needs to outlive the function that opens it — per-request scopes, per-tenant lifetimes, anything where `defer` at this level is too short.

```go
conn := q.Open(dial(addr)).WithScope(scope)              // auto-detect cleanup from T
conn := q.Open(dial(addr)).WithScope(myCleanup, scope)   // explicit cleanup + scope
return conn, nil                                         // safe to return — scope owns the lifetime
```

Two call shapes:

| Args                | Routing                                                                         |
|---------------------|----------------------------------------------------------------------------------|
| `(scope)`           | Auto-detect (chan / `Close()` / `Close() error`), same shapes `DeferCleanup()` infers. Maps to `scope.AttachFn(v, func(){ close(v) })` / `scope.Attach(v)` / `scope.AttachE(v)` respectively. |
| `(cleanup, scope)`  | Explicit cleanup. `func(T)` → `scope.AttachFn`, `func(T) error` → `scope.AttachFnE`. |

If the scope is already closed at attach time, the cleanup fires *eagerly* and `q.ErrScopeClosed` is bubbled — different from `DeferCleanup`'s "always succeeds" success path. The bubble exists because the resource has just been disposed; the caller shouldn't keep using it.

The `args` parameter is `...any` so the source compiles whether the caller passes one (`scope`) or two (`cleanup, scope`); the preprocessor enforces the ordering at build time.

`.WithScope` is mutually exclusive with `.DeferCleanup` and `.NoDeferCleanup` — pick one terminal per `q.Open` call. The resource-escape check is skipped for `.WithScope` calls (the scope is the owner; returning the value is the point).

## Chain methods on `q.OpenE`

All of these return `OpenResultE[T]` so `.DeferCleanup` can still terminate the chain.

| Method                                | Bubbled error                                         |
|---------------------------------------|-------------------------------------------------------|
| `.Err(replacement error)`             | `replacement`                                         |
| `.ErrF(fn func(error) error)`         | `fn(capturedErr)`                                     |
| `.Wrap(msg string)`                   | `fmt.Errorf("<msg>: %w", capturedErr)`                |
| `.Wrapf(format string, args ...any)`  | `fmt.Errorf("<format>: %w", args..., capturedErr)`    |
| `.Catch(fn func(error) (T, error))`   | On recover `(v, nil)` the recovered `v` is what `.DeferCleanup`'s cleanup later fires on |

Example chain:

```go
conn := q.OpenE(dial(addr)).Wrap("dialing").DeferCleanup((*Conn).Close)
```

and with recovery:

```go
conn := q.OpenE(dial(addr)).Catch(func(e error) (*Conn, error) {
    if errors.Is(e, syscall.ECONNREFUSED) {
        return fallbackConn(), nil    // recovered resource feeds DeferCleanup's cleanup
    }
    return nil, e
}).DeferCleanup((*Conn).Close)
```

## LIFO cleanup across multiple Opens

Two `Open`s in sequence register two defers. Go defer semantics are LIFO, so the later-acquired resource is released first — matching the `defer lock.Unlock()` idiom scaled up:

```go
func work(addr, path string) error {
    conn := q.Open(dial(addr)).DeferCleanup((*Conn).Close)
    f    := q.Open(os.Open(path)).DeferCleanup((*os.File).Close)

    // f.Close() runs first (innermost defer), then conn.Close().
    return process(conn, f)
}
```

If `os.Open` fails above, `conn` has already been acquired — its cleanup runs, `f`'s does not.

## Statement forms

Works in every position `q.Try` does:

```go
conn := q.Open(dial()).DeferCleanup(cleanup)                         // define
conn  = q.Open(dial()).DeferCleanup(cleanup)                         // assign
        q.Open(dial()).DeferCleanup(cleanup)                         // discard (side-effect only — cleanup registers)
return  q.Open(dial()).DeferCleanup(cleanup), nil                    // return-position
id   := identify(q.Open(dial()).DeferCleanup(cleanup))               // hoist
```

## Resource-escape detection

A `q.Open(...).DeferCleanup(...)` value is alive *only* until the enclosing function returns — that's when the deferred cleanup fires. Letting the value escape that scope is a use-after-close in waiting. The preprocessor catches the obvious shapes and fails the build with a diagnostic.

Three death events make a binding "dead" from a given source point onward:

1. **`q.Open(...).DeferCleanup(...)` itself** — auto-defers cleanup. Dead from the assign line.
2. **`defer x.Close()`** — explicit user-written defer. Dead from the defer line.
3. **`x.Close()`** — synchronous close. Dead from this point onward.

Note that `close(ch)` and `defer close(ch)` are NOT death events. Receiving from a closed channel is idiomatic (`close(ch); return ch` is a legitimate finite-channel factory), so channels are exempt.

Once dead, the binding cannot escape via:

- `return c`
- `go fn(c)` / `defer fn(c)` (other than the cleanup itself)
- field / global / map / index store: `p.c = c`, `m[k] = c`, etc.
- channel send: `ch <- c`

One-hop alias tracking covers the common `c2 := c; return c2` shape. Deeper indirection (passing through function calls, returning from inner closures) is out of scope — flag-everything-that-might-escape produces too many false positives without a real flow analysis.

`q.Open(...).NoDeferCleanup()` is the explicit "caller takes ownership" form and never makes the binding dead — return it freely.

### `//q:no-escape-check` opt-out

Some test fixtures (notably tests of q.Open's mechanism itself) intentionally factory out a closed resource so the caller can probe its post-close state. Mark such functions with a `//q:no-escape-check` directive:

```go
//q:no-escape-check
func channelAutoInner() (chan int, error) {
    ch := q.Open(makeChan()).DeferCleanup()
    ch <- 7
    return ch, nil
}
```

Real user code shouldn't need this — the patterns it suppresses are bug-shaped in production. The directive exists so we can write tests that verify q.Open's deferred close actually fires.

## See also

- [Design](../design.md#21-bare-bubble) — why `.DeferCleanup` is terminal
