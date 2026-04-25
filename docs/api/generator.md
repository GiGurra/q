# Generators: `q.Generator` / `q.Yield`

Go 1.23 added `iter.Seq[T]` — pull-based iteration via a callback. It works, but the boilerplate is awkward: you write a function whose parameter is a `func(T) bool` and you have to remember to early-return when the consumer signals stop.

`q.Generator` is preprocessor-rewritten sugar on top of `iter.Seq`. The body is a plain `func()`; you call `q.Yield(v)` at each emission point. The preprocessor:

1. Rewrites every `q.Yield(v)` inside the body into `if !yield(v) { return }`.
2. Wraps the body as `iter.Seq[T](func(yield func(T) bool) { ... })`.

The result is a real `iter.Seq[T]` — interop with `for v := range g` and any other `iter.Seq` consumer is automatic.

```go
fibs := q.Generator[int](func() {
    a, b := 0, 1
    for {
        q.Yield(a)
        a, b = b, a+b
    }
})

for v := range fibs {
    if v > 100 { break }
    fmt.Println(v)
}
```

## Surface

```go
func Generator[T any](body func()) iter.Seq[T]
func Yield[T any](v T)
```

The type parameter on `Generator[T]` is **required** at the call site — Go cannot infer a generic type argument that appears only in the result type, so `q.Generator(func() {…})` would not type-check.

`q.Yield` is recognised only inside a `q.Generator` body. Outside, the runtime stub panics — same loud-failure pattern as the rest of `q`.

## What it rewrites to

```go
// Before:
fibs := q.Generator[int](func() {
    a, b := 0, 1
    for {
        q.Yield(a)
        a, b = b, a+b
    }
})

// After (effectively):
fibs := iter.Seq[int](func(yield func(int) bool) {
    a, b := 0, 1
    for {
        if !yield(a) { return }
        a, b = b, a+b
    }
})
```

Zero runtime overhead beyond what hand-written `iter.Seq` would have.

## Termination

A `q.Generator` body terminates when:

- **The consumer stops ranging.** `if !yield(v) { return }` exits the body. Triggered by `break`, an early `return` from the surrounding function, or any other escape from the `for v := range g` loop.
- **The body returns naturally.** Either the body has emitted everything and falls off the end, or it returns explicitly. Subsequent attempts to range produce no further values.

```go
letters := q.Generator[string](func() {
    for _, s := range []string{"a", "b", "c"} {
        q.Yield(s)
    }
    // body returns here; sequence ends
})
for s := range letters {
    fmt.Println(s)
}
```

## Nested closures

`q.Yield` calls inside nested closures are also rewritten — but the early `return` exits the *innermost* enclosing function, not the generator body. This matches the behaviour of writing `iter.Seq` by hand. If you need to early-exit the generator from a nested goroutine, use a flag plus an explicit `return` in the outer body:

```go
g := q.Generator[int](func() {
    var stop atomic.Bool
    go func() {
        time.Sleep(time.Second)
        stop.Store(true) // signal the body to stop
    }()
    for i := 0; ; i++ {
        if stop.Load() { return }
        q.Yield(i)
    }
})
```

## When to reach for `q.Coro` instead

`q.Generator` is one-way: body emits, consumer pulls. If the consumer needs to feed values *back* into the body — a stateful conversation rather than a stream — use [`q.Coro`](coro.md). q.Coro is heavier (a goroutine + two channels per coroutine), but it's the right shape for bidirectional cooperation.

## See also

- [`q.Coro`](coro.md) — bidirectional, runtime-only coroutines.
- Go's [`iter`](https://pkg.go.dev/iter) package — the consumer side of `iter.Seq`.
