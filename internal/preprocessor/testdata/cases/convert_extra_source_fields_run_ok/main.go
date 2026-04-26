// Fixture: target-driven Chimney semantics. Source has many more
// exported fields than target; auto-derivation pairs only the ones
// the target asks for and silently drops the extras. No overrides
// needed.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// SourceWide has 8 exported fields.
type SourceWide struct {
	ID           int
	Name         string
	Email        string
	InternalNote string
	CreatedAt    string
	UpdatedAt    string
	IsAdmin      bool
	Score        float64
}

// TargetSlim only asks for 3.
type TargetSlim struct {
	ID    int
	Name  string
	Email string
}

func main() {
	src := SourceWide{
		ID:           7,
		Name:         "Ada",
		Email:        "ada@example.com",
		InternalNote: "ignore me",
		CreatedAt:    "2026-01-01",
		UpdatedAt:    "2026-04-01",
		IsAdmin:      true,
		Score:        99.5,
	}
	dst := q.ConvertTo[TargetSlim](src)
	fmt.Printf("dst: %+v\n", dst)
}
