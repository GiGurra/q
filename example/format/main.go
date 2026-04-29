// example/format mirrors docs/api/format.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/format
package main

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

// touchStrings keeps the strings import visible to lint / vet.
// q.F("{strings.ToUpper(...)}") references strings inside a literal,
// which the rewriter splices out — but the SOURCE file (pre-rewrite)
// must already import strings, and lint sees it as unused without
// at least one direct mention.
var _ = strings.ToUpper

type User struct{ Name string }

// ---------- "At a glance" ----------

func atAGlance() {
	// As the doc notes, identifiers used only inside q.F's format
	// literal aren't seen by `go vet` / staticcheck. Use the variables
	// once outside the format too so the IDE / lint tooling stays
	// happy on this example file.
	name := "world"
	age := 42
	_, _ = name, age // keep lint happy; q.F still references both

	fmt.Println(q.F("hello {name}"))
	fmt.Println(q.F("hi {name}, {age+1} next"))
	fmt.Println(q.F("upper: {strings.ToUpper(name)}"))

	if err := q.Ferr("user {age} not found"); err != nil {
		fmt.Println(err)
	}
}

// ---------- "Brace escapes" ----------
func braceEscapes() {
	name := "world"
	age := 42
	_, _ = name, age
	fmt.Println(q.F("literal {{ and }} braces"))
	fmt.Println(q.F("{{name}} stays literal"))
	fmt.Println(q.F("{{ {name} }}"))
	fmt.Println(q.F("100% complete, {age}% done"))
}

// ---------- "What goes inside {expr}" ----------
func whatGoesInside() {
	u := User{Name: "Ada"}
	a, b := 3, 4
	s := "hello"
	items := []string{"x", "y", "z"}
	name := "Linus"
	_, _, _, _, _, _ = u, a, b, s, items, name

	fmt.Println(q.F("name: {u.Name}"))
	fmt.Println(q.F("sum: {a + b}"))
	fmt.Println(q.F("upper: {strings.ToUpper(s)}"))
	fmt.Println(q.F("first: {items[0]}"))
	fmt.Println(q.F("got: {fmt.Sprintf(\"[%s]\", name)}"))
}

// ---------- "q.Ferr — fresh error vs trivial case" ----------
func ferrDemos() {
	id := 7
	_ = id
	err := q.Ferr("user {id} not found")
	fmt.Println(err)

	// No placeholders → rewrites to errors.New under the hood.
	err = q.Ferr("constant error")
	fmt.Println(err)
}

// ---------- "q.Fln — debug print" ----------
func flnDemo() string {
	var buf bytes.Buffer
	q.DebugWriter = &buf
	user := User{Name: "Ada"}
	items := []int{1, 2, 3}
	_, _ = user, items
	q.Fln("processing {len(items)} items for user {user.Name}")
	return buf.String()
}

// ---------- "Statement forms" ----------
func statementForms() {
	name := "world"
	_ = name

	msg := q.F("hi {name}")
	fmt.Println(msg)

	msg = q.F("hi {name} again")
	fmt.Println(msg)

	q.F("hi {name}") // discard (rare; result wasted)

	out, _ := returnDemo("forms-name")
	fmt.Println(out)

	hoist(q.F("hi {name}"))
}

func returnDemo(name string) (string, error) {
	_ = name
	return q.F("hi {name}"), nil
}

func hoist(s string) { fmt.Println("hoist:", s) }

func main() {
	atAGlance()
	braceEscapes()
	whatGoesInside()
	ferrDemos()
	fmt.Print("flnDemo: " + flnDemo())
	statementForms()
}
