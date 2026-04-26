// Negative fixture: q.Set with a string-literal field name (the
// stale, refactor-hostile shape) is rejected at compile time. Users
// must use UserDTO{}.<Field> so Go's type-checker catches renames.
package main

import "github.com/GiGurra/q/pkg/q"

type Source struct {
	ID int
}

type Target struct {
	ID  int
	Tag string
}

func main() {
	_ = q.Convert[Target](Source{ID: 1},
		q.Set("Tag", "v1"), // wrong shape — must be Target{}.Tag
	)
}
