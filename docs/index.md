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

## The four entries

| Entry                          | Source shape                   | Page                          |
|--------------------------------|--------------------------------|-------------------------------|
| `q.Try` / `q.TryE`             | `(T, error)`                   | [Try](api/try.md)             |
| `q.NotNil` / `q.NotNilE`       | `*T`                           | [NotNil](api/notnil.md)       |
| `q.Check` / `q.CheckE`         | `error` alone (statement only) | [Check](api/check.md)         |
| `q.Open` / `q.OpenE`           | `(T, error)` + defer cleanup   | [Open](api/open.md)           |

Each bare form bubbles on failure. Each `E` variant carries the capture into a `Result` whose chain methods (`Err`, `ErrF`, `Wrap`, `Wrapf`, `Catch`) shape the bubbled error at the call site.

## Where to go next

- [Getting Started](getting-started.md) — install, first build, IDE setup, GOCACHE discipline.
- [Examples → Basic bubbling](examples/basic.md) — smallest runnable programs for Try / NotNil / Check.
- [Examples → Error shaping](examples/error-shaping.md) — Wrap / Wrapf / Err / ErrF / Catch patterns.
- [Examples → Resources](examples/resources.md) — acquire/release with Open, LIFO cleanup, recovery to a fallback.
- [Design](design.md) — the link gate, the rewriter contract, what's recognised, what isn't.

## Status

Experimental — APIs and internals may change. But the public surface is complete: every entry × every chain method × every statement position (define, assign, discard, return-position, and hoist-inside-expression) works end-to-end. Multi-q in one statement composes, including nesting one q.* inside another. Closures and generics are supported.

The only currently-parked shape is multi-LHS where q.* itself produces multiple T values (`v, w := q.Try(call())`) — that needs new runtime helpers; see [TODO #16](https://github.com/GiGurra/q/blob/main/docs/planning/TODO.md#future--parking-lot).
