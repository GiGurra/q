// Fixture: q.Recv and q.As plus their chain variants. Both families
// share the Ok-like "bubble on !ok" shape; Recv wraps a channel
// receive (<-ch produces (v, ok) where ok=false when closed) and As
// wraps a type assertion.
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

var ErrReplaced = errors.New("replaced")

// receiveOne is the happy-path use of q.Recv.
func receiveOne(ch <-chan int) (int, error) {
	return q.Recv(ch), nil
}

// recvWrap uses q.RecvE chain to supply a richer bubble.
func recvWrap(ch <-chan int) (int, error) {
	return q.RecvE(ch).Wrap("reading work"), nil
}

// recvCatch recovers a closed-channel signal with a fallback value.
func recvCatch(ch <-chan int) (int, error) {
	return q.RecvE(ch).Catch(func() (int, error) {
		return -1, nil
	}), nil
}

// asInt uses bare q.As.
func asInt(x any) (int, error) {
	return q.As[int](x), nil
}

// asWrap uses q.AsE chain with Wrapf.
func asWrap(x any) (int, error) {
	return q.AsE[int](x).Wrapf("not int: %v", x), nil
}

// asErrReplace uses q.AsE.Err to swap the sentinel.
func asErrReplace(x any) (int, error) {
	return q.AsE[int](x).Err(ErrReplaced), nil
}

// asCatch recovers a bad type with a fallback.
func asCatch(x any) (int, error) {
	return q.AsE[int](x).Catch(func() (int, error) {
		return 42, nil
	}), nil
}

func report(name string, n int, err error) {
	if err != nil {
		fmt.Printf("%s: err=%s\n", name, err)
	} else {
		fmt.Printf("%s: ok=%d\n", name, n)
	}
}

func main() {
	// Recv — open channel with a value.
	openCh := make(chan int, 1)
	openCh <- 7
	n, err := receiveOne(openCh)
	report("recv.ok", n, err)

	// Recv — closed channel bubbles ErrChanClosed.
	closedCh := make(chan int)
	close(closedCh)
	n, err = receiveOne(closedCh)
	report("recv.closed", n, err)
	fmt.Printf("recv.is: %v\n", errors.Is(err, q.ErrChanClosed))

	// RecvE.Wrap — closed channel with custom message.
	n, err = recvWrap(closedCh)
	report("recvWrap.closed", n, err)

	// RecvE.Catch — closed channel with recovery.
	n, err = recvCatch(closedCh)
	report("recvCatch.closed", n, err)

	// As — matching type.
	n, err = asInt(42)
	report("as.int", n, err)

	// As — non-matching type bubbles ErrBadTypeAssert.
	n, err = asInt("not an int")
	report("as.bad", n, err)
	fmt.Printf("as.is: %v\n", errors.Is(err, q.ErrBadTypeAssert))

	// AsE.Wrapf — non-matching type with formatted message.
	n, err = asWrap("hello")
	report("asWrap.bad", n, err)

	// AsE.Err — non-matching type, replaced sentinel.
	n, err = asErrReplace("nope")
	report("asErr.bad", n, err)

	// AsE.Catch — non-matching type with recovery.
	n, err = asCatch("x")
	report("asCatch.bad", n, err)
}
