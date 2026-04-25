// Fixture: matrix of q.Case dispatch shapes for the unified design.
// Covers cond = V / bool / func()V / func()bool, mixed arms,
// source-rewritten lazy results.
package main

import (
	"fmt"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

// ---- Pure predicate match ----

func sign(n int) string {
	return q.Match(n,
		q.Case(n > 0, "positive"),
		q.Case(n < 0, "negative"),
		q.Default("zero"),
	)
}

// ---- Mix of value match + predicate ----

func bucket(n int) string {
	return q.Match(n,
		q.Case(0, "zero"),                 // value match
		q.Case(n > 100, "big"),            // predicate
		q.Case(42, "answer"),              // value match
		q.Case(n < 0, "negative"),         // predicate
		q.Default("ordinary"),
	)
}

// ---- func() V cond — lazy value match ----

var lookups int

func threshold() int { lookups++; return 7 }

func bumpsLookup(n int) string {
	return q.Match(n,
		q.Case(threshold, "matches threshold"),
		q.Default("doesn't match threshold"),
	)
}

// ---- func() bool cond — lazy predicate ----

func slowPositive(n int) func() bool {
	return func() bool { return n > 0 }
}

func usesPredicateFn(n int) string {
	pred := slowPositive(n)
	return q.Match(n,
		q.Case(pred, "positive (via fn)"),
		q.Default("non-positive"),
	)
}

// ---- Lazy result via source-rewrite — only matching arm runs side
//      effects. Same semantics that the old q.CaseFn used to provide.

var sideHits int

func tag(s string) string { sideHits++; return s }

func lazyResults(n int) string {
	return q.Match(n,
		q.Case(0, tag("zero")),
		q.Case(n > 0, tag("pos")),
		q.Default(tag("neg")),
	)
}

// ---- All-q.Case shape still emits a switch (preserved behaviour) ----

type Color int

const (
	Red Color = iota
	Green
	Blue
)

func describe(c Color) string {
	return q.Match(c,
		q.Case(Red, "warm"),
		q.Case(Green, "natural"),
		q.Case(Blue, "cool"),
	)
}

// ---- Predicate on a string-typed match ----

func category(s string) string {
	return q.Match(s,
		q.Case("alpha", "A"),
		q.Case(strings.HasPrefix(s, "z"), "Z-prefixed"),
		q.Case(len(s) > 4, "long"),
		q.Default("misc"),
	)
}

func main() {
	fmt.Println("sign(5):", sign(5))
	fmt.Println("sign(-3):", sign(-3))
	fmt.Println("sign(0):", sign(0))

	fmt.Println("bucket(0):", bucket(0))
	fmt.Println("bucket(150):", bucket(150))
	fmt.Println("bucket(42):", bucket(42))
	fmt.Println("bucket(-1):", bucket(-1))
	fmt.Println("bucket(7):", bucket(7))

	lookups = 0
	fmt.Printf("bumpsLookup(7): %s lookups=%d\n", bumpsLookup(7), lookups)
	lookups = 0
	fmt.Printf("bumpsLookup(8): %s lookups=%d\n", bumpsLookup(8), lookups)

	fmt.Println("usesPredicateFn(5):", usesPredicateFn(5))
	fmt.Println("usesPredicateFn(-5):", usesPredicateFn(-5))

	sideHits = 0
	fmt.Printf("lazyResults(0): %s hits=%d\n", lazyResults(0), sideHits)
	sideHits = 0
	fmt.Printf("lazyResults(5): %s hits=%d\n", lazyResults(5), sideHits)
	sideHits = 0
	fmt.Printf("lazyResults(-5): %s hits=%d\n", lazyResults(-5), sideHits)

	fmt.Println("describe(Red):", describe(Red))
	fmt.Println("describe(Green):", describe(Green))
	fmt.Println("describe(Blue):", describe(Blue))

	fmt.Println("category(\"alpha\"):", category("alpha"))
	fmt.Println("category(\"zebra\"):", category("zebra"))
	fmt.Println("category(\"longish\"):", category("longish"))
	fmt.Println("category(\"hi\"):", category("hi"))
}
