// Negative fixture: q.SetFnE inside q.ConvertTo (no error slot)
// must fail the build. The diagnostic should point users at
// q.ConvertToE.
package main

import "github.com/GiGurra/q/pkg/q"

type Source struct{ ID int }
type Target struct {
	ID    int
	Email string
}

func lookupEmail(_ Source) (string, error) { return "x", nil }

func main() {
	_ = q.ConvertTo[Target](Source{ID: 1},
		q.SetFnE(Target{}.Email, lookupEmail),
	)
}
