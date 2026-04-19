# Resources

`q.Open` registers a `defer cleanup(resource)` on the success path — so the acquire/cleanup pair stays flat at the call site instead of spanning four lines.

## Open a connection

```go
func handle(addr string) error {
    conn := q.Open(dial(addr)).Release((*Conn).Close)
    return conn.Serve()
}
```

rewrites to:

```go
func handle(addr string) error {
    conn, _qErr1 := dial(addr)
    if _qErr1 != nil {
        return _qErr1
    }
    defer ((*Conn).Close)(conn)
    return conn.Serve()
}
```

On error: bubble, no cleanup registered (there was nothing to clean up).
On success: `defer (*Conn).Close(conn)` fires when `handle` returns.

## Wrap the error, still register the cleanup

```go
conn := q.OpenE(dial(addr)).Wrap("dialing").Release((*Conn).Close)
```

The `.Wrap` shapes the bubble (`fmt.Errorf("%s: %w", "dialing", err)`); `.Release` is still the terminal and still registers the defer on success.

Same deal with `.Wrapf`, `.Err`, `.ErrF`, `.Catch`:

```go
conn := q.OpenE(dial(addr)).Wrapf("dialing %s", addr).Release((*Conn).Close)
conn := q.OpenE(dial(addr)).Err(ErrConnect).Release((*Conn).Close)
```

## Recover to a fallback resource

If `Catch` recovers with a substitute resource, *that* resource is what the cleanup fires on:

```go
conn := q.OpenE(dial(primary)).Catch(func(e error) (*Conn, error) {
    return dial(fallback)                  // different conn; cleanup still fires on it
}).Release((*Conn).Close)
```

Reading the rewritten output for this case:

```go
conn, _qErr1 := dial(primary)
if _qErr1 != nil {
    var _qRet1 error
    conn, _qRet1 = (func(e error) (*Conn, error) {
        return dial(fallback)
    })(_qErr1)
    if _qRet1 != nil {
        return _qRet1
    }
}
defer ((*Conn).Close)(conn)
```

The same `conn` variable is rebound by the `Catch` fn when it recovers, so the deferred `Close` targets whichever conn ended up being used.

## Multiple resources, LIFO cleanup

```go
func work(addr, path string) error {
    conn := q.Open(dial(addr)).Release((*Conn).Close)
    file := q.Open(os.Open(path)).Release((*os.File).Close)

    return process(conn, file)
    // file.Close runs first (innermost defer), then conn.Close.
}
```

If `os.Open` fails, `conn`'s defer is already registered — its `Close` runs, `file`'s doesn't. Same semantics as hand-written:

```go
conn, err := dial(addr)
if err != nil { return err }
defer conn.Close()

file, err := os.Open(path)
if err != nil { return err }                 // conn.Close fires here
defer file.Close()

return process(conn, file)
```

## Mutexes and other cleanup-only-on-success patterns

`q.Open` isn't limited to things that have a constructor returning `(T, error)`. Any `(T, error)` + cleanup-on-success pattern works. A lock that returns `(*Lock, error)` and a cleanup `(*Lock).Unlock` is a natural fit:

```go
lock := q.Open(acquireLock(key)).Release((*Lock).Unlock)
// lock is held for the rest of this function; Unlock fires on return.
```

## Discard form — acquire for side-effect

If you don't need to reference the resource but want the cleanup to register:

```go
q.Open(tracer.Start(ctx)).Release((*Span).End)
// Span.End runs on return; the span itself is unused.
```

Uncommon but legal — the rewriter binds to a `_qTmpN` temp and wires the defer to it.

## See also

- [API → q.Open](../api/open.md) — the full surface including every chain method
- [Examples → Error shaping](error-shaping.md) — the `Wrap`/`Err`/`Catch` vocabulary applies to `q.OpenE` too
