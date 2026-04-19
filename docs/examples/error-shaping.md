# Error shaping

The `E`-suffixed entries carry the capture into a `Result` value whose chain methods decide how the bubble is shaped.

## Wrap with a literal message

```go
user := q.TryE(loadUser(id)).Wrap("loading user")
```

rewrites to:

```go
user, _qErr1 := loadUser(id)
if _qErr1 != nil {
    return zero, fmt.Errorf("%s: %w", "loading user", _qErr1)
}
```

`errors.Is` and `errors.As` traverse the `%w` wrap — downstream consumers can still find the underlying sentinel / typed error:

```go
_, err := fetch(ctx, id)
if errors.Is(err, sql.ErrNoRows) {
    // …
}
```

## Wrapf with format args

```go
user := q.TryE(loadUser(id)).Wrapf("loading user %d from %s", id, db.Name())
```

rewrites to `fmt.Errorf("loading user %d from %s: %w", id, db.Name(), _qErr1)`. The format string **must be a literal** — the rewriter splices `: %w` into it at compile time.

## Replace with a constant error

```go
func parsePort(s string) (int, error) {
    n := q.TryE(strconv.Atoi(s)).Err(ErrBadPort)
    return n, nil
}
```

The original `strconv.Atoi` error is discarded; callers see exactly `ErrBadPort`.

## Transform with `ErrF`

```go
func load(id int) (Row, error) {
    row := q.TryE(db.Query(id)).ErrF(toDBError)
    return row, nil
}

func toDBError(err error) error {
    if errors.Is(err, sql.ErrNoRows) {
        return ErrNotFound
    }
    return fmt.Errorf("db: %w", err)
}
```

`ErrF` is the full-flexibility version when `Wrap`/`Wrapf` isn't enough.

## Recover with `Catch`

`Catch` is the union of "transform the error" and "substitute a fallback value":

```go
func parseOrZero(s string) (int, error) {
    n := q.TryE(strconv.Atoi(s)).Catch(func(e error) (int, error) {
        if errors.Is(e, strconv.ErrSyntax) {
            return 0, nil                    // recovered — bubble is skipped, 0 is used
        }
        return 0, fmt.Errorf("parsing %q: %w", s, e)   // bubble the shaped error
    })
    return n, nil
}
```

Returning `(value, nil)` from `fn` short-circuits the bubble and uses the value as if the original call had succeeded. Returning `(_, err)` bubbles `err` instead of the original.

## Error-only variant: `q.CheckE.Catch` with suppression

For `error`-only calls, `Catch`'s fn signature is `func(error) error` — `nil` suppresses the bubble entirely:

```go
func close(f *os.File) error {
    q.CheckE(f.Close()).Catch(func(e error) error {
        if errors.Is(e, os.ErrClosed) {
            return nil                  // already closed — not a real error
        }
        return e                        // bubble everything else
    })
    return nil
}
```

## `q.NotNilE` — no source error to transform

Because the nil branch has no captured error, the chain methods differ slightly:

- `.ErrF` takes `func() error` (a thunk, no `error` arg).
- `.Wrap(msg)` uses `errors.New(msg)` (no `%w` to splice).
- `.Wrapf(format, args...)` uses `fmt.Errorf(format, args...)` — the full message, no `%w` appended.
- `.Catch(fn)` — `fn` is `func() (*T, error)`; same recover-or-bubble semantics.

```go
user := q.NotNilE(table[id]).ErrF(func() error {
    return fmt.Errorf("no user %d (requested by %s)", id, requester)
})
```

## See also

- [API → q.Try](../api/try.md#chain-methods-on-q-trye)
- [API → q.NotNil](../api/notnil.md#chain-methods-on-q-notnile)
- [API → q.Check](../api/check.md#chain-methods-on-q-checke)
- [Examples → Basic bubbling](basic.md) — bare forms without shaping
- [Examples → Resources](resources.md) — error shaping on `q.OpenE`
