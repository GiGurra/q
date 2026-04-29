// example/atcompiletime mirrors docs/api/atcompiletime.md one-to-one.
// Each section of the doc has a matching var / function below.
// Run with:
//
//	go run -toolexec=q ./example/atcompiletime
package main

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- "A constant" ----------
//
//	days := q.AtCompileTime[int](func() int { return 7 * 24 * 60 * 60 })
var Days = q.AtCompileTime[int](func() int { return 7 * 24 * 60 * 60 })

// ---------- "A hash digest" ----------
var Hash = q.AtCompileTime[string](func() string {
	sum := md5.Sum([]byte("hello"))
	return hex.EncodeToString(sum[:])
})

// ---------- "A list" ----------
var Primes = q.AtCompileTime[[]int](func() []int {
	out := []int{}
	for n := 2; len(out) < 10; n++ {
		prime := true
		for _, p := range out {
			if p*p > n {
				break
			}
			if n%p == 0 {
				prime = false
				break
			}
		}
		if prime {
			out = append(out, n)
		}
	}
	return out
})

// ---------- "A lookup table" ----------
var CRC8 = q.AtCompileTime[[256]uint8](func() [256]uint8 {
	var t [256]uint8
	for i := range t {
		c := uint8(i)
		for b := 0; b < 8; b++ {
			if c&0x80 != 0 {
				c = (c << 1) ^ 0x07
			} else {
				c <<= 1
			}
		}
		t[i] = c
	}
	return t
})

// ---------- "Capturing earlier q.AtCompileTime results" ----------
//
// Note: each q.AtCompileTime is its own top-level `var` declaration.
// Grouped `var ( … )` blocks containing q.AtCompileTime entries are
// not currently supported by the rewriter — see TODO #102.
var Greeting = q.AtCompileTime[string](func() string { return "Hello" })
var Farewell = q.AtCompileTime[string](func() string { return "Goodbye" })
var Banner = q.AtCompileTime[string](func() string {
	return Greeting + " / " + Farewell
})

// ---------- "q.AtCompileTimeCode — a function value" ----------
var Greet = q.AtCompileTimeCode[func(string) string](func() string {
	return `func(name string) string { return "Hi, " + name }`
})

// ---------- "q.AtCompileTimeCode — a switch built from a list" ----------
var IsAllowed = q.AtCompileTimeCode[func(string) bool](func() string {
	var b strings.Builder
	b.WriteString("func(s string) bool { switch s {\n")
	for _, n := range []string{"alice", "bob", "carol"} {
		fmt.Fprintf(&b, "case %q: return true\n", n)
	}
	b.WriteString("}; return false }")
	return b.String()
})

// ---------- "q.AtCompileTimeCode — a constant string" ----------
var Tag = q.AtCompileTimeCode[string](func() string {
	parts := []string{"prod", "edge", "v2"}
	return fmt.Sprintf("%q", strings.Join(parts, "-"))
})

// ---------- "Composing q.AtCompileTimeCode with a precomputed value" ----------
var Names = q.AtCompileTime[[]string](func() []string {
	return []string{"alice", "bob"}
})

var GreetByIndex = q.AtCompileTimeCode[func(int) string](func() string {
	var b strings.Builder
	b.WriteString("func(i int) string { switch i {\n")
	for i, n := range Names {
		fmt.Fprintf(&b, "case %d: return %q\n", i, "hi "+n)
	}
	b.WriteString("default: return \"\"\n} }")
	return b.String()
})

func main() {
	fmt.Printf("Days: %d\n", Days)
	fmt.Printf("Hash: %s\n", Hash)
	fmt.Printf("Primes: %v\n", Primes)
	fmt.Printf("CRC8[0..3]: %v\n", CRC8[0:4])
	fmt.Printf("CRC8[255]: 0x%02x\n", CRC8[255])

	fmt.Printf("Greeting: %s\n", Greeting)
	fmt.Printf("Farewell: %s\n", Farewell)
	fmt.Printf("Banner: %s\n", Banner)

	fmt.Printf("Greet(alice): %s\n", Greet("alice"))

	fmt.Printf("IsAllowed(alice): %v\n", IsAllowed("alice"))
	fmt.Printf("IsAllowed(dave): %v\n", IsAllowed("dave"))

	fmt.Printf("Tag: %s\n", Tag)

	fmt.Printf("Names: %v\n", Names)
	fmt.Printf("GreetByIndex(0): %s\n", GreetByIndex(0))
	fmt.Printf("GreetByIndex(1): %s\n", GreetByIndex(1))
	fmt.Printf("GreetByIndex(99): %q\n", GreetByIndex(99))
}
