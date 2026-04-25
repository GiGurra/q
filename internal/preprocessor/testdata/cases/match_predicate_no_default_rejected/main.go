// Fixture: q.Match with a predicate q.Case (bool cond) and no
// q.Default — predicate matches can't be statically covered for
// exhaustiveness, so a default arm is required.
package main

import "github.com/GiGurra/q/pkg/q"

func sign(n int) string {
	return q.Match(n,
		q.Case(n > 0, "positive"),
		q.Case(n < 0, "negative"),
		// missing q.Default
	)
}

func main() { _ = sign(5) }
