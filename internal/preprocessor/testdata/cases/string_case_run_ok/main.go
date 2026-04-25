// Fixture: q.Upper / q.Lower / q.Snake / q.Kebab / q.Camel /
// q.Pascal / q.Title — compile-time string-case transforms.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	// Upper / Lower
	fmt.Println(q.Upper("hello"))
	fmt.Println(q.Lower("HELLO"))

	// Snake — case-boundary detection
	fmt.Println(q.Snake("HelloWorld"))
	fmt.Println(q.Snake("XMLHttpRequest"))
	fmt.Println(q.Snake("hello-world"))
	fmt.Println(q.Snake("hello world"))
	fmt.Println(q.Snake("alreadySnake_case"))
	fmt.Println(q.Snake("URL"))

	// Kebab
	fmt.Println(q.Kebab("HelloWorld"))
	fmt.Println(q.Kebab("XMLHttpRequest"))
	fmt.Println(q.Kebab("hello_world"))

	// Camel
	fmt.Println(q.Camel("hello_world"))
	fmt.Println(q.Camel("hello-world"))
	fmt.Println(q.Camel("hello world"))
	fmt.Println(q.Camel("HelloWorld"))
	fmt.Println(q.Camel("XMLHttpRequest"))

	// Pascal
	fmt.Println(q.Pascal("hello_world"))
	fmt.Println(q.Pascal("helloWorld"))
	fmt.Println(q.Pascal("XML_HTTP_REQUEST"))

	// Title
	fmt.Println(q.Title("hello world"))
	fmt.Println(q.Title("a b c"))

	// Each call site folds to a literal at compile time — verify
	// by passing through a const-position context.
	const greeting = "" + // forces compile-time evaluation
		"" // (placeholder to anchor the const decl)
	_ = greeting
	col := q.Snake("UserID")
	fmt.Printf("column: %s\n", col)
}
