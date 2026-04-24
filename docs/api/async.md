# `q.Async`, `q.Await`, `q.AwaitE`

JavaScript-flavoured promises on top of goroutines + channels. `q.Async` spawns the work, `q.Await` / `q.AwaitE` pull the result with a Try-shaped bubble.

## Signatures

```go
type Future[T any] struct { /* ... */ }

func Async[T any](fn func() (T, error)) Future[T]    // runtime helper (NOT rewritten)
func AwaitRaw[T any](f Future[T]) (T, error)         // runtime helper (NOT rewritten)

func Await[T any](f Future[T]) T                     // rewritten like q.Try
func AwaitE[T any](f Future[T]) ErrResult[T]         // rewritten like q.TryE
```

`q.Async` and `q.AwaitRaw` are plain runtime functions — you can call them directly. The preprocessor only rewrites `q.Await` and `q.AwaitE`.

## What `q.Await` does

```go
f := q.Async(fetchUser)
u := q.Await(f)
```

rewrites (the second line) to:

```go
u, _qErr1 := q.AwaitRaw(f)
if _qErr1 != nil {
    return /* zeros */, _qErr1
}
```

`q.Async` runs `fetchUser()` in a goroutine and stashes the `(v, err)` result in a buffered channel. `q.AwaitRaw` blocks on that channel. Every Future can be awaited exactly once.

## Chain methods on `q.AwaitE`

Reuses the `ErrResult` type, so the vocabulary is exactly the same as [`q.TryE`](try.md#chain-methods-on-qtrye) — Err / ErrF / Wrap / Wrapf / Catch with the full `%w`-preserving shapes.

```go
u := q.AwaitE(f).Wrapf("fetching user %d", id)
u := q.AwaitE(f).Catch(func(e error) (User, error) { return anonUser(), nil })
```

## Fan-out / fan-in

```go
futures := make([]q.Future[int], len(urls))
for i, url := range urls {
    futures[i] = q.Async(func() (int, error) { return fetchSize(url) })
}

total := 0
for _, f := range futures {
    total += q.Await(f)
}
```

## Statement forms

`q.Await` / `q.AwaitE` work in every position `q.Try` does.

## See also

- [q.Try](try.md) — the underlying bubble shape.
