# `q.Ok` and `q.OkE`

The comma-ok bubble — for map lookups, type assertions, channel receives, any API that returns `(T, bool)`.

## Signatures

```go
func Ok[T any](v T, ok bool) T
func OkE[T any](v T, ok bool) OkResult[T]

var ErrNotOk = errors.New("q: not ok")
```

Bare `q.Ok` bubbles `q.ErrNotOk` — a package-level sentinel usable via `errors.Is`. `q.OkE` produces a richer error through the chain.

## What `q.Ok` does

Two call-argument shapes are accepted. Single-call form — Go's f(g()) rule spreads the tuple:

```go
v := q.Ok(lookup(key))            // lookup returns (T, bool)
```

Two-arg form — already destructured:

```go
v, ok := table[key]
return q.Ok(v, ok), nil
```

Both rewrite to:

```go
v, _qOk1 := <inner>
if !_qOk1 {
    return /* zeros */, q.ErrNotOk
}
```

## Chain methods on `q.OkE`

There is no source error on the not-ok branch, so `.ErrF` takes a thunk, and `.Wrap` / `.Wrapf` build a fresh error (no `%w`).

| Method                                | Bubbled error                                 |
|---------------------------------------|-----------------------------------------------|
| `.Err(replacement error)`             | `replacement`                                 |
| `.ErrF(fn func() error)`              | `fn()`                                        |
| `.Wrap(msg string)`                   | `errors.New(msg)`                             |
| `.Wrapf(format string, args ...any)`  | `fmt.Errorf(format, args...)`                 |
| `.Catch(fn func() (T, error))`        | Recover with `(v, nil)` or bubble `(_, err)` |

```go
v := q.OkE(lookup(id)).Err(ErrNotFound)
v := q.OkE(lookup(id)).Wrapf("no user %d", id)
v := q.OkE(lookup(id)).Catch(func() (User, error) { return backfill(id) })
```

(Map index `users[id]` returns `(T, bool)` only in the destructure
position `v, ok := users[id]`. As an argument to another call it
returns just `T`, so `q.OkE(users[id])` won't compile — feed it via
a `(T, bool)`-returning function (`lookup(id)` here) or destructure
first and pass `q.OkE(v, ok)` in the two-arg form.)

## Statement forms

Same five positions as `q.Try` — define, assign, discard, return-position, hoist.

## See also

- [q.Recv](recv.md) — same pattern, specialised to channel receives.
- [q.As](as.md) — same pattern, specialised to type assertions.
- [q.NotNil](notnil.md) — the pointer-nil cousin, same vocabulary.
