// Fixture: every recipe return shape works inside one assembly.
//
//   NewX() X         — value type
//   NewX() *X        — pointer
//   NewX() Ifc       — interface
//   NewX() (X, err)  — value type with error
//   NewX() (*X, err) — pointer with error
//   NewX() (Ifc, err)— interface with error
//
// q.Tagged is used to brand otherwise-identical types so each shape
// occupies a distinct slot in the dep graph (without it, e.g. two
// providers of `int` would clash on duplicate-provider).
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Ifc interface{ Tag() string }
type ifcImpl struct{ name string }

func (i ifcImpl) Tag() string { return i.name }

// Brand types — empty structs whose only role is to differentiate
// otherwise-identical types in the recipe graph.
type _val struct{}
type _ptr struct{}
type _ifc struct{}
type _vale struct{}
type _ptre struct{}
type _ifce struct{}

type Val   = q.Tagged[int, _val]
type Ptr   = q.Tagged[*int, _ptr]
type IfcV  = q.Tagged[Ifc, _ifc]
type ValE  = q.Tagged[int, _vale]
type PtrE  = q.Tagged[*int, _ptre]
type IfcVE = q.Tagged[Ifc, _ifce]

// Pure recipes — one per return shape.
func newVal() Val { return q.MkTag[_val](1) }
func newPtr() Ptr { v := 2; return q.MkTag[_ptr](&v) }
func newIfc() IfcV { return q.MkTag[_ifc](Ifc(ifcImpl{name: "plain"})) }

// Errored recipes — one per return shape. All return nil-error in this
// happy-path fixture; the bind+bubble shape is exercised by the rewrite
// regardless of whether the error path actually fires at runtime.
func newValE() (ValE, error)  { return q.MkTag[_vale](10), nil }
func newPtrE() (PtrE, error)  { v := 20; return q.MkTag[_ptre](&v), nil }
func newIfcE() (IfcVE, error) { return q.MkTag[_ifce](Ifc(ifcImpl{name: "errored"})), nil }

type App struct {
	v   int
	p   *int
	i   Ifc
	ve  int
	pe  *int
	ie  Ifc
}

func newApp(v Val, p Ptr, i IfcV, ve ValE, pe PtrE, ie IfcVE) *App {
	return &App{v: v.Value(), p: p.Value(), i: i.Value(), ve: ve.Value(), pe: pe.Value(), ie: ie.Value()}
}

func main() {
	a, err := q.AssembleErr[*App](newVal, newPtr, newIfc, newValE, newPtrE, newIfcE, newApp)
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println("v:", a.v, "p:", *a.p, "i:", a.i.Tag(), "ve:", a.ve, "pe:", *a.pe, "ie:", a.ie.Tag())
}
