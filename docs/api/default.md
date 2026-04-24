# `q.Default` and `q.DefaultE`

Swallow the error, substitute a fallback. The only family in `q` that doesn't bubble — reach for it deliberately when a sensible default exists and you don't want to propagate the failure.

## Signatures

```go
func Default[T any](v T, err error, fallback T) T
func DefaultE[T any](v T, err error, fallback T) DefaultResult[T]

func (DefaultResult[T]) When(pred func(error) bool) T
```

## What `q.Default` does

Two call-argument shapes are accepted. Single-call form:

```go
n := q.Default(strconv.Atoi(s), -1)
```

Three-arg (pre-destructured) form:

```go
v, err := strconv.Atoi(s)
n := q.Default(v, err, -1)
```

Both rewrite to:

```go
n, _qErr1 := <inner>
if _qErr1 != nil {
    n = -1
}
```

No early return — execution continues past the Default site. If the enclosing function returns an `error`, Default won't touch it.

## `q.DefaultE.When(pred)`

Conditional fallback — the predicate decides. Matching errors fall back, non-matching errors bubble unchanged.

```go
n := q.DefaultE(load(), 0).When(func(e error) bool {
    return errors.Is(e, io.EOF)
})
// EOF → 0. Any other error bubbles to the enclosing function's err return.
```

For the bubble path (when the predicate returns false), `DefaultE` needs the enclosing function to have a terminal `error` return, the same as `q.Try`.

## Statement forms

All five positions work for bare `q.Default`. `q.DefaultE.When(pred)` is the same but requires an error return for the non-matching branch.

## Not supported (yet)

Shaping the bubble via `.Wrap` / `.ErrF` after `.When(pred)` — the chain currently terminates at `.When`. If you need that, compose with `q.TryE` downstream.

## See also

- [q.Try](try.md) — for when you *do* want to bubble.
- [q.Recover](recover.md) — for wholesale panic-to-error at the function level.
