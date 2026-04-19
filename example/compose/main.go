// example/compose — q.* composed with generics, closures, and
// the error-chain. Run with:
//
//	go run -toolexec=q ./example/compose
package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

// --- Generics ---

// parseT uses q.Try inside a generic function. The *new(T) zero-
// value in the bubble spells the type parameter correctly.
func parseT[T any](parse func(string) (T, error), s string) (T, error) {
	v := q.Try(parse(s))
	return v, nil
}

// --- Closures ---

// deferredLookup uses q.NotNilE inside a deferred closure. The
// deferred closure has its *own* result shape; its bubble uses
// those results, not the outer function's.
func deferredLookup(table map[int]*string, id int) (out string, err error) {
	getOrDefault := func() (string, error) {
		name := q.NotNilE(table[id]).Err(fmt.Errorf("no id %d", id))
		return *name, nil
	}
	out, err = getOrDefault()
	return out, err
}

// --- Error chain: errors.Is/As through Wrap ---

// SyntaxErr is a typed error we'll traverse through a Wrap.
type SyntaxErr struct{ Token string }

func (e *SyntaxErr) Error() string { return "syntax error at " + e.Token }

// inner returns one of three selected failure modes.
func inner(mode string) (int, error) {
	switch mode {
	case "eof":
		return 0, io.EOF
	case "syntax":
		return 0, &SyntaxErr{Token: "[bad]"}
	}
	return 42, nil
}

// wrapped wraps the inner err with a prefix. errors.Is and errors.As
// must traverse the %w in the rewritten bubble.
func wrapped(mode string) (int, error) {
	v := q.TryE(inner(mode)).Wrapf("processing %q", mode)
	return v, nil
}

func main() {
	// Generics.
	v, err := parseT(strconv.Atoi, "17")
	fmt.Printf("parseT(Atoi, \"17\") => %d, %v\n", v, err)
	v, err = parseT(strconv.Atoi, "bad")
	fmt.Printf("parseT(Atoi, \"bad\") => %d, %v\n", v, err)

	s, err := parseT(parseID, "user-42")
	fmt.Printf("parseT(parseID, \"user-42\") => %q, %v\n", s, err)

	// Closures.
	name := "Ada"
	table := map[int]*string{1: &name}
	n, err := deferredLookup(table, 1)
	fmt.Printf("deferredLookup(1) => %q, %v\n", n, err)
	n, err = deferredLookup(table, 99)
	fmt.Printf("deferredLookup(99) => %q, %v\n", n, err)

	// Error chain.
	_, err = wrapped("eof")
	fmt.Printf("Wrapf+errors.Is(io.EOF): %v\n", errors.Is(err, io.EOF))

	_, err = wrapped("syntax")
	var sErr *SyntaxErr
	fmt.Printf("Wrapf+errors.As(*SyntaxErr): %v", errors.As(err, &sErr))
	if sErr != nil {
		fmt.Printf(" (token=%s)", sErr.Token)
	}
	fmt.Println()
}

// parseID splits "prefix-n" and returns n as a string (just to give
// parseT a second T to instantiate at).
func parseID(s string) (string, error) {
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			return s[i+1:], nil
		}
	}
	return "", fmt.Errorf("no '-' in %q", s)
}
