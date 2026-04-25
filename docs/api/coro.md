# Bidirectional coroutines: `q.Coro`

Go has goroutines (concurrency, separate stacks) and `iter.Seq` since Go 1.23 (pull-based iteration with a one-way `yield` callback). It does not have full coroutines: bidirectional, suspendable functions where the caller can pass values *into* and pull values *out of* a paused computation.

`q.Coro` wraps a goroutine + two channels into a Lua / Python-`generator.send`-style API:

```go
doubler := q.Coro(func(in <-chan int, out chan<- int) {
    for v := range in {
        out <- v * 2
    }
})
defer doubler.Close()

v, _ := doubler.Resume(21)  // 42
v, _  = doubler.Resume(100) // 200
```

Pure runtime helper — no preprocessor work involved.

## Surface

```go
func Coro[I, O any](body func(in <-chan I, out chan<- O)) *Coroutine[I, O]

type Coroutine[I, O any] struct { /* unexported */ }

func (c *Coroutine[I, O]) Resume(v I) (O, bool)
func (c *Coroutine[I, O]) Close()
func (c *Coroutine[I, O]) Wait()
func (c *Coroutine[I, O]) Done() bool
```

`Resume(v)` sends `v` to the body and waits for the next output. Returns `(zero, false)` if the body has completed (returned on its own or `Close` was called).

`Close` signals the body to stop by closing the input channel — `for v := range in` loops terminate cleanly. Idempotent.

`Wait` blocks until the body's goroutine has fully returned — useful as a hard barrier in tests or before reading shared state the body wrote.

`Done` is the non-blocking version of "has the body finished?".

## Cooperative protocol

The body must alternate reads from `in` and writes to `out` to mirror the caller's `Resume(v)` → wait-for-output cycle. q.Coro doesn't enforce this — it's a cooperative protocol between caller and body:

```go
// OK — read, then write, in lockstep with the caller.
body := func(in <-chan int, out chan<- int) {
    for v := range in {
        out <- v * 2
    }
}

// BROKEN — body sends without reading. The caller's Resume(v) blocks
// because no one will read from `in`, and the body's `out <- 1`
// blocks because no one reads `out` until Resume reaches its
// receive-from-out branch.
brokenBody := func(in <-chan int, out chan<- int) {
    out <- 1
    out <- 2
}
```

For "the body emits a sequence" pattern, take a token-style input and ignore the value:

```go
type tick struct{}
fibs := q.Coro(func(in <-chan tick, out chan<- int) {
    a, b := 0, 1
    for range in {
        out <- a
        a, b = b, a+b
    }
})
defer fibs.Close()
for range 10 {
    n, _ := fibs.Resume(tick{})
    fmt.Println(n)
}
```

## Stateful conversation

Coroutines hold local state across `Resume` calls. This is the main reason to reach for q.Coro over plain `iter.Seq` — `iter.Seq` is pull-only with no way to feed values back into the iteration:

```go
summer := q.Coro(func(in <-chan int, out chan<- int) {
    sum := 0
    for v := range in {
        sum += v
        out <- sum
    }
})
defer summer.Close()

summer.Resume(10)  // 10
summer.Resume(20)  // 30
summer.Resume(30)  // 60
```

## Termination

The body can finish on its own (return from the function) and the next `Resume` returns `(zero, false)`. Or the caller calls `Close()` — the body's `for v := range in` loop terminates because the channel is closed.

```go
twice := q.Coro(func(in <-chan int, out chan<- int) {
    for i := 0; i < 2; i++ {
        v, ok := <-in
        if !ok { return }
        out <- v * 10
    }
    // body returns; further Resume → (zero, false)
})

twice.Resume(1)        // 10, true
twice.Resume(2)        // 20, true
v, ok := twice.Resume(3)  // 0, false  (body has returned)
twice.Wait()
```

## Concurrency

Each `Coroutine` holds one in-flight `Resume` at a time. Concurrent `Resume` calls deadlock with each other: each tries to send on the input channel, but only one body goroutine reads — the second send blocks until the body reads, but the body is waiting for the first caller's output read. Don't share a `Coroutine` across goroutines without external synchronisation.

`Close` and `Wait` are safe from any goroutine.

## What this isn't

- **Not a full Lua coroutine.** Lua coroutines suspend on a single stack; q.Coro uses a real goroutine with its own scheduler context. Cost is the same as any Go goroutine — tens of microseconds to spawn, ~2KB initial stack.
- **Not a state machine rewrite.** Tier 3 in TODO #85 is the preprocessor-rewritten variant where the body is folded into a struct + `Resume(v) (O, bool)` method and runs inline (no goroutine). That's a much bigger lift; q.Coro tier 2 ships first as the goroutine-backed shape.
- **Not exhaustive-typed.** The `(O, bool)` shape doesn't carry a "done reason" enum (cancellation vs body return vs error). If the body needs to signal an error, define `O` as a sum / `Result`-like type.

## See also

- [`q.Async` / `q.Await`](async.md) — fire-and-await for one-shot async work. q.Coro is for stateful, multi-step conversations.
- [`q.Drain` / `q.RecvAny`](channel_multi.md) — pure channel operations without the conversation framing.
