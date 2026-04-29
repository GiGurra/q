// example/string_case mirrors docs/api/string_case.md one-to-one.
// Run with:
//
//	go run -toolexec=q ./example/string_case
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- "At a glance" ----------
func atAGlance() {
	fmt.Printf("Upper(hello)=%q\n", q.Upper("hello"))
	fmt.Printf("Lower(HELLO)=%q\n", q.Lower("HELLO"))
	fmt.Printf("Snake(HelloWorld)=%q\n", q.Snake("HelloWorld"))
	fmt.Printf("Snake(XMLHttpRequest)=%q\n", q.Snake("XMLHttpRequest"))
	fmt.Printf("Snake(hello-world)=%q\n", q.Snake("hello-world"))
	fmt.Printf("Kebab(HelloWorld)=%q\n", q.Kebab("HelloWorld"))
	fmt.Printf("Camel(hello_world)=%q\n", q.Camel("hello_world"))
	fmt.Printf("Camel(XMLHttpRequest)=%q\n", q.Camel("XMLHttpRequest"))
	fmt.Printf("Pascal(hello_world)=%q\n", q.Pascal("hello_world"))
	fmt.Printf("Pascal(XML_HTTP_REQUEST)=%q\n", q.Pascal("XML_HTTP_REQUEST"))
	fmt.Printf("Title(hello world)=%q\n", q.Title("hello world"))
}

// ---------- "Use cases" — also works at package-level var ----------
//
// Helpers fold to string literals in the rewritten source. Both
// function bodies and package-level `var x = q.Snake(...)` are
// walked. `const` is rejected (Go's typechecker rejects function
// calls in const initializers before any rewrite happens).
var (
	userIDColumn = q.Snake("UserID") // "user_id"
	dbHostEnv    = q.Upper("db_host") // "DB_HOST" (literals only — no nested q.* calls)
)

func useCases() {
	url := "/posts/" + q.Kebab("My First Post") // "/posts/my-first-post"

	fmt.Printf("userIDColumn=%q\n", userIDColumn)
	fmt.Printf("dbHostEnv=%q\n", dbHostEnv)
	fmt.Printf("url=%q\n", url)
	fmt.Printf("Camel(user_id)=%q\n", q.Camel("user_id"))
}

func main() {
	atAGlance()
	fmt.Println("---")
	useCases()
}
