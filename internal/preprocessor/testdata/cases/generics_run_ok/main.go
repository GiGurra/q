// Fixture: q.Try and q.NotNil work from inside generic functions and
// methods on generic types. The rewriter pulls return-type text from
// the enclosing FuncDecl, so it must spell type-parameter names like
// `T` correctly inside *new(T) — and method receivers on generic
// types like `Box[T]` must produce a working zero of T.
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

var ErrEmpty = errors.New("empty")

// firstNonNil takes a slice of *T and returns the first non-nil
// element, propagating ErrEmpty if every element is nil. The
// rewriter's *new(T) inside a generic body must use the parameter
// name T verbatim.
func firstNonNil[T any](items []*T) (T, error) {
	for _, p := range items {
		if p != nil {
			v := q.NotNil(p)
			return *v, nil
		}
	}
	return *new(T), ErrEmpty
}

// Box is a generic container with a method that uses q.Try.
type Box[T any] struct {
	v   T
	err error
}

// Get returns the boxed value or bubbles the boxed error. Method on a
// generic receiver — the rewriter's enclosing-FuncDecl walk must
// handle this shape.
func (b Box[T]) Get() (T, error) {
	v := q.Try(b.read())
	return v, nil
}

func (b Box[T]) read() (T, error) { return b.v, b.err }

func main() {
	a, b := 1, 2
	fmt.Println(firstNonNil([]*int{nil, &a, &b}))
	fmt.Println(firstNonNil([]*int{nil, nil, nil}))

	s1, s2 := "hello", "world"
	fmt.Println(firstNonNil([]*string{nil, &s1, &s2}))

	var noStrings []*string
	fmt.Println(firstNonNil(noStrings))

	intBox := Box[int]{v: 42}
	fmt.Println(intBox.Get())

	failBox := Box[int]{err: errors.New("boom")}
	fmt.Println(failBox.Get())

	strBox := Box[string]{v: "ok"}
	fmt.Println(strBox.Get())
}
