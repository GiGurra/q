# `q.TODO` and `q.Unreachable`

Panic with a call-site-prefixed message. Rust's `todo!()` / `unreachable!()`. Statement-only.

## Signatures

```go
func TODO(msg ...string)
func Unreachable(msg ...string)
```

Both variadic — the message is optional.

## What `q.TODO` does

```go
func parseV2(s string) (Config, error) {
    q.TODO("schema v2 parser")
}
```

rewrites to (say, `parser.go` line 42):

```go
func parseV2(s string) (Config, error) {
    panic("q.TODO parser.go:42: schema v2 parser")
}
```

Without a message:

```go
q.TODO()
// → panic("q.TODO parser.go:42")
```

## `q.Unreachable`

Same shape, different message prefix. Use where the code path *shouldn't* happen even given arbitrary inputs — e.g. the default branch of an exhaustive switch:

```go
switch tag {
case "a", "b", "c":
    return handle(tag)
default:
    q.Unreachable("tag was %q", tag)   // oh no, use %v-style formatting?
}
```

(No, the current `q.Unreachable(msg)` takes a string, not a format string. Use `fmt.Sprintf` on the caller side for formatted messages.)

## Statement forms

Stmt-only — both return nothing.

## See also

- [q.Assert](assert.md) — for conditional invariants.
