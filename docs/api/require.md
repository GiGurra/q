# `q.Require`

Runtime precondition — bubble an error to the enclosing function's `error` return when `cond` is false. Statement-only.

## Signature

```go
func Require(cond bool, msg ...string)
```

The message is optional. The bubbled error always carries the call-site `file:line` prefix; if a message is supplied it is appended after a `: ` separator. Format-string needs are up to the caller (use `fmt.Sprintf` or string concatenation).

## What `q.Require` does

```go
q.Require(len(buf) >= 16, "header too short")
```

rewrites to (say, `codec.go` line 88, in a function returning `error`):

```go
if !(len(buf) >= 16) {
    return errors.New("q.Require failed codec.go:88: " + ("header too short"))
}
```

In a function returning `(T, error)`:

```go
if !(len(buf) >= 16) {
    return *new(T), errors.New("q.Require failed codec.go:88: " + ("header too short"))
}
```

Without a message:

```go
q.Require(len(buf) >= 16)
// → if !(len(buf) >= 16) { return …, errors.New("q.Require failed codec.go:88") }
```

## Why a bubble, not a panic

q's purpose is to make error-returning code flat — not to spawn panics through the call graph. A failed precondition is just another way the call cannot succeed, so it propagates the same way every other failure does. That keeps the calling code's `if err != nil` (or `q.Try(...)`) path uniform: the caller doesn't need a `defer recover()` to find out a precondition didn't hold.

If you genuinely want to crash on a violated invariant — "this branch is unreachable" or "we got somewhere we shouldn't" — use [`q.Unreachable`](todo.md) or [`q.TODO`](todo.md). Those exist precisely for the cases where panicking is the correct response.

## When to use

- Runtime preconditions on user-facing inputs (length, range, non-empty, etc.).
- Defensive checks at API boundaries where the caller's contract should be enforced before downstream work begins.
- Cheap invariant checks that catch a class of bug at the closest possible point to where it manifests.

For unit-test-style assertions inside test code, use the standard testing helpers — `q.Require` is for production paths.

## Statement forms

Stmt-only. The enclosing function must have at least one return slot (the last must be `error`); the bubble has nowhere to go otherwise.

## Not yet supported

- A chain variant `q.RequireE(cond).Wrap(…)` / `.Err(…)` / `.ErrF(…)` for shaping the bubbled error. The current bare form constructs the error from a literal+message; if you need a sentinel or wrapped error, write the conditional explicitly today and file a request.
- Build-tag compile-out (`-tags=qrelease` to strip all Requires) is tracked but not shipped. Current version always emits the check.

## See also

- [q.Unreachable](todo.md) — panic for invariants that should be unreachable.
- [q.TODO](todo.md) — panic for unfinished branches you want to find at runtime.
- [q.Check](check.md) — bubble on an `error`-only call (`db.Ping`, `validate(x)`).
