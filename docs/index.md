# q

[![CI Status](https://github.com/GiGurra/q/actions/workflows/ci.yml/badge.svg)](https://github.com/GiGurra/q/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/GiGurra/q)](https://goreportcard.com/report/github.com/GiGurra/q)

The question-mark operator for Go. Each `q.Try(...)` / `q.NotNil(...)` / chain call is rewritten at compile time into the conventional `if err != nil { return …, err }` shape — call sites read flat, generated code is identical to hand-written error forwarding, runtime overhead is zero.

```go
// Without q
func loadUser(id int) (User, error) {
    row, err := db.Query(id)
    if err != nil {
        return User{}, fmt.Errorf("loading user %d: %w", id, err)
    }
    user, err := parse(row)
    if err != nil {
        return User{}, err
    }
    return user, nil
}

// With q
func loadUser(id int) (User, error) {
    row  := q.TryE(db.Query(id)).Wrapf("loading user %d", id)
    user := q.Try(parse(row))
    return user, nil
}
```

Signatures stay plain Go. There are no special types you have to learn, no closures, no panic/recover. `gopls` and `go vet` see ordinary code, so IDE checking stays green — but **building without the preprocessor fails loudly at link time**, so you cannot silently ship a binary that bypasses the rewrite.

The withdrawn Go [`try` proposal](https://github.com/golang/go/issues/32437) is the same idea, delivered as a preprocessor instead of a language change. You opt in per-module via `-toolexec`.

## The full surface

### Bare bubble — pass through, propagate unchanged

```go
n := q.Try(strconv.Atoi(s))      // (T, error) → bubble err
u := q.NotNil(table[id])         // (*T)       → bubble q.ErrNil
```

### Chain — custom error handling at the call site

For `(T, error)` via `q.TryE`:

```go
n := q.TryE(strconv.Atoi(s)).Err(ErrBadInput)                   // replace with constant error
n := q.TryE(strconv.Atoi(s)).ErrF(toDBError)                    // transform: fn(err) error
n := q.TryE(strconv.Atoi(s)).Wrap("parsing")                    // fmt.Errorf("parsing: %w", err)
n := q.TryE(strconv.Atoi(s)).Wrapf("parsing %q", s)             // fmt.Errorf("parsing %q: %w", s, err)
n := q.TryE(strconv.Atoi(s)).Catch(func(e error) (int, error) { // transform OR recover
    if errors.Is(e, strconv.ErrSyntax) {
        return 0, nil                                           //   recover with default
    }
    return 0, fmt.Errorf("parsing %q: %w", s, e)                //   bubble new error
})
```

For `*T` via `q.NotNilE`:

```go
u := q.NotNilE(table[id]).Err(ErrNotFound)                       // replace with constant error
u := q.NotNilE(table[id]).ErrF(func() error { ... })             // computed error (thunk)
u := q.NotNilE(table[id]).Wrap("user not found")                 // errors.New(msg)
u := q.NotNilE(table[id]).Wrapf("no user %d", id)                // fmt.Errorf(format, args...)
u := q.NotNilE(table[id]).Catch(func() (*User, error) { ... })   // computed value OR error
```

`Catch` is the most powerful method: returning `(value, nil)` recovers and uses that value in place of the bubble; returning `(zero, err)` bubbles the new error. The other methods are conveniences for the common shapes.

### Statement positions

Every helper works in three positions:

```go
v := q.Try(call())   // define   — declare a fresh variable
v  = q.Try(call())   // assign   — update an existing one (incl. obj.field, arr[i])
     q.Try(call())   // discard  — bubble on err, drop the value
```

The discard form is useful for "must succeed" calls where the return value isn't needed (e.g. `q.NotNil(somePtr)` as a precondition assertion).

## Next

- [Getting Started](getting-started.md) — install, first build, IDE setup, GOCACHE discipline.
- [Design](design.md) — the link gate, the rewriter contract, what's recognised, what isn't.

## Status

Experimental — APIs and internals may change.

**What works.** Bare `q.Try` and `q.NotNil`. Every chain method on `q.TryE` and `q.NotNilE` (`Err`, `ErrF`, `Catch`, `Wrap`, `Wrapf`). Every statement form (define, assign, discard).

**Currently rejected with a diagnostic** (planned, but not yet supported): return-position (`return q.Try(call())`), nested-in-call (`f(q.Try(call()))`), multi-LHS. Half-rewritten code never happens silently.
