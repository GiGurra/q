// example/oneof mirrors docs/api/oneof.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/oneof
package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/GiGurra/q/pkg/q"
	"github.com/GiGurra/q/pkg/q/either"
)

// ---------- "Declare a sum" ----------
type Pending struct{}
type Done struct{ At time.Time }
type Failed struct{ Err error }

type Status q.OneOf3[Pending, Done, Failed]

// ---------- "Function output: producing a sum value" ----------
//
//	func currentStatus() Status {
//	    if pending {
//	        return q.AsOneOf[Status](Pending{})
//	    }
//	    if completed {
//	        return q.AsOneOf[Status](Done{At: time.Now()})
//	    }
//	    return q.AsOneOf[Status](Failed{Err: errors.New("timeout")})
//	}
func currentStatus(stage string, when time.Time) Status {
	if stage == "pending" {
		return q.AsOneOf[Status](Pending{})
	}
	if stage == "done" {
		return q.AsOneOf[Status](Done{At: when})
	}
	return q.AsOneOf[Status](Failed{Err: errors.New("timeout")})
}

// ---------- "Function input: passing a variant" ----------
//
//	func handle(s Status) { /* ... */ }
//	handle(q.AsOneOf[Status](Pending{}))
//	handle(q.AsOneOf[Status](Done{At: time.Now()}))
func handle(s Status) string {
	return describe(s)
}

// ---------- "Function input: accepting + dispatching a sum" ----------
//
//	func describe(s Status) {
//	    switch v := q.Exhaustive(s.Value).(type) {
//	    case Pending: fmt.Println("waiting")
//	    case Done:    fmt.Println("done at", v.At)
//	    case Failed:  fmt.Println("failed:", v.Err)
//	    }
//	}
func describe(s Status) string {
	switch v := q.Exhaustive(s.Value).(type) {
	case Pending:
		return "waiting"
	case Done:
		return "done at " + v.At.Format("15:04:05")
	case Failed:
		return "failed: " + v.Err.Error()
	}
	return ""
}

// ---------- "Atom variants" ----------
type Idle q.Atom
type Working q.Atom

type Activity q.OneOf2[Idle, Working]

func describeActivity(a Activity) string {
	switch v := q.Exhaustive(a.Value).(type) {
	case Idle:
		_ = v
		return "idle"
	case Working:
		_ = v
		return "working"
	}
	return ""
}

// ---------- "Expression-form dispatch — q.Match with q.Case + q.OnType" ----------
func describeStatusValue(s Status) string {
	return q.Match(s,
		q.Case(Pending{}, "waiting"),
		q.OnType(func(d Done) string { return "done at " + d.At.Format("15:04:05") }),
		q.OnType(func(f Failed) string { return "failed: " + f.Err.Error() }),
	)
}

// "Atom-arms via q.A[T]()":
func describeActivityValue(a Activity) string {
	return q.Match(a,
		q.Case(q.A[Idle](), "idle"),
		q.Case(q.A[Working](), "working"),
	)
}

// ---------- "Nested-sum dispatch (leaf-flattening)" ----------
type NotFound struct{ Path string }
type Forbidden struct{ Reason string }
type Created struct{ ID int }
type Updated struct{ ID int }

type ErrSet q.OneOf2[NotFound, Forbidden]
type OkSet q.OneOf2[Created, Updated]
type Result = either.Either[ErrSet, OkSet]

func describeResult(r Result) string {
	return q.Match(r,
		q.OnType(func(n NotFound) string { return "404: " + n.Path }),
		q.OnType(func(f Forbidden) string { return "403: " + f.Reason }),
		q.OnType(func(c Created) string { return fmt.Sprintf("201 (id=%d)", c.ID) }),
		q.OnType(func(u Updated) string { return fmt.Sprintf("200 (id=%d)", u.ID) }),
	)
}

func main() {
	when, _ := time.Parse(time.RFC3339, "2026-01-02T12:34:56Z")

	// Statement-level dispatch.
	fmt.Printf("describe(pending): %s\n", describe(currentStatus("pending", when)))
	fmt.Printf("describe(done): %s\n", describe(currentStatus("done", when)))
	fmt.Printf("describe(failed): %s\n", describe(currentStatus("other", when)))

	// Function input: variant wrapped at call site.
	fmt.Printf("handle(Pending): %s\n", handle(q.AsOneOf[Status](Pending{})))
	fmt.Printf("handle(Done): %s\n", handle(q.AsOneOf[Status](Done{At: when})))

	// Atom variants.
	fmt.Printf("describeActivity(Idle): %s\n", describeActivity(Activity{Tag: 1, Value: q.A[Idle]()}))
	fmt.Printf("describeActivity(Working): %s\n", describeActivity(Activity{Tag: 2, Value: q.A[Working]()}))

	// Expression-form dispatch.
	fmt.Printf("describeStatusValue(pending): %s\n", describeStatusValue(currentStatus("pending", when)))
	fmt.Printf("describeStatusValue(done): %s\n", describeStatusValue(currentStatus("done", when)))
	fmt.Printf("describeStatusValue(failed): %s\n", describeStatusValue(currentStatus("other", when)))

	fmt.Printf("describeActivityValue(Idle): %s\n", describeActivityValue(Activity{Tag: 1, Value: q.A[Idle]()}))
	fmt.Printf("describeActivityValue(Working): %s\n", describeActivityValue(Activity{Tag: 2, Value: q.A[Working]()}))

	// Nested-sum dispatch via either.Either.
	notFound := either.Left[ErrSet, OkSet](q.AsOneOf[ErrSet](NotFound{Path: "/missing"}))
	forbidden := either.Left[ErrSet, OkSet](q.AsOneOf[ErrSet](Forbidden{Reason: "no perm"}))
	created := either.Right[ErrSet, OkSet](q.AsOneOf[OkSet](Created{ID: 7}))
	updated := either.Right[ErrSet, OkSet](q.AsOneOf[OkSet](Updated{ID: 9}))

	fmt.Printf("describeResult(NotFound): %s\n", describeResult(notFound))
	fmt.Printf("describeResult(Forbidden): %s\n", describeResult(forbidden))
	fmt.Printf("describeResult(Created): %s\n", describeResult(created))
	fmt.Printf("describeResult(Updated): %s\n", describeResult(updated))
}
