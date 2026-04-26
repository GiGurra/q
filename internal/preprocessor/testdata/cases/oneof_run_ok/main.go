// Fixture: q.OneOfN sum types — q.AsOneOf construction, q.Match
// dispatch via q.Case (type-tagged) + q.OnType (payload-binding),
// and q.Exhaustive type switch on .Value.
package main

import (
	"fmt"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

// Variant types. Each variant can be a struct, a primitive, an atom —
// anything Go-typed.
type Pending struct{}
type Done struct {
	At time.Time
}
type Failed struct {
	Err error
}

type Status q.OneOf3[Pending, Done, Failed]

// Atom-flavoured sum: each variant is a named string (q.Atom).
type Idle q.Atom
type Working q.Atom

type Activity q.OneOf2[Idle, Working]

// describe uses q.Case for tag-only arms (variant payload not needed)
// AND q.OnType for arms that bind the variant's payload.
func describe(s Status) string {
	return q.Match(s,
		q.Case(Pending{}, "waiting"),
		q.OnType(func(d Done) string { return "done at " + d.At.UTC().Format("15:04") }),
		q.OnType(func(f Failed) string { return "failed: " + f.Err.Error() }),
	)
}

// allCase uses q.Case-only — payloads are discarded.
func quickStatus(s Status) string {
	return q.Match(s,
		q.Case(Pending{}, "p"),
		q.Case(Done{}, "d"),
		q.Case(Failed{}, "f"),
	)
}

// withDefault: q.Default substitutes for missing arms.
func partial(s Status) string {
	return q.Match(s,
		q.Case(Pending{}, "waiting"),
		q.Default("not waiting"),
	)
}

// atomVariants: a sum of atoms.
func describeActivity(a Activity) string {
	return q.Match(a,
		q.Case(q.A[Idle](), "idle"),
		q.Case(q.A[Working](), "working"),
	)
}

// statementSwitch: q.Exhaustive coverage on the type-switch over .Value.
func describeStmt(s Status) string {
	switch v := q.Exhaustive(s.Value).(type) {
	case Pending:
		_ = v
		return "stmt:waiting"
	case Done:
		return "stmt:done at " + v.At.UTC().Format("15:04")
	case Failed:
		return "stmt:failed: " + v.Err.Error()
	}
	return "?"
}

func main() {
	fixed, _ := time.Parse("2006-01-02T15:04:05Z", "2026-01-15T10:30:00Z")

	pending := q.AsOneOf[Status](Pending{})
	done := q.AsOneOf[Status](Done{At: fixed})
	failed := q.AsOneOf[Status](Failed{Err: fmt.Errorf("disk full")})

	fmt.Println(describe(pending))
	fmt.Println(describe(done))
	fmt.Println(describe(failed))

	fmt.Println(quickStatus(pending))
	fmt.Println(quickStatus(done))
	fmt.Println(quickStatus(failed))

	fmt.Println(partial(pending))
	fmt.Println(partial(done))

	idle := q.AsOneOf[Activity](q.A[Idle]())
	working := q.AsOneOf[Activity](q.A[Working]())
	fmt.Println(describeActivity(idle))
	fmt.Println(describeActivity(working))

	fmt.Println(describeStmt(pending))
	fmt.Println(describeStmt(done))
	fmt.Println(describeStmt(failed))

	// Tag inspection — variants get 1-based positions.
	fmt.Println("pending.Tag:", pending.Tag)
	fmt.Println("done.Tag:", done.Tag)
	fmt.Println("failed.Tag:", failed.Tag)
}
