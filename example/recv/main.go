// example/recv mirrors docs/api/recv.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/recv
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Msg struct {
	Body     string
	Sentinel bool
}

var ErrPipelineClosed = errors.New("pipeline closed")

func openCh() chan Msg {
	ch := make(chan Msg, 1)
	ch <- Msg{Body: "hello"}
	close(ch)
	return ch
}

func openClosedCh() chan Msg {
	ch := make(chan Msg)
	close(ch)
	return ch
}

// ---------- "What q.Recv does" ----------
//
//	msg := q.Recv(ch)
func recvDemo(ch <-chan Msg) (Msg, error) {
	msg := q.Recv(ch)
	return msg, nil
}

// ---------- "Chain methods on q.RecvE" ----------
func recvEWrap(inbox <-chan Msg) (Msg, error) {
	msg := q.RecvE(inbox).Wrap("reading inbox")
	return msg, nil
}

func recvEErr(inbox <-chan Msg) (Msg, error) {
	msg := q.RecvE(inbox).Err(ErrPipelineClosed)
	return msg, nil
}

func recvECatch(inbox <-chan Msg) (Msg, error) {
	msg := q.RecvE(inbox).Catch(func() (Msg, error) { return Msg{Sentinel: true}, nil })
	return msg, nil
}

func recvEErrF(inbox <-chan Msg) (Msg, error) {
	msg := q.RecvE(inbox).ErrF(func() error { return errors.New("inbox: gone") })
	return msg, nil
}

func recvEWrapf(inbox <-chan Msg, name string) (Msg, error) {
	msg := q.RecvE(inbox).Wrapf("inbox %s closed", name)
	return msg, nil
}

// ---------- Statement forms ----------
func formDefine(ch <-chan Msg) (Msg, error) {
	m := q.Recv(ch)
	return m, nil
}

func formAssign(ch <-chan Msg) (Msg, error) {
	var arr [1]Msg
	arr[0] = q.Recv(ch)
	return arr[0], nil
}

func formDiscard(ch <-chan Msg) error {
	q.Recv(ch)
	return nil
}

func formReturn(ch <-chan Msg) (Msg, error) {
	return q.Recv(ch), nil
}

func formHoist(ch <-chan Msg) (string, error) {
	body := bodyOf(q.Recv(ch))
	return body, nil
}

func bodyOf(m Msg) string { return m.Body }

func main() {
	run := func(label string, fn func() (Msg, error)) {
		m, err := fn()
		if err != nil {
			fmt.Printf("%s: err=%s\n", label, err)
			return
		}
		fmt.Printf("%s: ok body=%q sentinel=%v\n", label, m.Body, m.Sentinel)
	}

	// Open path: channel has one message, then closed; first recv succeeds.
	run("recvDemo(open)", func() (Msg, error) { return recvDemo(openCh()) })
	// Closed path: channel already drained → bubbles q.ErrChanClosed.
	_, err := recvDemo(openClosedCh())
	fmt.Printf("recvDemo(closed): err=%s\n", err)
	fmt.Printf("recvDemo(closed).is(q.ErrChanClosed): %v\n", errors.Is(err, q.ErrChanClosed))

	run("recvEWrap(closed)", func() (Msg, error) { return recvEWrap(openClosedCh()) })
	run("recvEErr(closed)", func() (Msg, error) { return recvEErr(openClosedCh()) })
	if _, err := recvEErr(openClosedCh()); err != nil {
		fmt.Printf("recvEErr(closed).is(ErrPipelineClosed): %v\n", errors.Is(err, ErrPipelineClosed))
	}
	run("recvECatch(closed) [recovers via sentinel]", func() (Msg, error) { return recvECatch(openClosedCh()) })
	run("recvEErrF(closed)", func() (Msg, error) { return recvEErrF(openClosedCh()) })
	run("recvEWrapf(closed,inbox)", func() (Msg, error) { return recvEWrapf(openClosedCh(), "inbox") })

	run("formDefine(open)", func() (Msg, error) { return formDefine(openCh()) })
	run("formAssign(open)", func() (Msg, error) { return formAssign(openCh()) })
	if err := formDiscard(openCh()); err != nil {
		fmt.Printf("formDiscard(open): err=%s\n", err)
	} else {
		fmt.Println("formDiscard(open): ok")
	}
	if err := formDiscard(openClosedCh()); err != nil {
		fmt.Printf("formDiscard(closed): err=%s\n", err)
	}
	run("formReturn(open)", func() (Msg, error) { return formReturn(openCh()) })
	if name, err := formHoist(openCh()); err != nil {
		fmt.Printf("formHoist(open): err=%s\n", err)
	} else {
		fmt.Printf("formHoist(open): %s\n", name)
	}
}
