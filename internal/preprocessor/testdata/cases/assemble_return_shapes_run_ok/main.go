// Fixture: every recipe return shape works inside one assembly.
//
//   NewX() X         — value type
//   NewX() *X        — pointer
//   NewX() Ifc       — interface
//   NewX() (X, err)  — value type with error
//   NewX() (*X, err) — pointer with error
//   NewX() (Ifc, err)— interface with error
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Ifc interface{ Tag() string }
type ifcImpl struct{ name string }

func (i ifcImpl) Tag() string { return i.name }

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

func newVal() Val { return q.MkTag[_val](1) }
func newPtr() Ptr { v := 2; return q.MkTag[_ptr](&v) }
func newIfc() IfcV { return q.MkTag[_ifc](Ifc(ifcImpl{name: "plain"})) }

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
	a := q.Unwrap(q.Assemble[*App](newVal, newPtr, newIfc, newValE, newPtrE, newIfcE, newApp))
	fmt.Println("v:", a.v, "p:", *a.p, "i:", a.i.Tag(), "ve:", a.ve, "pe:", *a.pe, "ie:", a.ie.Tag())
}
