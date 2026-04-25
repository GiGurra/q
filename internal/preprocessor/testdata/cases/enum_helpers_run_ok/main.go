// Fixture: q.EnumValues / q.EnumNames / q.EnumName / q.EnumParse /
// q.EnumValid / q.EnumOrdinal — all rewrite to literal slices or
// inline switches at compile time.
package main

import (
	"errors"
	"fmt"

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

func main() {
	colors := q.EnumValues[Color]()
	fmt.Printf("colors: %v\n", colors)
	fmt.Printf("colors.len: %d\n", len(colors))

	colorNames := q.EnumNames[Color]()
	fmt.Printf("colorNames: %v\n", colorNames)

	// Print int values to see the underlying ordering, bypassing
	// String().
	colorInts := []int{}
	for _, c := range colors {
		colorInts = append(colorInts, int(c))
	}
	fmt.Printf("colorInts: %v\n", colorInts)

	statuses := q.EnumValues[Status]()
	statusVals := []string{}
	for _, s := range statuses {
		statusVals = append(statusVals, string(s))
	}
	fmt.Printf("statusVals: %v\n", statusVals)

	statusNames := q.EnumNames[Status]()
	fmt.Printf("statusNames: %v\n", statusNames)

	// EnumName
	fmt.Printf("name(Red): %q\n", q.EnumName[Color](Red))
	fmt.Printf("name(Green): %q\n", q.EnumName[Color](Green))
	fmt.Printf("name(Blue): %q\n", q.EnumName[Color](Blue))
	fmt.Printf("name(unknown): %q\n", q.EnumName[Color](Color(99)))
	fmt.Printf("status.name(Done): %q\n", q.EnumName[Status](Done))

	// EnumParse — happy path (NAME-based, mirrors EnumName)
	c, errOK := q.EnumParse[Color]("Green")
	fmt.Printf("parse(Green): %d %v %v\n", int(c), c == Green, errOK)
	// Unknown name → bubbles ErrEnumUnknown
	_, errBad := q.EnumParse[Color]("Yellow")
	fmt.Printf("parse(Yellow).err: %v\n", errBad)
	fmt.Printf("parse(Yellow).is: %v\n", errors.Is(errBad, q.ErrEnumUnknown))

	// EnumParse over string-typed enum (also NAME-based: pass "Done"
	// the constant identifier — the underlying value is "done")
	s, errStatus := q.EnumParse[Status]("Done")
	fmt.Printf("status.parse(Done): %s %v %v\n", string(s), s == Done, errStatus)

	// EnumValid
	fmt.Printf("valid(Red): %v\n", q.EnumValid[Color](Red))
	fmt.Printf("valid(99): %v\n", q.EnumValid[Color](Color(99)))
	fmt.Printf("status.valid(Done): %v\n", q.EnumValid[Status](Done))
	fmt.Printf("status.valid(unknown): %v\n", q.EnumValid[Status](Status("nope")))

	// EnumOrdinal
	fmt.Printf("ord(Red): %d\n", q.EnumOrdinal[Color](Red))
	fmt.Printf("ord(Green): %d\n", q.EnumOrdinal[Color](Green))
	fmt.Printf("ord(Blue): %d\n", q.EnumOrdinal[Color](Blue))
	fmt.Printf("ord(99): %d\n", q.EnumOrdinal[Color](Color(99)))

	// In an arbitrary expression position (each q.Enum* call site
	// is independently rewritten in place)
	descs := []string{}
	for _, c := range colors {
		descs = append(descs, fmt.Sprintf("%s=%d", q.EnumName[Color](c), q.EnumOrdinal[Color](c)))
	}
	fmt.Printf("descs: %v\n", descs)

	// User-defined String() method built on top of EnumName.
	fmt.Printf("color.String(): %s\n", Red.String())
	fmt.Printf("status.String(): %s\n", Failed.String())

	// Composition: q.Try wraps q.EnumParse. The (T, error) shape of
	// EnumParse drops straight into Try via Go's f(g()) rule.
	parseTry, errInner := tryParse("Blue")
	fmt.Printf("tryParse(Blue): %d %v %v\n", int(parseTry), parseTry == Blue, errInner)
	_, errInnerBad := tryParse("Pink")
	fmt.Printf("tryParse(Pink).err: %v\n", errInnerBad)
	fmt.Printf("tryParse(Pink).is: %v\n", errors.Is(errInnerBad, q.ErrEnumUnknown))

	// Composition: q.TryE chain wraps q.EnumParse — Wrapf prepends
	// context to the bubbled error.
	_, errWrapped := tryParseWrapped("Pink")
	fmt.Printf("tryParseWrapped.err: %v\n", errWrapped)
	fmt.Printf("tryParseWrapped.is: %v\n", errors.Is(errWrapped, q.ErrEnumUnknown))

	// Composition: in-place q.EnumName wraps a bubble q.Try.
	wrapped, errWrap := nameOfTried("Green")
	fmt.Printf("nameOfTried(Green): %q %v\n", wrapped, errWrap)
	_, errBubble := nameOfTried("Pink")
	fmt.Printf("nameOfTried(Pink).err: %v\n", errBubble)

	// Composition: in-place q.EnumName inside a non-q function call.
	fmt.Printf("nested-in-Sprintf: %s\n", fmt.Sprintf("name=%s", q.EnumName[Color](Green)))
}

// nameOfTried demonstrates an in-place family (q.EnumName) wrapping
// a bubble family (q.Try). q.Try fires its check before q.EnumName's
// IIFE evaluates the substituted temp.
func nameOfTried(s string) (string, error) {
	return q.EnumName[Color](q.Try(q.EnumParse[Color](s))), nil
}

func tryParse(s string) (Color, error) {
	c := q.Try(q.EnumParse[Color](s))
	return c, nil
}

func tryParseWrapped(s string) (Color, error) {
	c := q.TryE(q.EnumParse[Color](s)).Wrapf("loading %q", s)
	return c, nil
}

// String demonstrates how a user pairs q.EnumName with their own
// method declaration to get a "real" Stringer without the generator.
func (c Color) String() string { return q.EnumName[Color](c) }

func (s Status) String() string {
	name := q.EnumName[Status](s)
	if name == "" {
		return string(s)
	}
	return name
}
