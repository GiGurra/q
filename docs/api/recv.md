# `q.Recv` and `q.RecvE`

Channel receive with a bubble on close — the comma-ok pattern specialised to `v, ok := <-ch`.

## Signatures

```go
func Recv[T any](ch <-chan T) T
func RecvE[T any](ch <-chan T) OkResult[T]

var ErrChanClosed = errors.New("q: channel closed")
```

Bare `q.Recv` bubbles `q.ErrChanClosed`. `q.RecvE` shares the `OkResult` type with `q.OkE`, so the chain vocabulary is identical.

## What `q.Recv` does

```go
msg := q.Recv(ch)
```

rewrites to:

```go
msg, _qOk1 := <-(ch)
if !_qOk1 {
    return /* zeros */, q.ErrChanClosed
}
```

The channel expression is parenthesised so internal operators bind correctly under the leading `<-`.

## Chain methods on `q.RecvE`

Same as [`q.OkE`](ok.md#chain-methods-on-qoke) — Err / ErrF / Wrap / Wrapf / Catch with `errors.New` and `fmt.Errorf` without `%w` (there is no captured source error on a closed channel).

```go
msg := q.RecvE(inbox).Wrap("reading inbox")
msg := q.RecvE(inbox).Err(ErrPipelineClosed)
msg := q.RecvE(inbox).Catch(func() (Msg, error) { return Msg{sentinel: true}, nil })
```

## Statement forms

Same five positions as `q.Try`.

## See also

- [q.Ok](ok.md) — the general comma-ok shape this specialises.
- [q.As](as.md) — the type-assertion cousin.
