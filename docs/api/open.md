# `q.Open` and `q.OpenE`

Resource acquisition with defer-on-success cleanup — the `(T, error)` bubble plus a registered `defer cleanup(resource)` on the success path.

## Signatures

```go
func Open[T any](v T, err error) OpenResult[T]
func OpenE[T any](v T, err error) OpenResultE[T]

func (OpenResult[T])  Release(cleanup func(T)) T
func (OpenResult[T])  NoRelease() T
func (OpenResultE[T]) Release(cleanup func(T)) T
func (OpenResultE[T]) NoRelease() T
```

`.Release(cleanup)` and `.NoRelease()` are both terminals — what actually returns `T`. `q.Open(v, err)` on its own returns an `OpenResult[T]` that exposes nothing else; you always chain one of the two terminals onto it. (Why a method, not an extra arg: Go's multi-return spread only fires when the multi-return call is the sole argument, so `q.Open(call(), cleanup)` won't compile. The terminal-method shape side-steps that.)

## What `q.Open` does

```go
conn := q.Open(dial(addr)).Release((*Conn).Close)
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

## `.NoRelease()` — opt-in "no cleanup" terminal

Some resources don't need a cleanup at the call site — for example, you might be passing the value off to a long-lived owner that handles teardown elsewhere, or the type genuinely has nothing to release. Spell that intent explicitly with `.NoRelease()`:

```go
val := q.Open(loadValue(key)).NoRelease()
// rewrites to:
//     val, _qErr1 := loadValue(key)
//     if _qErr1 != nil { return /* zeros */, _qErr1 }
//     // no defer
```

`.NoRelease()` shares the bubble path with `.Release(...)` — only the success-defer line is omitted. Composes with the OpenE shape methods just like Release does:

```go
val := q.OpenE(loadValue(key)).Wrap("loading").NoRelease()
```

Why a separate terminal instead of `Release(q.NoRelease)` (a no-op cleanup)? Spelling it as a method makes the intent obvious in code review — "we acquired this and we're not closing it, here's the call that says so" — instead of needing to look up what `q.NoRelease` does in the docs.

## Chain methods on `q.OpenE`

All of these return `OpenResultE[T]` so `.Release` can still terminate the chain.

| Method                                | Bubbled error                                         |
|---------------------------------------|-------------------------------------------------------|
| `.Err(replacement error)`             | `replacement`                                         |
| `.ErrF(fn func(error) error)`         | `fn(capturedErr)`                                     |
| `.Wrap(msg string)`                   | `fmt.Errorf("<msg>: %w", capturedErr)`                |
| `.Wrapf(format string, args ...any)`  | `fmt.Errorf("<format>: %w", args..., capturedErr)`    |
| `.Catch(fn func(error) (T, error))`   | On recover `(v, nil)` the recovered `v` is what `.Release`'s cleanup later fires on |

Example chain:

```go
conn := q.OpenE(dial(addr)).Wrap("dialing").Release((*Conn).Close)
```

and with recovery:

```go
conn := q.OpenE(dial(addr)).Catch(func(e error) (*Conn, error) {
    if errors.Is(e, syscall.ECONNREFUSED) {
        return fallbackConn(), nil    // recovered resource feeds Release's cleanup
    }
    return nil, e
}).Release((*Conn).Close)
```

## LIFO cleanup across multiple Opens

Two `Open`s in sequence register two defers. Go defer semantics are LIFO, so the later-acquired resource is released first — matching the `defer lock.Unlock()` idiom scaled up:

```go
func work(addr, path string) error {
    conn := q.Open(dial(addr)).Release((*Conn).Close)
    f    := q.Open(os.Open(path)).Release((*os.File).Close)

    // f.Close() runs first (innermost defer), then conn.Close().
    return process(conn, f)
}
```

If `os.Open` fails above, `conn` has already been acquired — its cleanup runs, `f`'s does not.

## Statement forms

Works in every position `q.Try` does:

```go
conn := q.Open(dial()).Release(cleanup)                         // define
conn  = q.Open(dial()).Release(cleanup)                         // assign
        q.Open(dial()).Release(cleanup)                         // discard (side-effect only — cleanup registers)
return  q.Open(dial()).Release(cleanup), nil                    // return-position
id   := identify(q.Open(dial()).Release(cleanup))               // hoist
```

## See also

- [Examples → Resources](../examples/resources.md)
- [Design](../design.md#21-bare-bubble) — why `.Release` is terminal
