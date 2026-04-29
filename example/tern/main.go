// example/tern mirrors docs/api/tern.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/tern
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type User struct{ Name string }
type Config struct{ MaxConn int }
type Options struct {
	Timeout  int
	Endpoint string
}
type Request struct {
	Timeout  int
	Endpoint string
}

const (
	defaultMaxConn  = 8
	defaultTimeout  = 30
	defaultEndpoint = "https://example.com"
)

// ---------- "What you get" — lazy nil-deref ----------
func displayName(user *User) string {
	return q.Tern(user != nil, user.Name, "anonymous")
}

func maxConn(cfg *Config) int {
	return q.Tern(cfg != nil, cfg.MaxConn, defaultMaxConn)
}

// ---------- "Lazy expensive computation" ----------
func fast() string             { return "fast-result" }
func slowLookup(key string) string { return "slow:" + key }

func cachedOrSlow(cached bool, key string) string {
	return q.Tern(cached, fast(), slowLookup(key))
}

// ---------- "In a struct literal" ----------
func makeReq(opts Options) Request {
	return Request{
		Timeout:  q.Tern(opts.Timeout > 0, opts.Timeout, defaultTimeout),
		Endpoint: q.Tern(opts.Endpoint != "", opts.Endpoint, defaultEndpoint),
	}
}

// ---------- "In a return statement" ----------
func sign(n int) int {
	return q.Tern(n > 0, 1, q.Tern(n < 0, -1, 0))
}

// ---------- "Chained for multi-way pick" ----------
func tier(score int) string {
	return q.Tern(score >= 90, "A",
		q.Tern(score >= 80, "B",
			q.Tern(score >= 70, "C", "F")))
}

// ---------- "Explicit T when you want to widen the result type" ----------
type myStringer struct{ s string }

func (m myStringer) String() string { return m.s }

func widen(ok bool) fmt.Stringer {
	concrete := myStringer{s: "concrete"}
	fallback := myStringer{s: "fallback"}
	return q.Tern[fmt.Stringer](ok, concrete, fallback)
}

func main() {
	// Lazy nil-deref.
	fmt.Printf("displayName(nil)=%q\n", displayName(nil))
	fmt.Printf("displayName(Ada)=%q\n", displayName(&User{Name: "Ada"}))
	fmt.Printf("maxConn(nil)=%d\n", maxConn(nil))
	fmt.Printf("maxConn(MaxConn=42)=%d\n", maxConn(&Config{MaxConn: 42}))

	// Lazy compute.
	fmt.Printf("cachedOrSlow(true)=%s\n", cachedOrSlow(true, "k"))
	fmt.Printf("cachedOrSlow(false)=%s\n", cachedOrSlow(false, "k"))

	// Struct literal.
	req := makeReq(Options{Timeout: 10})
	fmt.Printf("makeReq(Timeout=10): %+v\n", req)
	req = makeReq(Options{})
	fmt.Printf("makeReq(zero): %+v\n", req)

	// Return statement.
	for _, n := range []int{-3, 0, 7} {
		fmt.Printf("sign(%d)=%d\n", n, sign(n))
	}

	// Chained.
	for _, s := range []int{95, 85, 75, 50} {
		fmt.Printf("tier(%d)=%s\n", s, tier(s))
	}

	// Widen.
	fmt.Printf("widen(true)=%s\n", widen(true).String())
	fmt.Printf("widen(false)=%s\n", widen(false).String())
}
