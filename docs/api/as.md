# `q.As` and `q.AsE`

Type assertion with a bubble on failure — the comma-ok pattern specialised to `v, ok := x.(T)`.

## Signatures

```go
func As[T any](x any) T
func AsE[T any](x any) OkResult[T]

var ErrBadTypeAssert = errors.New("q: type assertion failed")
```

The type parameter `T` must be supplied explicitly — Go can't infer it from the single `any` argument. Both bare and chain forms use `q.As[T](x)` / `q.AsE[T](x)`.

## What `q.As` does

```go
n := q.As[int](x)
```

rewrites to:

```go
n, _qOk1 := (x).(int)
if !_qOk1 {
    return /* zeros */, q.ErrBadTypeAssert
}
```

## Chain methods on `q.AsE[T]`

Same as [`q.OkE`](ok.md#chain-methods-on-qoke). Useful example:

```go
admin := q.AsE[Admin](user).Wrapf("%T is not an admin", user)
```

## Statement forms

Same five positions as `q.Try`.

## See also

- [q.Ok](ok.md) — the general comma-ok shape this specialises.
- [q.Recv](recv.md) — the channel-receive cousin.
