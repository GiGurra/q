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

// Opener wraps acquire + cleanup in a generic function so the
// rewriter exercises q.Open[T] under a user-provided type
// parameter. The cleanup records via the global log so callers can
// verify the defer fires with the generic type's value.
var openGenericsLog []string

//q:no-escape-check
func takeGeneric[T any](produce func() (T, error), cleanup func(T)) (T, error) {
	v := q.Open(produce()).DeferCleanup(cleanup)
	return v, nil
}

func logClose(s string) {
	openGenericsLog = append(openGenericsLog, s)
}

// Holder is a generic type with a method that uses q.Open; verifies
// the DeferCleanup cleanup handles the type parameter correctly on the
// receiver-method path.
type Holder[T any] struct {
	v   T
	err error
}

//q:no-escape-check
func (h Holder[T]) acquireWith(cleanup func(T)) (T, error) {
	v := q.Open(h.supply()).DeferCleanup(cleanup)
	return v, nil
}

func (h Holder[T]) supply() (T, error) { return h.v, h.err }

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

	// q.Open in a generic function with cleanup.
	openGenericsLog = openGenericsLog[:0]
	gv, gerr := takeGeneric(func() (string, error) { return "abc", nil }, logClose)
	fmt.Printf("openGeneric.ok v=%q err=%v log=%v\n", gv, gerr, openGenericsLog)

	openGenericsLog = openGenericsLog[:0]
	gv, gerr = takeGeneric(func() (string, error) { return "", errors.New("nope") }, logClose)
	fmt.Printf("openGeneric.bad v=%q err=%v log=%v\n", gv, gerr, openGenericsLog)

	// q.Open on a method of a generic type.
	openGenericsLog = openGenericsLog[:0]
	h := Holder[string]{v: "held"}
	hv, herr := h.acquireWith(logClose)
	fmt.Printf("openHolder.ok v=%q err=%v log=%v\n", hv, herr, openGenericsLog)

	openGenericsLog = openGenericsLog[:0]
	hBad := Holder[string]{err: errors.New("hold failed")}
	hv, herr = hBad.acquireWith(logClose)
	fmt.Printf("openHolder.bad v=%q err=%v log=%v\n", hv, herr, openGenericsLog)
}
