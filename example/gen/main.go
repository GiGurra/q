// example/gen mirrors docs/api/gen.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/gen
package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- "At a glance" ----------
type Color int

const (
	Red Color = iota
	Green
	Blue
)

type Status string

const (
	Pending Status = "pending"
	Done    Status = "done"
	Failed  Status = "failed"
)

// Generated method directives.
var _ = q.GenStringer[Color]()
var _ = q.GenEnumJSONStrict[Color]()
var _ = q.GenEnumJSONLax[Status]()

// ---------- "Strict vs. Lax — which to pick" ----------
//
//	switch q.Exhaustive(s) {
//	case Pending: return "wait"
//	case Done:    return "ok"
//	default:      return forwardOpaque(s)
//	}
func handle(s Status) string {
	switch q.Exhaustive(s) {
	case Pending:
		return "wait"
	case Done:
		return "ok"
	case Failed:
		return "fail"
	default:
		return "unknown:" + string(s)
	}
}

func main() {
	// Stringer.
	fmt.Printf("Color.String: %v, %v, %v\n", Red, Green, Blue)

	// Strict JSON marshal.
	b, _ := json.Marshal(Green)
	fmt.Printf("strict json marshal Green: %s\n", b)

	// Strict JSON unmarshal — known value.
	var c Color
	if err := json.Unmarshal([]byte(`"Blue"`), &c); err != nil {
		fmt.Printf("strict unmarshal Blue: err=%s\n", err)
	} else {
		fmt.Printf("strict unmarshal Blue: %v\n", c)
	}

	// Strict JSON unmarshal — unknown value (errors).
	if err := json.Unmarshal([]byte(`"Pink"`), &c); err != nil {
		fmt.Printf("strict unmarshal Pink: err=%s, is(q.ErrEnumUnknown)=%v\n", err, errors.Is(err, q.ErrEnumUnknown))
	}

	// Lax JSON marshal.
	bs, _ := json.Marshal(Done)
	fmt.Printf("lax json marshal Done: %s\n", bs)

	// Lax JSON unmarshal — preserves unknown value verbatim.
	var s Status
	if err := json.Unmarshal([]byte(`"future-value"`), &s); err != nil {
		fmt.Printf("lax unmarshal future-value: err=%s\n", err)
	} else {
		fmt.Printf("lax unmarshal future-value: %s\n", s)
	}

	// Exhaustive over Lax-opted Status — default catches the unknown.
	fmt.Printf("handle(Pending): %s\n", handle(Pending))
	fmt.Printf("handle(Done): %s\n", handle(Done))
	fmt.Printf("handle(Failed): %s\n", handle(Failed))
	fmt.Printf("handle(future-value): %s\n", handle(Status("future-value")))
}
