// example/require mirrors docs/api/require.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/require
package main

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- "What q.Require does" — error-only return + message ----------
func encode(buf []byte) error {
	q.Require(len(buf) >= 16, "header too short")
	return nil
}

// (T, error) return.
func decode(buf []byte) (string, error) {
	q.Require(len(buf) >= 16, "header too short")
	return string(buf), nil
}

// Without a message.
func encodeNoMsg(buf []byte) error {
	q.Require(len(buf) >= 16)
	return nil
}

// ---------- "Sentinel identity" ----------
func isRequireFailure(err error) bool {
	return errors.Is(err, q.ErrRequireFailed)
}

// stripFileLine erases the absolute path prefix so the test output
// is deterministic across machines: `…/main.go:18:` → `main.go:N:`.
var fileLineRE = regexp.MustCompile(`[^ ]*main\.go:\d+`)

func stripPath(s string) string {
	return fileLineRE.ReplaceAllString(s, "main.go:N")
}

func main() {
	var (
		short = []byte("hi")
		long  = []byte("0123456789abcdef-extra")
	)

	if err := encode(long); err != nil {
		fmt.Printf("encode(long): err=%s\n", stripPath(err.Error()))
	} else {
		fmt.Println("encode(long): ok")
	}

	err := encode(short)
	fmt.Printf("encode(short): err=%s\n", stripPath(err.Error()))
	fmt.Printf("encode(short).is(q.ErrRequireFailed): %v\n", isRequireFailure(err))

	if v, err := decode(long); err != nil {
		fmt.Printf("decode(long): err=%s\n", stripPath(err.Error()))
	} else {
		fmt.Printf("decode(long): ok body=%q\n", v)
	}
	v, err := decode(short)
	fmt.Printf("decode(short): v=%q err=%s\n", v, stripPath(err.Error()))

	err = encodeNoMsg(short)
	fmt.Printf("encodeNoMsg(short): err=%s\n", stripPath(err.Error()))
}
