package q

// coro.go — coroutine helpers.
//
// Two surfaces:
//
//   - q.Generator / q.Yield (tier 1): preprocessor-rewritten sugar
//     that produces a Go 1.23 `iter.Seq[T]` from a body that calls
//     q.Yield(v) at each emission point. Pull-based, single
//     direction (body → caller). Free interop with `for v := range`.
//
//   - q.Coro / *Coroutine (tier 2): pure-runtime bidirectional
//     coroutine on a goroutine plus two channels. The body reads
//     inputs and writes outputs via channels; the caller drives the
//     conversation through .Resume(in) -> (out, ok) and .Close().
//     Use this when both sides need to exchange values cooperatively
//     — q.Generator covers the simpler emit-only case.
//
// Tier 3 (stackless state machines via preprocessor) is tracked in
// docs/planning/TODO.md but not shipped.

import (
	"iter"
	"sync"
)

// Generator turns a body that calls q.Yield(v) into a stdlib
// iter.Seq[T]. The preprocessor rewrites the call site, transforming
// the body into the iter.Seq callback shape: every q.Yield(v) becomes
// `if !yield(v) { return }`, and the whole expression is converted to
// `iter.Seq[T](func(yield func(T) bool) { ... })`.
//
// The type parameter T is required at the call site (Go can't infer
// generic type arguments that appear only in the result type).
//
// Example:
//
//	fibs := q.Generator[int](func() {
//	    a, b := 0, 1
//	    for {
//	        q.Yield(a)
//	        a, b = b, a+b
//	    }
//	})
//	for v := range fibs {
//	    if v > 100 { break }
//	    fmt.Println(v)
//	}
//
// Reach for q.Coro when the body needs to receive values from the
// caller as well as emit them.
//
// Inside a Generator body, `return` exits the body (ending the
// sequence). The yield function is hidden by the rewriter — call
// q.Yield(v) instead of writing the boilerplate `if !yield(v) {
// return }` form. q.Yield is recognised only inside a Generator's
// body; outside it, the runtime stub panics.
//
// Nested closures inside the body: q.Yield calls in nested closures
// are also rewritten, but the early `return` exits the innermost
// enclosing func. This matches the behaviour of writing the iter.Seq
// callback by hand.
func Generator[T any](body func()) iter.Seq[T] {
	panicUnrewritten("q.Generator")
	return nil
}

// Yield emits v as the next value in an enclosing q.Generator's
// sequence. The preprocessor rewrites q.Yield(v) inside a Generator
// body into `if !yield(v) { return }` — early-exit when the consumer
// stops ranging.
//
// Outside a Generator body, q.Yield panics at runtime via the
// universal stub — there is no yield function to bind to.
func Yield[T any](v T) {
	panicUnrewritten("q.Yield")
}

// Coroutine is a bidirectional, suspendable computation. Construct
// with q.Coro; drive with .Resume / .Close.
type Coroutine[I, O any] struct {
	in   chan I
	out  chan O
	done chan struct{}
	mu   sync.Mutex
	closed bool
}

// Coro spawns body in a goroutine and returns a Coroutine handle.
// body reads inputs from `in` and writes outputs to `out`; when it
// returns, the coroutine is finished and subsequent .Resume calls
// return zero, false.
//
// Either side may close the conversation: the caller via .Close(),
// or the body by returning. Closing is idempotent.
//
//	doubler := q.Coro(func(in <-chan int, out chan<- int) {
//	    for v := range in {
//	        out <- v * 2
//	    }
//	})
//	defer doubler.Close()
//
//	v, _ := doubler.Resume(21) // 42
//	v, _ = doubler.Resume(100) // 200
//
// Send/receive ordering matters: the body must read from `in` before
// it sends on `out` for each step (or the .Resume call will block
// forever). q.Coro doesn't enforce this — it's a cooperative
// protocol between caller and body.
func Coro[I, O any](body func(in <-chan I, out chan<- O)) *Coroutine[I, O] {
	c := &Coroutine[I, O]{
		in:   make(chan I),
		out:  make(chan O),
		done: make(chan struct{}),
	}
	go func() {
		defer close(c.done)
		// close(out) so a select in .Resume can detect body
		// completion when the body returns without sending.
		defer close(c.out)
		body(c.in, c.out)
	}()
	return c
}

// Resume sends v to the coroutine's body and waits for the next
// value the body produces. Returns (value, true) on success, or
// (zero, false) if the body has finished (returned) or .Close was
// called.
//
// Resume is safe to call from any goroutine but only one Resume
// call can be in-flight at a time; concurrent Resumes deadlock with
// each other (each would block waiting for the body to read its
// input).
func (c *Coroutine[I, O]) Resume(v I) (O, bool) {
	var zero O
	// Fast-path bail when already closed/done.
	select {
	case <-c.done:
		return zero, false
	default:
	}
	select {
	case c.in <- v:
	case <-c.done:
		return zero, false
	}
	select {
	case o, ok := <-c.out:
		if !ok {
			return zero, false
		}
		return o, true
	case <-c.done:
		return zero, false
	}
}

// Close signals the body that the conversation is over by closing
// the input channel. The body's `for range in` loop terminates
// cleanly; bodies that don't range over `in` should select on it
// closing to detect Close.
//
// Idempotent. Safe to call from any goroutine. Returns immediately;
// the body's goroutine may still run for a bit while finishing up.
// Use .Wait if you need to block until the body is fully done.
func (c *Coroutine[I, O]) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.in)
	c.mu.Unlock()
}

// Wait blocks until the body's goroutine has returned. Useful in
// tests + when the caller needs a hard barrier between "I'm done
// using the coroutine" and "the body is fully torn down."
func (c *Coroutine[I, O]) Wait() {
	<-c.done
}

// Done reports whether the body has finished (either by returning
// on its own or because .Close was called and the body has now
// drained). Non-blocking.
func (c *Coroutine[I, O]) Done() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}
