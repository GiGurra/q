// Fixture: every recipe return shape works inside one assembly.
//
//   NewX() X         — value type
//   NewX() *X        — pointer
//   NewX() Ifc       — interface
//   NewX() (X, err)  — value type with error
//   NewX() (*X, err) — pointer with error
//   NewX() (Ifc, err)— interface with error
//
// Distinct named-type wrappers brand each recipe so the provider
// map gets one slot per shape — no q.Tagged needed; plain Go
// embedding suffices.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Ifc interface{ Tag() string }
type ifcImpl struct{ name string }

func (i ifcImpl) Tag() string { return i.name }

// One named-type wrapper per recipe shape. struct{ ... } embedding
// gives each its own provider key without affecting the underlying
// type's behaviour.
type Val struct{ v int }
type Ptr struct{ v *int }
type IfcV struct{ Ifc }
type ValE struct{ v int }
type PtrE struct{ v *int }
type IfcVE struct{ Ifc }

func newVal() Val   { return Val{v: 1} }
func newPtr() Ptr   { v := 2; return Ptr{v: &v} }
func newIfc() IfcV  { return IfcV{Ifc: ifcImpl{name: "plain"}} }
func newValE() (ValE, error) { return ValE{v: 10}, nil }
func newPtrE() (PtrE, error) { v := 20; return PtrE{v: &v}, nil }
func newIfcE() (IfcVE, error) {
	return IfcVE{Ifc: ifcImpl{name: "errored"}}, nil
}

type App struct {
	v  int
	p  *int
	i  Ifc
	ve int
	pe *int
	ie Ifc
}

func newApp(v Val, p Ptr, i IfcV, ve ValE, pe PtrE, ie IfcVE) *App {
	return &App{
		v: v.v, p: p.v, i: i.Ifc,
		ve: ve.v, pe: pe.v, ie: ie.Ifc,
	}
}

func main() {
	a := q.Unwrap(q.Assemble[*App](newVal, newPtr, newIfc, newValE, newPtrE, newIfcE, newApp).DeferCleanup())
	fmt.Println("v:", a.v, "p:", *a.p, "i:", a.i.Tag(), "ve:", a.ve, "pe:", *a.pe, "ie:", a.ie.Tag())
}
