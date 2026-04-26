// Negative fixture: same-named target field has an incompatible
// type → build fails with the assignability diagnostic.
package main

import "github.com/GiGurra/q/pkg/q"

type Source struct {
	ID   int
	Name string
}

type Target struct {
	ID   string // mismatch: Source.ID is int
	Name string
}

func main() {
	s := Source{ID: 1, Name: "x"}
	_ = q.Convert[Target](s)
}
