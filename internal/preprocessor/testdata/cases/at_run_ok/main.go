// Fixture: q.At(<chain>).OrElse(<alt>)*.{Or(<fallback>) | OrZero()}
// — nested-nil safe traversal with a chain of fallbacks.
//
// Demonstrates:
//
//   1. Happy path (no nils) returns the leaf.
//   2. Nil intermediate falls through to .Or fallback.
//   3. Multiple .OrElse paths try in source order; first non-nil wins.
//   4. .OrZero returns the zero value of T when every path is nil.
//   5. Lazy fallback — fallback expression evaluates only when reached.
//   6. Single-eval — root expression with side effects runs once per path.
//   7. Non-nilable leaf (string, int) — chain returns leaf when reached.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Settings struct {
	Theme   string
	MaxConn int
}

type Profile struct {
	DisplayName string
	Settings    *Settings
}

type User struct {
	ID       int
	Profile  *Profile
	Defaults *Profile
}

var fallbackCalls int

func defaultTheme() string { fallbackCalls++; return "dark" }

func main() {
	full := &User{
		ID: 1,
		Profile: &Profile{
			DisplayName: "alice",
			Settings:    &Settings{Theme: "light", MaxConn: 42},
		},
	}

	// (1) Happy path.
	theme := q.At(full.Profile.Settings.Theme).Or("default")
	fmt.Println("happy theme:", theme)
	fmt.Println("fallbackCalls (happy):", fallbackCalls)

	// (2) Nil intermediate.
	noProfile := &User{ID: 2}
	theme2 := q.At(noProfile.Profile.Settings.Theme).Or(defaultTheme())
	fmt.Println("nil-profile theme:", theme2)
	fmt.Println("fallbackCalls (after nil):", fallbackCalls)

	// (3) .OrElse fallback path — Defaults present.
	withDefaults := &User{
		ID:       3,
		Defaults: &Profile{Settings: &Settings{Theme: "via-defaults", MaxConn: 7}},
	}
	theme3 := q.At(withDefaults.Profile.Settings.Theme).
		OrElse(withDefaults.Defaults.Settings.Theme).
		Or("hardcoded")
	fmt.Println("orelse theme:", theme3)

	// (4) Two .OrElse paths, both nil — terminal kicks in.
	noProfileNoDefaults := &User{ID: 4}
	theme4 := q.At(noProfileNoDefaults.Profile.Settings.Theme).
		OrElse(noProfileNoDefaults.Defaults.Settings.Theme).
		Or("final")
	fmt.Println("all-nil theme:", theme4)

	// (5) .OrZero — zero value of T.
	maxConn := q.At(noProfile.Profile.Settings.MaxConn).OrZero()
	fmt.Println("zero maxConn:", maxConn)

	// (6) .OrZero on a string — zero value is "".
	themeZero := q.At(noProfile.Profile.Settings.Theme).OrZero()
	fmt.Printf("zero theme: %q\n", themeZero)

	// (7) Single-eval — root call runs once even with multiple selectors.
	calls := 0
	getUser := func() *User {
		calls++
		return noProfile
	}
	_ = q.At(getUser().Profile.Settings.Theme).Or("single-eval")
	fmt.Println("single-eval calls:", calls)

	// (8) Multiple .OrElse with one mid-chain alt succeeding.
	a := &User{Defaults: &Profile{Settings: &Settings{Theme: "mid"}}}
	chosen := q.At(a.Profile.Settings.Theme).
		OrElse(a.Defaults.Settings.Theme).
		Or("never")
	fmt.Println("mid-chain hit:", chosen)

	// (9) Lazy alt — second OrElse arg has a side effect; should not fire
	//     when the first OrElse hits.
	altCalls := 0
	getAlt := func() string { altCalls++; return "lazy-alt" }
	hit := q.At(a.Profile.Settings.Theme).
		OrElse(a.Defaults.Settings.Theme).
		OrElse(getAlt()).
		Or("never")
	fmt.Println("lazy-alt hit:", hit)
	fmt.Println("altCalls (should be 0):", altCalls)
}
