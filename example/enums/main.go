// example/enums mirrors docs/api/enums.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/enums
package main

import (
	"encoding/json"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- "At a glance — int enum" ----------
type Color int

const (
	Red Color = iota
	Green
	Blue
)

// "Rolling your own Stringer":
//
//	func (c Color) String() string { return q.EnumName[Color](c) }
func (c Color) String() string { return q.EnumName[Color](c) }

// "JSON / text marshalling":
//
//	func (c Color) MarshalText() ([]byte, error)
func (c Color) MarshalText() ([]byte, error) {
	return []byte(q.EnumName[Color](c)), nil
}

func (c *Color) UnmarshalText(b []byte) error {
	parsed, err := q.EnumParse[Color](string(b))
	if err != nil {
		return err
	}
	*c = parsed
	return nil
}

// ---------- "At a glance — string enum" ----------
type Status string

const (
	Pending Status = "pending"
	Done    Status = "done"
	Failed  Status = "failed"
)

// "Graceful fallback Stringer for string enums":
func (s Status) String() string {
	if name := q.EnumName[Status](s); name != "" {
		return name
	}
	return string(s)
}

// ---------- "Value-based parsing wrapper" ----------
//
//	func ParseStatusValue(s string) (Status, error) {
//	    v := Status(s)
//	    if !q.EnumValid[Status](v) {
//	        return "", q.ErrEnumUnknown
//	    }
//	    return v, nil
//	}
func ParseStatusValue(s string) (Status, error) {
	v := Status(s)
	if !q.EnumValid[Status](v) {
		return "", q.ErrEnumUnknown
	}
	return v, nil
}

// ---------- "Exhaustive switches" ----------
//
//	switch q.Exhaustive(c) {
//	case Red: return "warm"
//	case Green: return "natural"
//	case Blue: return "cool"
//	}
func warmth(c Color) string {
	switch q.Exhaustive(c) {
	case Red:
		return "warm"
	case Green:
		return "natural"
	case Blue:
		return "cool"
	}
	return ""
}

func main() {
	fmt.Printf("EnumValues[Color]: %v\n", q.EnumValues[Color]())
	fmt.Printf("EnumNames[Color]: %v\n", q.EnumNames[Color]())

	fmt.Printf("EnumName[Color](Green): %q\n", q.EnumName[Color](Green))
	fmt.Printf("EnumName[Color](Color(99)): %q\n", q.EnumName[Color](Color(99)))

	if v, err := q.EnumParse[Color]("Green"); err != nil {
		fmt.Printf("EnumParse[Color](Green): err=%s\n", err)
	} else {
		fmt.Printf("EnumParse[Color](Green): %v\n", v)
	}
	if _, err := q.EnumParse[Color]("Pink"); err != nil {
		fmt.Printf("EnumParse[Color](Pink): err=%s\n", err)
	}

	fmt.Printf("EnumValid[Color](Red): %v\n", q.EnumValid[Color](Red))
	fmt.Printf("EnumValid[Color](Color(99)): %v\n", q.EnumValid[Color](Color(99)))

	fmt.Printf("EnumOrdinal[Color](Blue): %d\n", q.EnumOrdinal[Color](Blue))
	fmt.Printf("EnumOrdinal[Color](Color(99)): %d\n", q.EnumOrdinal[Color](Color(99)))

	// String enum.
	fmt.Printf("EnumValues[Status]: %v\n", q.EnumValues[Status]())
	fmt.Printf("EnumNames[Status]: %v\n", q.EnumNames[Status]())
	fmt.Printf("EnumName[Status](Done): %q\n", q.EnumName[Status](Done))

	// Stringer.
	fmt.Printf("Color.String: %s, %s, %s\n", Red, Green, Blue)
	fmt.Printf("Status.String: %s, %s, %s, %s\n", Pending, Done, Failed, Status("unknown-fallback"))

	// JSON.
	b, _ := json.Marshal(Green)
	fmt.Printf("json.Marshal(Green): %s\n", b)
	var c Color
	_ = json.Unmarshal([]byte(`"Blue"`), &c)
	fmt.Printf("json.Unmarshal(\"Blue\"): %v (Blue=%v)\n", c, c == Blue)

	// Value-based parsing wrapper.
	if v, err := ParseStatusValue("done"); err != nil {
		fmt.Printf("ParseStatusValue(done): err=%s\n", err)
	} else {
		fmt.Printf("ParseStatusValue(done): %s\n", v)
	}
	if _, err := ParseStatusValue("nope"); err != nil {
		fmt.Printf("ParseStatusValue(nope): err=%s\n", err)
	}

	// Exhaustive switch.
	fmt.Printf("warmth(Red): %s\n", warmth(Red))
	fmt.Printf("warmth(Green): %s\n", warmth(Green))
	fmt.Printf("warmth(Blue): %s\n", warmth(Blue))
}
