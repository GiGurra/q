// Fixture: either.Either[L, R] — Scala-flavoured 2-arm sum type.
// Demonstrates Left/Right constructors, AsEither, Fold, Map, FlatMap,
// IsLeft/RightOk, and integration with q.Match (q.OnType / q.Default).
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
type Response struct {
	Body string
}

// Result names the Either alias for clarity at use sites.
type Result = either.Either[Error, Response]

func process(badInput bool) Result {
	if badInput {
		return either.AsEither[Result](Error{Code: 400, Msg: "bad input"})
	}
	return either.AsEither[Result](Response{Body: "ok"})
}

// Same with the named constructors:
func processNamed(badInput bool) Result {
	if badInput {
		return either.Left[Error, Response](Error{Code: 401, Msg: "auth"})
	}
	return either.Right[Error, Response](Response{Body: "yay"})
}

func main() {
	r := process(true)
	fmt.Println("left set?", r.IsLeft(), "right set?", r.IsRight())

	if e, ok := r.LeftOk(); ok {
		fmt.Printf("left: code=%d msg=%s\n", e.Code, e.Msg)
	}

	r2 := process(false)
	if v, ok := r2.RightOk(); ok {
		fmt.Println("right:", v.Body)
	}

	// Fold:
	desc := either.Fold(r,
		func(e Error) string { return fmt.Sprintf("err %d", e.Code) },
		func(r Response) string { return r.Body },
	)
	fmt.Println("fold:", desc)

	// Map (right-biased):
	mapped := either.Map(r2, func(r Response) int { return len(r.Body) })
	if v, ok := mapped.RightOk(); ok {
		fmt.Println("mapped len:", v)
	}

	// FlatMap:
	chained := either.FlatMap(r2, func(r Response) either.Either[Error, int] {
		if len(r.Body) < 5 {
			return either.Right[Error, int](42)
		}
		return either.Left[Error, int](Error{Code: 500, Msg: "too long"})
	})
	if v, ok := chained.RightOk(); ok {
		fmt.Println("chained right:", v)
	}

	// q.Match integration via q.OnType (Either is structurally a 2-arm sum):
	match := q.Match(r2,
		q.OnType(func(e Error) string { return "matched err: " + e.Msg }),
		q.OnType(func(r Response) string { return "matched ok: " + r.Body }),
	)
	fmt.Println("match:", match)

	// q.Match with a tag-only q.Case for one arm:
	mixed := q.Match(r,
		q.Case(Response{}, "got response"),
		q.OnType(func(e Error) string { return "got err " + e.Msg }),
	)
	fmt.Println("mixed:", mixed)

	// q.Exhaustive type switch on Value:
	switch v := q.Exhaustive(r.Value).(type) {
	case Error:
		fmt.Printf("stmt err: code=%d\n", v.Code)
	case Response:
		fmt.Println("stmt ok:", v.Body)
	}

	// Named-constructor + Swap:
	rn := processNamed(true)
	swapped := either.Swap(rn)
	if v, ok := swapped.RightOk(); ok {
		fmt.Printf("swapped right (was left): code=%d\n", v.Code)
	}

	// Tag inspection:
	fmt.Println("r.Tag:", r.Tag, "r2.Tag:", r2.Tag)
}
