// Negative fixture: target field has no source counterpart →
// build fails with a per-field diagnostic from resolveConvert.
package main

import "github.com/GiGurra/q/pkg/q"

type Source struct {
	ID   int
	Name string
}

type Target struct {
	ID    int
	Name  string
	Email string // unmapped — no Source.Email
}

func main() {
	s := Source{ID: 1, Name: "x"}
	_ = q.Convert[Target](s)
}
