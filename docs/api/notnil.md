# `q.NotNil` and `q.NotNilE`

The `*T` bubble — when you have a pointer expression that might be nil, bubble if it is.

## Signatures

```go
func NotNil[T any](p *T) *T
func NotNilE[T any](p *T) NilResult[T]

var ErrNil = errors.New("q: nil value")
```

Bare `q.NotNil` bubbles `q.ErrNil` — a package-level sentinel you can `errors.Is` against downstream. Reach for `q.NotNilE` to produce a richer error.

## What `q.NotNil` does

```go
u := q.NotNil(table[id])
```

rewrites to:

```go
u := table[id]
if u == nil {
    return /* zeros */, q.ErrNil
}
```

Because there is no source error to forward, the chain's `.ErrF` takes a thunk (`func() error`) rather than a transformer, and `.Wrap` builds `errors.New(msg)` rather than a `%w`-wrapping `fmt.Errorf`.

## Chain methods on `q.NotNilE`

All methods are terminal — they return `*T`.

| Method                                | Bubbled error                                 |
|---------------------------------------|-----------------------------------------------|
| `.Err(replacement error)`             | `replacement`                                 |
| `.ErrF(fn func() error)`              | `fn()` — no source error to pass in           |
| `.Wrap(msg string)`                   | `errors.New(msg)`                             |
| `.Wrapf(format string, args ...any)`  | `fmt.Errorf(format, args...)` — no `%w` appended; the format is the complete message |
| `.Catch(fn func() (*T, error))`       | If `fn` returns `(p, nil)` recover with `p`; otherwise bubble the new error |

```go
u := q.NotNilE(table[id]).Err(ErrNotFound)
u := q.NotNilE(table[id]).ErrF(func() error { return fmt.Errorf("no user %d", id) })
u := q.NotNilE(table[id]).Wrap("user not found")
u := q.NotNilE(table[id]).Wrapf("no user %d", id)
u := q.NotNilE(table[id]).Catch(func() (*User, error) {
    return backfill(id)
})
```

## Statement forms

Same five positions as `q.Try` — define, assign, discard, return-position, and hoist. The `discard` form is particularly useful as a precondition assertion:

```go
q.NotNil(somePtr)                  // fail loudly and early if nil
// ... use somePtr.Field freely below
```

## See also

- [Examples → Basic bubbling](../examples/basic.md)
- [Examples → Error shaping](../examples/error-shaping.md)
- [q.Try](try.md) — for `(T, error)` values
- [Design](../design.md#2-the-user-facing-surface) — why the two families split
