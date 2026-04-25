// Fixture: q.GenStringer / q.GenEnumJSONStrict / q.GenEnumJSONLax
// directives synthesize companion methods for the opted-in types.
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

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

// Directives — package-level opt-ins.
var _ = q.GenStringer[Color]()
var _ = q.GenEnumJSONStrict[Color]()
var _ = q.GenEnumJSONLax[Status]()

func main() {
	// GenStringer-synthesized method.
	fmt.Printf("color.String(): %s\n", Red.String())
	fmt.Printf("color.String(): %s\n", Green.String())
	fmt.Printf("unknown.String(): %q\n", Color(99).String())

	// GenEnumJSONStrict on Color: name-based, errors on unknown.
	if b, err := json.Marshal(Blue); err == nil {
		fmt.Println("color.Marshal:", string(b))
	}
	var c Color
	if err := json.Unmarshal([]byte(`"Green"`), &c); err == nil {
		fmt.Println("color.Unmarshal(Green):", int(c))
	}
	// Unknown name → error.
	if err := json.Unmarshal([]byte(`"Pink"`), &c); err != nil {
		// json wraps our error; strip the wrapping prefix for stable output.
		msg := err.Error()
		if i := strings.Index(msg, "q.GenEnumJSONStrict"); i >= 0 {
			msg = msg[i:]
		}
		fmt.Println("color.Unmarshal(Pink).err:", msg)
	}

	// GenEnumJSONLax on Status: passthrough, preserves unknown.
	if b, err := json.Marshal(Done); err == nil {
		fmt.Println("status.Marshal:", string(b))
	}
	var s Status
	if err := json.Unmarshal([]byte(`"pending"`), &s); err == nil {
		fmt.Println("status.Unmarshal(pending):", string(s))
	}
	// Forward-compat: unknown wire value preserved.
	if err := json.Unmarshal([]byte(`"future_state"`), &s); err == nil {
		fmt.Println("status.Unmarshal(future_state):", string(s))
		// Re-marshal: round-trips unchanged.
		if b, err := json.Marshal(s); err == nil {
			fmt.Println("status.Remarshal:", string(b))
		}
	}
	// Validity check at canonical sites.
	fmt.Println("future_state valid?", q.EnumValid[Status](s))
}
