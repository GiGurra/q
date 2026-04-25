// Fixture: positive cases that the resource-escape detection must
// NOT flag.
//
// 1. q.Open(...).NoRelease() means caller takes ownership — return is fine.
// 2. q.Open(...).Release(...) used purely locally — no escape.
// 3. Plain function call (`process(c)`) does NOT count as an escape.
// 4. close(ch) followed by return ch — closed channels are still
//    legitimate for receives ("finite stream" idiom).
// 5. Channel auto-close via q.Open is allowed when explicitly opted
//    out via //q:no-escape-check (factory testing pattern).
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Conn struct{ id int; closed bool }

func (c *Conn) Close() { c.closed = true }

func dial(id int) (*Conn, error) { return &Conn{id: id}, nil }

// (1) NoRelease — caller is expected to take ownership. Return is OK.
func openWithNoRelease() (*Conn, error) {
	c := q.OpenE(dial(1)).NoRelease()
	return c, nil
}

// (2) Release-bound but used only locally; no escape.
func openLocalUseOnly() error {
	c := q.Open(dial(2)).Release((*Conn).Close)
	c.id += 10 // local read/mutate is fine
	return nil
}

// (3) Passing the resource to a normal function call is fine — the
// callee returns before the deferred close fires.
func process(c *Conn) int { return c.id }

func openAndProcess() (int, error) {
	c := q.Open(dial(3)).Release((*Conn).Close)
	return process(c), nil // OK: return value is process's int, not c
}

// (4) Producing a closed channel and returning it — the consumer
// ranges until close. Idiomatic.
func closedChanFactory() <-chan int {
	ch := make(chan int, 3)
	for i := 1; i <= 3; i++ {
		ch <- i
	}
	close(ch)
	return ch // OK: channel post-close is still readable
}

// (5) Auto-close on a channel via q.Open is also OK with the opt-out
// directive — primarily for tests of q.Open's mechanism.
//
//q:no-escape-check
func chanFactoryWithDirective() (chan int, error) {
	ch := q.Open(func() (chan int, error) { return make(chan int, 1), nil }()).Release()
	ch <- 99
	return ch, nil
}

func main() {
	c, _ := openWithNoRelease()
	fmt.Println("openWithNoRelease.id:", c.id)

	_ = openLocalUseOnly()
	fmt.Println("openLocalUseOnly: ok")

	n, _ := openAndProcess()
	fmt.Println("openAndProcess:", n)

	for v := range closedChanFactory() {
		fmt.Println("closedChan:", v)
	}

	ch, _ := chanFactoryWithDirective()
	v, ok1 := <-ch
	_, ok2 := <-ch
	fmt.Printf("chanFactoryWithDirective: v=%d ok1=%v ok2=%v\n", v, ok1, ok2)
}
