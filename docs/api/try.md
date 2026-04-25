# `q.Try` and `q.TryE`

The `(T, error)` bubble — the 90% case. Most uses of `q` are one of these two.

## Signatures

```go
func Try[T any](v T, err error) T
func TryE[T any](v T, err error) ErrResult[T]
```

Bare `q.Try` bubbles the captured err unchanged. The chain entry `q.TryE` carries the capture into a `Result` value so a chain method can shape the bubbled error at the call site.

## What `q.Try` does

```go
n := q.Try(strconv.Atoi(s))
```

rewrites to:

```go
n, _qErr1 := strconv.Atoi(s)
if _qErr1 != nil {
    return /* zeros */, _qErr1
}
```

The zeros come from the enclosing function's result list — one `*new(T)` per non-error result position, with the captured err in the final slot. `q.Try` requires the enclosing function's last result to be `error`.

## Chain methods on `q.TryE`

### Terminals

These end the chain by emitting the bubble check; they return `T`.

| Method                                | Bubbled error                                         |
|---------------------------------------|-------------------------------------------------------|
| `.Err(replacement error)`             | `replacement`                                         |
| `.ErrF(fn func(error) error)`         | `fn(capturedErr)`                                     |
| `.Wrap(msg string)`                   | `fmt.Errorf("<msg>: %w", capturedErr)`                |
| `.Wrapf(format string, args ...any)`  | `fmt.Errorf("<format>: %w", args..., capturedErr)` — the format string must be a string literal |
| `.Catch(fn func(error) (T, error))`   | If `fn` returns `(v, nil)` recover with `v`; otherwise bubble the new error |

```go
n := q.TryE(strconv.Atoi(s)).Err(ErrBadInput)
n := q.TryE(strconv.Atoi(s)).ErrF(toDBError)
n := q.TryE(strconv.Atoi(s)).Wrap("parsing")
n := q.TryE(strconv.Atoi(s)).Wrapf("parsing %q", s)
n := q.TryE(strconv.Atoi(s)).Catch(func(e error) (int, error) {
    if errors.Is(e, strconv.ErrSyntax) {
        return 0, nil
    }
    return 0, e
})
```

`errors.Is` and `errors.As` traverse `.Wrap` / `.Wrapf` correctly — the generated `fmt.Errorf` uses `%w`, so the underlying sentinel / typed error is reachable downstream.

### Recovers (chain-continuing)

Two intermediates that recover specific failure modes without ending the chain — they return `ErrResult[T]`, so a terminal still has to follow.

| Method                                          | Effect                                                                                          |
|-------------------------------------------------|-------------------------------------------------------------------------------------------------|
| `.RecoverIs(sentinel error, value T)`           | If `errors.Is(capturedErr, sentinel)`, recover with `value` and clear the err. Else pass through. |
| `.RecoverAs(typedNil error, value T)`           | If `errors.As` succeeds extracting capturedErr into the type carried by `typedNil`, recover.    |

`typedNil` for `RecoverAs` must be a typed-nil literal (`(*MyErr)(nil)` or `MyErrType(nil)`); the rewriter extracts the target type at compile time.

The user's "recover this case, wrap everything else" pattern collapses into a one-liner:

```go
// Before
n := q.TryE(strconv.Atoi(s)).Catch(func(e error) (int, error) {
    if errors.Is(e, strconv.ErrSyntax) {
        return 0, nil
    }
    return 0, fmt.Errorf("parsing %q: %w", s, e)
})

// After
n := q.TryE(strconv.Atoi(s)).
    RecoverIs(strconv.ErrSyntax, 0).
    Wrapf("parsing %q", s)
```

Multiple Recover steps may be chained; each runs only if no earlier step has already recovered:

```go
n := q.TryE(call()).
    RecoverIs(ErrA, 1).
    RecoverIs(ErrB, 2).
    RecoverAs((*MyErr)(nil), 3).
    Wrap("loading")
```

Standalone use (RecoverIs / RecoverAs as the chain's last step) is rejected by the preprocessor — the bubble would be silently swallowed otherwise. Always pair them with a terminal.

### Standalone runtime helpers

Not chain methods, but they pair naturally with `.Catch`:

| Helper                              | Returns                                                |
|-------------------------------------|--------------------------------------------------------|
| `q.Const[T any](v T) func(error) (T, error)` | Always recovers to `v`. Useful as `.Catch(q.Const(0))`. |

## Statement forms

Every position a plain Go expression fits in works for `q.Try` and `q.TryE`:

```go
v := q.Try(call())                                // define
v  = q.Try(call())                                // assign (incl. obj.field, arr[i])
     q.Try(call())                                // discard (ExprStmt)
return q.Try(call()), nil                         // return-position
x := f(q.Try(call()))                             // nested in another expression (hoist)
return q.Try(a()) * q.Try(b()) / q.Try(c()), nil  // multiple q.*s, short-circuit on earlier failures
x := q.Try(Foo(q.Try(Bar())))                     // q.* nested inside another q.*
```

Multi-LHS where `q.Try` itself is the multi-result producer (`v, w := q.Try(call())`) is parked — see [TODO #16](https://github.com/GiGurra/q/blob/main/docs/planning/TODO.md#future--parking-lot).

## Closures and generics

`q.Try` inside a `func(...) { ... }` uses the closure's own result list for the bubble, not the enclosing FuncDecl's. It also works inside generic functions and on methods of generic receivers — zero values are spelled `*new(T)` which is universal.

## See also

- [q.Check](check.md) — for functions returning just `error`
- [q.NotNil](notnil.md) — for nil-pointer bubbles
- [Design](../design.md) — rewriter contract, link gate, phasing
