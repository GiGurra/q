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
}
