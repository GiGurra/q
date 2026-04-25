// Fixture: q.F / q.Ferr / q.Fln string interpolation.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

type User struct {
	Name string
	Age  int
}

func main() {
	name := "world"
	age := 42

	// Simple interpolation
	fmt.Println(q.F("hello {name}"))
	fmt.Println(q.F("you are {age}"))

	// Multiple placeholders + arithmetic
	fmt.Println(q.F("hi {name}, {age+1} next year"))

	// Selector chain
	u := User{Name: "Alice", Age: 30}
	fmt.Println(q.F("user {u.Name} aged {u.Age}"))

	// Function call inside placeholder
	fmt.Println(q.F("upper: {strings.ToUpper(name)}"))

	// String literal containing braces inside the placeholder
	fmt.Println(q.F("got: {fmt.Sprintf(\"[%s]\", name)}"))

	// Brace escapes
	fmt.Println(q.F("literal {{ and }} braces"))
	fmt.Println(q.F("{{name}} stays literal but {name} expands"))

	// Percent escape
	fmt.Println(q.F("100% complete, {age}% done"))

	// No placeholders → constant string
	fmt.Println(q.F("constant"))

	// q.Ferr → error
	err := q.Ferr("bad input: {name}")
	fmt.Println(err)

	err2 := q.Ferr("plain")
	fmt.Println(err2)

	// q.Fln → fmt.Fprintln to q.DebugWriter (we capture)
	var buf bytes.Buffer
	q.DebugWriter = &buf
	q.Fln("debug: {name}={age}")
	q.Fln("plain debug")
	fmt.Print(buf.String())

	// Composition: q.Try wrapping a function that uses q.F
	v, err3 := wrapped("abc")
	fmt.Println(v, err3)

	// In an arbitrary expression position
	msg := strings.ToUpper(q.F("yelling at {name}"))
	fmt.Println(msg)

	// Sentinel-identity preserved through q.Ferr (no, q.Ferr doesn't
	// wrap anything — verifying that on purpose)
	target := errors.New("base")
	fmt.Println(errors.Is(q.Ferr("not wrapping {target}"), target))
}

func wrapped(s string) (string, error) {
	if s == "" {
		return "", q.Ferr("empty input rejected")
	}
	return q.F("processed {s}"), nil
}
