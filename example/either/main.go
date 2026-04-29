// example/either mirrors docs/api/either.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/either
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
	"github.com/GiGurra/q/pkg/q/either"
)

type Error struct {
	Code int
	Msg  string
}
type Response struct{ Body string }
type Request struct{ Body string }

func (r Request) Valid() bool { return r.Body != "" }

type Result = either.Either[Error, Response]

// ---------- Top-of-doc — process ----------
//
//	func process(req Request) Result {
//	    if !req.Valid() {
//	        return either.Left[Error, Response](Error{Code: 400, Msg: "bad input"})
//	    }
//	    return either.Right[Error, Response](Response{Body: "ok"})
//	}
func process(req Request) Result {
	if !req.Valid() {
		return either.Left[Error, Response](Error{Code: 400, Msg: "bad input"})
	}
	return either.Right[Error, Response](Response{Body: "ok"})
}

// "Scala-style fold":
//
//	desc := either.Fold(r,
//	    func(e Error) string    { return e.Msg },
//	    func(r Response) string { return r.Body },
//	)
func describeFold(r Result) string {
	return either.Fold(r,
		func(e Error) string { return e.Msg },
		func(r Response) string { return r.Body },
	)
}

// "q.Match + q.OnType integration":
func describeMatch(r Result) string {
	return q.Match(r,
		q.OnType(func(e Error) string { return e.Msg }),
		q.OnType(func(r Response) string { return r.Body }),
	)
}

// ---------- "Three ways to construct" ----------

func ctorNamed() (Result, Result) {
	r := either.Right[Error, Response](Response{Body: "ok-named"})
	e := either.Left[Error, Response](Error{Code: 1, Msg: "err-named"})
	return r, e
}

func ctorAsEither() (Result, Result) {
	r := either.AsEither[Result](Response{Body: "ok-aseither"})
	e := either.AsEither[Result](Error{Code: 2, Msg: "err-aseither"})
	return r, e
}

func ctorAsOneOf() Result {
	return q.AsOneOf[Result](Response{Body: "ok-asoneof"})
}

// ---------- "Right-biased operations" ----------

type Decoded struct{ N int }

func decode(body string) either.Either[Error, Decoded] {
	if body == "" {
		return either.Left[Error, Decoded](Error{Msg: "empty"})
	}
	return either.Right[Error, Decoded](Decoded{N: len(body)})
}

func rightBiased(r Result) (either.Either[Error, int], either.Either[Error, Decoded], Response) {
	mapped := either.Map(r, func(r Response) int { return len(r.Body) })
	chained := either.FlatMap(r, func(r Response) either.Either[Error, Decoded] {
		return decode(r.Body)
	})
	body := either.GetOrElse(r, Response{Body: "default"})
	return mapped, chained, body
}

func mapLeftSwap(r Result) (either.Either[string, Response], either.Either[Response, Error]) {
	left2 := either.MapLeft(r, func(e Error) string { return fmt.Sprintf("[%d] %s", e.Code, e.Msg) })
	swapped := either.Swap(r)
	return left2, swapped
}

// ---------- "Coverage with q.Exhaustive" ----------

func exhaustiveCoverage(r Result) string {
	switch v := q.Exhaustive(r.Value).(type) {
	case Error:
		return fmt.Sprintf("err: %d %s", v.Code, v.Msg)
	case Response:
		return fmt.Sprintf("ok: %s", v.Body)
	}
	return "" // unreachable — q.Exhaustive guarantees coverage
}

func main() {
	ok := process(Request{Body: "x"})
	bad := process(Request{Body: ""})

	fmt.Printf("process(ok).IsRight=%v\n", ok.IsRight())
	fmt.Printf("process(bad).IsLeft=%v\n", bad.IsLeft())

	fmt.Printf("describeFold(ok): %s\n", describeFold(ok))
	fmt.Printf("describeFold(bad): %s\n", describeFold(bad))
	fmt.Printf("describeMatch(ok): %s\n", describeMatch(ok))
	fmt.Printf("describeMatch(bad): %s\n", describeMatch(bad))

	rN, eN := ctorNamed()
	if v, hasR := rN.RightOk(); hasR {
		fmt.Printf("ctorNamed.r: %s\n", v.Body)
	}
	if v, hasL := eN.LeftOk(); hasL {
		fmt.Printf("ctorNamed.e: %d %s\n", v.Code, v.Msg)
	}

	rA, eA := ctorAsEither()
	fmt.Printf("ctorAsEither.r.IsRight=%v\n", rA.IsRight())
	fmt.Printf("ctorAsEither.e.IsLeft=%v\n", eA.IsLeft())

	rOo := ctorAsOneOf()
	fmt.Printf("ctorAsOneOf.IsRight=%v\n", rOo.IsRight())

	mapped, chained, body := rightBiased(ok)
	fmt.Printf("Map(ok)=%v IsRight=%v\n", mapped, mapped.IsRight())
	fmt.Printf("FlatMap(ok)=%v IsRight=%v\n", chained, chained.IsRight())
	fmt.Printf("GetOrElse(ok)=%v\n", body)

	mappedBad, _, bodyBad := rightBiased(bad)
	fmt.Printf("Map(bad).IsLeft=%v\n", mappedBad.IsLeft())
	fmt.Printf("GetOrElse(bad)=%v\n", bodyBad)

	left2, swapped := mapLeftSwap(bad)
	fmt.Printf("MapLeft(bad).IsLeft=%v left=%+v\n", left2.IsLeft(), left2.Value)
	fmt.Printf("Swap(bad).IsRight=%v (left of original is now right)\n", swapped.IsRight())

	fmt.Printf("exhaustiveCoverage(ok): %s\n", exhaustiveCoverage(ok))
	fmt.Printf("exhaustiveCoverage(bad): %s\n", exhaustiveCoverage(bad))
}
