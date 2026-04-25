// Fixture: in-place q.* calls inside container-statement headers —
// IfStmt.Init, IfStmt.Cond, ForStmt.Init/Cond/Post, RangeStmt.X,
// SwitchStmt.Init, SwitchStmt.Tag (non-Exhaustive). The rewriter
// must produce a same-line span substitution; multi-line rewrites
// would break the header parse.
package main

import (
	"fmt"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

type Color int

const (
	Red Color = iota
	Green
	Blue
)

func main() {
	// IfStmt.Init: in-place rewrite inside `if v := ...; cond { ... }`.
	if name := q.EnumName[Color](Green); name != "" {
		fmt.Printf("if-init: %s\n", name)
	}

	// IfStmt.Cond: in-place rewrite inside the condition expression.
	if q.EnumValid[Color](Color(99)) {
		fmt.Println("if-cond: unreachable")
	} else {
		fmt.Println("if-cond: 99 not valid")
	}

	// RangeStmt.X: range over a literal slice produced at compile time.
	for _, c := range q.EnumValues[Color]() {
		fmt.Printf("range: %s ord=%d\n", q.EnumName[Color](c), q.EnumOrdinal[Color](c))
	}

	// ForStmt.Init / Cond / Post — in-place expressions in the header.
	for i := q.EnumOrdinal[Color](Red); i < q.EnumOrdinal[Color](Blue)+1; i++ {
		fmt.Printf("for-classic: %d\n", i)
	}

	// SwitchStmt.Init: q.* in the SimpleStmt before the tag.
	switch name := q.EnumName[Color](Blue); name {
	case "Blue":
		fmt.Printf("switch-init: matched %s\n", name)
	default:
		fmt.Println("switch-init: unmatched")
	}

	// SwitchStmt.Tag (non-Exhaustive): in-place expression as the tag.
	switch q.EnumName[Color](Green) {
	case "Green":
		fmt.Println("switch-tag: matched Green")
	default:
		fmt.Println("switch-tag: unmatched")
	}

	// Composition: q.F nested inside an if-init AssignStmt.
	greeting := "world"
	if msg := q.F("hi {greeting}!"); strings.HasPrefix(msg, "hi") {
		fmt.Printf("if-init-F: %s\n", msg)
	}

	// Range over q.EnumNames inline.
	for i, n := range q.EnumNames[Color]() {
		fmt.Printf("range-names: %d=%s\n", i, n)
	}
}
