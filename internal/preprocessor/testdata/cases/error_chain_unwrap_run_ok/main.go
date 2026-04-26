// Fixture: errors.Is and errors.As work through q's Wrap / Wrapf
// rewrites. The rewriter spells fmt.Errorf with `%w` for the captured
// error, so the standard error-chain inspection helpers must traverse
// the wrap and find the underlying typed/sentinel error.
package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/GiGurra/q/pkg/q"
)

// SyntaxErr is a typed error so we can verify errors.As recovers it
// through the wrap.
type SyntaxErr struct{ Token string }

func (e *SyntaxErr) Error() string { return "syntax error at " + e.Token }

// inner returns one of three failure modes selected by the input — a
// sentinel io.EOF, a typed *SyntaxErr, or a fresh anonymous error.
func inner(mode string) (int, error) {
	switch mode {
	case "eof":
		return 0, io.EOF
	case "syntax":
		return 0, &SyntaxErr{Token: "[bad]"}
	case "anon":
		return 0, errors.New("anonymous failure")
	}
	return 42, nil
}

func wrappedF(mode string) (int, error) {
	v := q.TryE(inner(mode)).Wrapf("processing %q", mode)
	return v, nil
}

func wrappedNoF(mode string) (int, error) {
	v := q.TryE(inner(mode)).Wrap("processing")
	return v, nil
}

// openConn returns a toy conn plus one of the failure modes. Same
// shape as inner but producing *Conn instead of int so we can exercise
// q.OpenE.
type Conn struct{ id int }

func (*Conn) Close() {}

func openConn(mode string) (*Conn, error) {
	switch mode {
	case "eof":
		return nil, io.EOF
	case "syntax":
		return nil, &SyntaxErr{Token: "[bad]"}
	case "anon":
		return nil, errors.New("anonymous failure")
	}
	return &Conn{id: 1}, nil
}

//q:no-escape-check
func openWrappedF(mode string) (*Conn, error) {
	c := q.OpenE(openConn(mode)).Wrapf("opening %q", mode).DeferCleanup((*Conn).Close)
	return c, nil
}

//q:no-escape-check
func openWrappedNoF(mode string) (*Conn, error) {
	c := q.OpenE(openConn(mode)).Wrap("opening").DeferCleanup((*Conn).Close)
	return c, nil
}

func main() {
	// errors.Is should find io.EOF through the wrap.
	_, err := wrappedF("eof")
	fmt.Printf("Wrapf+Is(io.EOF): %v\n", errors.Is(err, io.EOF))

	_, err = wrappedNoF("eof")
	fmt.Printf("Wrap+Is(io.EOF):  %v\n", errors.Is(err, io.EOF))

	// errors.As should recover *SyntaxErr through the wrap.
	_, err = wrappedF("syntax")
	var sErr *SyntaxErr
	if errors.As(err, &sErr) {
		fmt.Printf("Wrapf+As(*SyntaxErr): token=%s\n", sErr.Token)
	} else {
		fmt.Println("Wrapf+As(*SyntaxErr): MISSED")
	}

	_, err = wrappedNoF("syntax")
	sErr = nil
	if errors.As(err, &sErr) {
		fmt.Printf("Wrap+As(*SyntaxErr):  token=%s\n", sErr.Token)
	} else {
		fmt.Println("Wrap+As(*SyntaxErr):  MISSED")
	}

	// Outer message preserves the supplied prefix and the wrapped err.
	_, err = wrappedF("anon")
	fmt.Printf("Wrapf message: %s\n", err)

	_, err = wrappedNoF("anon")
	fmt.Printf("Wrap message:  %s\n", err)

	// Sanity: success path leaves err nil.
	n, err := wrappedF("ok")
	fmt.Printf("Wrapf success: n=%d err=%v\n", n, err)

	// Same chain checks for q.OpenE — the wrap shape is identical to
	// TryE's, so errors.Is / errors.As must still traverse correctly.
	_, err = openWrappedF("eof")
	fmt.Printf("Open Wrapf+Is(io.EOF): %v\n", errors.Is(err, io.EOF))

	_, err = openWrappedNoF("eof")
	fmt.Printf("Open Wrap+Is(io.EOF):  %v\n", errors.Is(err, io.EOF))

	_, err = openWrappedF("syntax")
	var osErr *SyntaxErr
	if errors.As(err, &osErr) {
		fmt.Printf("Open Wrapf+As(*SyntaxErr): token=%s\n", osErr.Token)
	} else {
		fmt.Println("Open Wrapf+As(*SyntaxErr): MISSED")
	}

	_, err = openWrappedF("anon")
	fmt.Printf("Open Wrapf message: %s\n", err)
}
