package q

// coro.go — bidirectional coroutines built on a goroutine + two
// channels.
//
// Go has goroutines (concurrency, separate stacks) and Go 1.23 has
// `iter.Seq` (pull-based iteration). It does not have full
// coroutines: bidirectional, suspendable functions that the caller
// can pass values into and pull values out of, cooperatively.
//
// q.Coro spawns the body in its own goroutine and exposes a pair of
// channels (one for caller→body, one for body→caller). The body
// reads inputs and writes outputs via those channels; the caller
// drives the conversation through .Resume(in) -> (out, ok) and
// .Close().
//
// Pure runtime — no preprocessor work. Tier 1 (iter.Seq sugar) and
// tier 3 (stackless state machines via preprocessor) are tracked in
// docs/planning/TODO.md #85 but not shipped here.

import (
	"sync"
)

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
