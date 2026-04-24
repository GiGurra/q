# `q.Assert`

Runtime assertion — panic with a call-site-prefixed message when `cond` is false. Statement-only.

## Signature

```go
func Assert(cond bool, msg ...string)
```

The message is optional. Format-string needs are up to the caller (via `fmt.Sprintf` on the message).

## What `q.Assert` does

```go
q.Assert(len(buf) >= 16, "header too short")
```

rewrites to (say, `codec.go` line 88):

```go
if !(len(buf) >= 16) {
    panic("q.Assert failed codec.go:88: header too short")
}
```

Without a message:

```go
q.Assert(len(buf) >= 16)
// → if !(len(buf) >= 16) { panic("q.Assert failed codec.go:88") }
```

## When to use

- Internal invariants — preconditions that should never fail for correctly-written callers.
- Type-safety gaps where you've narrowed a type-asserted value but the compiler can't prove the narrowing.
- "This arithmetic can't overflow because [...]" — document the reasoning, catch the counter-example.

Not for user-facing input validation — return an error for that.

## Statement forms

Stmt-only.

## Not yet supported

Build-tag compile-out (`-tags=qrelease` to strip all Asserts) is tracked but not shipped. Current version always emits the check.

## See also

- [q.Unreachable](todo.md) — for invariants that don't need a conditional.
- [q.TODO](todo.md) — for unfinished branches you want to find at runtime.
