# `q.Lock`

Acquire a lock and register a deferred Unlock in one statement. Statement-only.

## Signature

```go
func Lock(l sync.Locker)
```

Accepts any `sync.Locker` — `*sync.Mutex`, `*sync.RWMutex` (write side), `rwm.RLocker()` (read side), or a user-defined type with `Lock()` / `Unlock()`.

## What `q.Lock` does

```go
func (s *Store) Set(k, v string) {
    q.Lock(&s.mu)
    s.data[k] = v
}
```

rewrites to:

```go
func (s *Store) Set(k, v string) {
    _qLock1 := &s.mu
    _qLock1.Lock()
    defer _qLock1.Unlock()
    s.data[k] = v
}
```

The locker expression is evaluated exactly once and bound to a local — important when you pass something like `rwm.RLocker()` (each call returns a fresh Locker):

```go
q.Lock(s.rwm.RLocker())   // read side — binds once, Locks once, defers matching Unlock
```

## Statement forms

`q.Lock` returns nothing, so it's always a plain expression statement. Using it as a value (`v := q.Lock(...)`) is a Go type error caught by the compiler.

## See also

- [q.Open](open.md) — the general `(T, error) + defer cleanup` pattern; `q.Lock` is a specialisation of it for lockers with no error return.
