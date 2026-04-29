// example/at mirrors docs/api/at.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/at
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// Doc's running types.
type Settings struct{ Theme string }
type Profile struct {
	DisplayName string
	Settings    *Settings
}
type User struct {
	Profile  *Profile
	Defaults *Defaults
	ID       int
}

type Defaults struct{ Settings *Settings }

type Opts struct{ Endpoint string }
type Env struct{ Endpoint string }
type GlobalConfig struct{ DefaultEndpoint string }

type Cfg struct{ DB *CfgDB }
type CfgDB struct{ MaxConn int }

func loadDefault() int { return 7 }

var ErrThemeMissing = errors.New("theme missing")

func loadFromDisk(id int) (string, error) {
	if id == 0 {
		return "", errors.New("no user")
	}
	return fmt.Sprintf("disk-theme-%d", id), nil
}

func getUser() *User {
	return &User{Profile: &Profile{Settings: &Settings{Theme: "dark"}}}
}

// ---------- "What the rewriter does — Theme example" ----------
//
//	v := q.At(user.Profile.Settings.Theme).
//	    OrElse(user.Defaults.Settings.Theme).
//	    Or("light")
func themeWithFallback(user *User) string {
	v := q.At(user.Profile.Settings.Theme).
		OrElse(user.Defaults.Settings.Theme).
		Or("light")
	return v
}

// ---------- "Examples — Simple fallback" ----------
//
//	display := q.At(user.Profile.DisplayName).Or("anonymous")
func simpleFallback(user *User) string {
	display := q.At(user.Profile.DisplayName).Or("anonymous")
	return display
}

// ---------- "Examples — Multiple fallback paths" ----------
//
//	endpoint := q.At(opts.Endpoint).
//	    OrElse(env.Endpoint).
//	    OrElse(globalConfig.DefaultEndpoint).
//	    Or("https://example.com")
func multipleFallback(opts *Opts, env *Env, globalConfig *GlobalConfig) string {
	endpoint := q.At(opts.Endpoint).
		OrElse(env.Endpoint).
		OrElse(globalConfig.DefaultEndpoint).
		Or("https://example.com")
	return endpoint
}

// ---------- "Examples — Zero-value terminal" ----------
//
//	name := q.At(user.Profile.DisplayName).OrZero()
func zeroTerminal(user *User) string {
	name := q.At(user.Profile.DisplayName).OrZero()
	return name
}

// ---------- "Examples — .OrElse arg as plain expression" ----------
//
//	maxConn := q.At(cfg.DB.MaxConn).OrElse(loadDefault()).Or(10)
func orElsePlainExpr(cfg *Cfg) int {
	maxConn := q.At(cfg.DB.MaxConn).OrElse(loadDefault()).Or(10)
	return maxConn
}

// ---------- "Examples — Nested method call as the root" ----------
//
//	v := q.At(getUser().Profile.Settings.Theme).Or("light")
func nestedMethodRoot() string {
	v := q.At(getUser().Profile.Settings.Theme).Or("light")
	return v
}

// ---------- "Examples — Bubble shape (.OrError)" ----------
//
//	func loadTheme(u *User) (string, error) {
//	    return q.At(u.Profile.Settings.Theme).OrError(ErrThemeMissing), nil
//	}
func loadTheme(u *User) (string, error) {
	return q.At(u.Profile.Settings.Theme).OrError(ErrThemeMissing), nil
}

// ---------- "Examples — Bubble shape (.OrE)" ----------
//
//	func resolveTheme(u *User) (string, error) {
//	    return q.At(u.Profile.Settings.Theme).OrE(loadFromDisk(u.ID)), nil
//	}
func resolveTheme(u *User) (string, error) {
	return q.At(u.Profile.Settings.Theme).OrE(loadFromDisk(u.ID)), nil
}

func main() {
	// All-paths populated.
	full := &User{
		Profile:  &Profile{DisplayName: "Ada", Settings: &Settings{Theme: "dark"}},
		Defaults: &Defaults{Settings: &Settings{Theme: "ocean"}},
		ID:       1,
	}
	// Profile is nil — primary path breaks.
	noProfile := &User{
		Defaults: &Defaults{Settings: &Settings{Theme: "ocean-default"}},
		ID:       2,
	}
	// All nil — fallback path breaks too.
	allNil := &User{ID: 3}
	allNilNoID := &User{}

	fmt.Printf("themeWithFallback(full): %s\n", themeWithFallback(full))
	fmt.Printf("themeWithFallback(noProfile): %s\n", themeWithFallback(noProfile))
	fmt.Printf("themeWithFallback(allNil): %s\n", themeWithFallback(allNil))

	fmt.Printf("simpleFallback(full): %s\n", simpleFallback(full))
	fmt.Printf("simpleFallback(allNil): %s\n", simpleFallback(allNil))

	// q.At's nil guards fire on nilable HOPS — passing a nil *Opts
	// breaks the first path; the leaf string then succeeds via the
	// second / third path.
	fmt.Printf("multipleFallback(nil,nil,populated): %s\n",
		multipleFallback(nil, nil, &GlobalConfig{DefaultEndpoint: "global-default"}))
	fmt.Printf("multipleFallback(set,_,_): %s\n",
		multipleFallback(&Opts{Endpoint: "opts-set"}, nil, nil))
	fmt.Printf("multipleFallback(nil,nil,nil): %s\n",
		multipleFallback(nil, nil, nil))

	fmt.Printf("zeroTerminal(full): %q\n", zeroTerminal(full))
	fmt.Printf("zeroTerminal(allNil): %q\n", zeroTerminal(allNil))

	fmt.Printf("orElsePlainExpr(set): %d\n", orElsePlainExpr(&Cfg{DB: &CfgDB{MaxConn: 25}}))
	fmt.Printf("orElsePlainExpr(nil-DB): %d\n", orElsePlainExpr(&Cfg{}))

	fmt.Printf("nestedMethodRoot: %s\n", nestedMethodRoot())

	if v, err := loadTheme(full); err != nil {
		fmt.Printf("loadTheme(full): err=%s\n", err)
	} else {
		fmt.Printf("loadTheme(full): %s\n", v)
	}
	if _, err := loadTheme(allNil); err != nil {
		fmt.Printf("loadTheme(allNil): err=%s\n", err)
	}

	if v, err := resolveTheme(full); err != nil {
		fmt.Printf("resolveTheme(full): err=%s\n", err)
	} else {
		fmt.Printf("resolveTheme(full): %s\n", v)
	}
	if v, err := resolveTheme(allNil); err != nil {
		fmt.Printf("resolveTheme(allNil): err=%s\n", err)
	} else {
		fmt.Printf("resolveTheme(allNil): %s\n", v)
	}
	if _, err := resolveTheme(allNilNoID); err != nil {
		fmt.Printf("resolveTheme(allNilNoID): err=%s\n", err)
	}
}
