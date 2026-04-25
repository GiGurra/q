// Fixture: every recipe return shape works.
//
//   NewX() X         — value type
//   NewX() *X        — pointer
//   NewX() Ifc       — interface
//   NewX() (X, err)  — value type with error
//   NewX() (*X, err) — pointer with error
//   NewX() (Ifc, err)— interface with error
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type ValT struct{ x int }
type RefT struct{ x int }

type Ifc interface{ Tag() string }
type ifcImpl struct{ name string }

func (i ifcImpl) Tag() string { return i.name }

type App struct {
	v   ValT
	p   *RefT
	i   Ifc
	v2  ValT
	p2  *RefT
	i2  Ifc
}

func newVal() ValT             { return ValT{x: 1} }
func newPtr() *RefT            { return &RefT{x: 2} }
func newIfc() Ifc              { return ifcImpl{name: "plain"} }
func newValE() (ValT, error)   { return ValT{x: 10}, nil }
func newPtrE() (*RefT, error)  { return &RefT{x: 20}, nil }
func newIfcE() (Ifc, error)    { return ifcImpl{name: "errored"}, nil }
func newApp(v ValT, p *RefT, i Ifc, v2 ValT, p2 *RefT, i2 Ifc) (*App, error) {
	if v.x == 0 {
		return nil, errors.New("zero")
	}
	return &App{v: v, p: p, i: i, v2: v2, p2: p2, i2: i2}, nil
}

// Tag the value-typed providers so we have distinct keys for v vs v2.
type _alt struct{}

type ValT2 = q.Tagged[ValT, _alt]
type RefT2 = q.Tagged[*RefT, _alt]
type Ifc2  = q.Tagged[Ifc, _alt]

func newValT2() ValT2 { return q.MkTag[_alt](ValT{x: 100}) }
func newRefT2() RefT2 { return q.MkTag[_alt](&RefT{x: 200}) }
func newIfcT2() Ifc2  { return q.MkTag[_alt](Ifc(ifcImpl{name: "tagged"})) }

func newApp2(v ValT, p *RefT, i Ifc, v2 ValT2, p2 RefT2, i2 Ifc2) (*App, error) {
	return &App{v: v, p: p, i: i, v2: v2.Value(), p2: p2.Value(), i2: i2.Value()}, nil
}

func main() {
	a, err := q.AssembleErr[*App](newVal, newPtr, newIfc, newValT2, newRefT2, newIfcT2, newApp2)
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println("v.x:", a.v.x, "p.x:", a.p.x, "i:", a.i.Tag(), "v2.x:", a.v2.x, "p2.x:", a.p2.x, "i2:", a.i2.Tag())
}
