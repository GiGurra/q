// example/todo mirrors docs/api/todo.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/todo
package main

import (
	"fmt"
	"regexp"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ Schema string }

// ---------- "What q.TODO does" ----------
func parseV2(_ string) (Config, error) {
	q.TODO("schema v2 parser")
	return Config{}, nil // unreachable; q.TODO panics
}

func parseV3(_ string) (Config, error) {
	q.TODO()
	return Config{}, nil
}

// ---------- "q.Unreachable — default branch of an exhaustive switch" ----------
func handle(tag string) string {
	switch tag {
	case "a", "b", "c":
		return "handled-" + tag
	default:
		q.Unreachable(fmt.Sprintf("tag was %q", tag))
	}
	return ""
}

// stripFileLine erases line numbers so output is stable across edits.
var fileLineRE = regexp.MustCompile(`main\.go:\d+`)

func stripPath(s string) string { return fileLineRE.ReplaceAllString(s, "main.go:N") }

func runRecover(label string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("%s: panic=%s\n", label, stripPath(fmt.Sprint(r)))
		}
	}()
	fn()
}

func main() {
	runRecover("parseV2(in)", func() { _, _ = parseV2("input") })
	runRecover("parseV3(in)", func() { _, _ = parseV3("input") })
	fmt.Printf("handle(a)=%s\n", handle("a"))
	runRecover("handle(z)", func() { _ = handle("z") })
}
