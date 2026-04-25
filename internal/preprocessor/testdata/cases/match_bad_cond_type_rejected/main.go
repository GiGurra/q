// Fixture: q.Case cond with a type that's neither the matched
// value's type, bool, nor a func() returning either — should be
// rejected with a clear diagnostic.
package main

import "github.com/GiGurra/q/pkg/q"

func bad(n int) string {
	return q.Match(n,
		q.Case("a string", "wrong type"), // cond is string, but n is int
		q.Default("default"),
	)
}

func main() { _ = bad(1) }
