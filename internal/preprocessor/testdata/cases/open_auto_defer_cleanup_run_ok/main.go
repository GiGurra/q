// Fixture: q.Open(...).DeferCleanup() with no args — preprocessor infers
// the cleanup from the resource type at compile time. Three forms:
//   - channel type:        defer close(v)
//   - Close() error:       defer func() { _ = v.Close() }()
//   - Close() (no return): defer v.Close()
// Plus a regression check that explicit DeferCleanup(cleanup) still fires.
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// --- Channel resource. Auto-cleanup: defer close(ch). ---

func makeChan() (chan int, error) {
	return make(chan int, 4), nil
}

// channelAutoInner returns the channel after the deferred close
// has fired. The trick: the auto-DeferCleanup defer runs as
// channelAutoInner returns, so the caller can probe the channel's
// closed state to confirm the close happened.
//
//q:no-escape-check
func channelAutoInner() (chan int, error) {
	ch := q.Open(makeChan()).DeferCleanup()
	ch <- 7
	return ch, nil
}

func channelAutoObserved() string {
	ch, _ := channelAutoInner()
	v, ok1 := <-ch
	_, ok2 := <-ch
	return fmt.Sprintf("v=%d ok1=%v ok2=%v", v, ok1, ok2)
}

// --- Close() error resource. Auto-cleanup: defer func() { _ = v.Close() }(). ---

type errCloser struct {
	id     int
	closed *[]int
}

func (e *errCloser) Close() error {
	*e.closed = append(*e.closed, e.id)
	return nil
}

func openErrCloser(id int, closed *[]int) (*errCloser, error) {
	return &errCloser{id: id, closed: closed}, nil
}

func errCloserAuto(closed *[]int) error {
	_ = q.Open(openErrCloser(11, closed)).DeferCleanup()
	return nil
}

// --- Close() void resource. Auto-cleanup: defer v.Close(). ---

type voidCloser struct {
	id     int
	closed *[]int
}

func (v *voidCloser) Close() {
	*v.closed = append(*v.closed, v.id)
}

func openVoidCloser(id int, closed *[]int) (*voidCloser, error) {
	return &voidCloser{id: id, closed: closed}, nil
}

func voidCloserAuto(closed *[]int) error {
	_ = q.Open(openVoidCloser(22, closed)).DeferCleanup()
	return nil
}

// --- Regression: explicit DeferCleanup(cleanup) still fires unchanged. ---

type plainResource struct{ id int }

func openPlain(id int) (*plainResource, error) {
	return &plainResource{id: id}, nil
}

func explicitDeferCleanup(closed *[]int) error {
	cleanup := func(p *plainResource) { *closed = append(*closed, p.id) }
	_ = q.Open(openPlain(33)).DeferCleanup(cleanup)
	return nil
}

// --- OpenE chain composition: Wrap + auto DeferCleanup. ---

//q:no-escape-check
func errCloserAutoWrap(closed *[]int) (*errCloser, error) {
	v := q.OpenE(openErrCloser(44, closed)).Wrap("dial").DeferCleanup()
	return v, nil
}

// --- Auto-DeferCleanup on the bubble path: cleanup must NOT fire when
// the open itself failed. ---

var errOpen = errors.New("boom")

func failingOpen() (*errCloser, error) {
	return nil, errOpen
}

//q:no-escape-check
func autoDeferCleanupBubble() (*errCloser, error) {
	v := q.Open(failingOpen()).DeferCleanup()
	return v, nil
}

func main() {
	// Channel auto-close.
	fmt.Println("channelAutoObserved:", channelAutoObserved())

	// Close() error path.
	var closed []int
	if err := errCloserAuto(&closed); err != nil {
		fmt.Println("errCloserAuto:", err)
	}
	fmt.Printf("errCloserAuto.closed=%v\n", closed)

	// Close() void path.
	closed = nil
	if err := voidCloserAuto(&closed); err != nil {
		fmt.Println("voidCloserAuto:", err)
	}
	fmt.Printf("voidCloserAuto.closed=%v\n", closed)

	// Explicit DeferCleanup(cleanup) regression.
	closed = nil
	if err := explicitDeferCleanup(&closed); err != nil {
		fmt.Println("explicitDeferCleanup:", err)
	}
	fmt.Printf("explicitDeferCleanup.closed=%v\n", closed)

	// OpenE.Wrap + auto-DeferCleanup composition.
	closed = nil
	v, _ := errCloserAutoWrap(&closed)
	fmt.Printf("errCloserAutoWrap.id=%d closed=%v\n", v.id, closed)

	// Bubble path — no cleanup should fire.
	_, err := autoDeferCleanupBubble()
	fmt.Printf("autoDeferCleanupBubble.err=%v\n", err)
}
