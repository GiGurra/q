# Basic bubbling

The simplest ways to bubble errors, nils, and error-only failures.

## `q.Try` on a `(T, error)`

```go
package main

import (
    "fmt"
    "strconv"

    "github.com/GiGurra/q/pkg/q"
)

func parseDouble(s string) (int, error) {
    n := q.Try(strconv.Atoi(s))
    return n * 2, nil
}

func main() {
    n, err := parseDouble("21")
    fmt.Println(n, err)             // 42 <nil>

    n, err = parseDouble("abc")
    fmt.Println(n, err)             // 0 strconv.Atoi: parsing "abc": invalid syntax
}
```

What `q.Try(strconv.Atoi(s))` does: if `strconv.Atoi` returns a non-nil error, `parseDouble` returns `(0, thatErr)` immediately. On success, `n` is the parsed int and execution continues.

## `q.NotNil` on a `*T`

```go
func greet(names map[int]*string, id int) (string, error) {
    name := q.NotNil(names[id])      // bubble q.ErrNil if the id isn't in the map
    return "hi " + *name, nil
}
```

Bubble error is `q.ErrNil`, a package-level sentinel you can `errors.Is` against downstream:

```go
_, err := greet(nil, 42)
fmt.Println(errors.Is(err, q.ErrNil))   // true
```

## `q.Check` on a function returning just `error`

```go
func shutdown(db *sql.DB) error {
    q.Check(db.Ping())                   // bubble db.Ping()'s error unchanged
    q.Check(db.Close())                  // bubble db.Close()'s error
    return nil
}
```

`q.Check` is always an expression statement — it has no value. You can't write `v := q.Check(...)`, and that's enforced at compile time by the Go type checker before `q`'s rewriter ever sees the code.

## Statement positions

Every value-producing helper works in five positions:

```go
v := q.Try(call())                      // define — fresh variable
v  = q.Try(call())                      // assign — existing variable or addressable target
     q.Try(call())                      // discard — side-effect only (bubble runs)
return q.Try(call()), nil               // return-position
x := f(q.Try(call()))                   // hoist — nested inside any expression
```

For `q.Check`, only the discard form exists (it returns nothing).

## Multiple q.* in one statement

```go
func area(w, h string) (int, error) {
    return q.Try(strconv.Atoi(w)) * q.Try(strconv.Atoi(h)), nil
}
```

Each `q.Try` binds to its own temp and has its own bubble check — a failing first `Atoi` short-circuits (the second never runs).

## See also

- [API → q.Try](../api/try.md)
- [API → q.NotNil](../api/notnil.md)
- [API → q.Check](../api/check.md)
- [Examples → Error shaping](error-shaping.md) — Wrap, Wrapf, Err, ErrF, Catch
- [Examples → Resources](resources.md) — q.Open for defer-on-success cleanup
