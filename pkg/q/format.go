package q

// format.go — compile-time string interpolation. Each call site is
// rewritten into the equivalent fmt.Sprintf / fmt.Fprintln /
// errors.New form with the `{expr}` segments hoisted out of the
// literal as positional arguments. The format string MUST be a Go
// string literal — dynamic format strings would defeat compile-time
// extraction (and, for q.SQL, would re-open the injection hole the
// helper exists to close).
//
// Brace-escape: `{{` for a literal `{`, `}}` for a literal `}`. Go
// string literals and rune literals inside an `{expr}` are honoured —
// braces inside `"..."` / `'...'` / `` `...` `` don't terminate the
// placeholder. So `q.F("got {f(\"}\")}", v)` is well-formed.

// F builds a formatted string by interpolating `{expr}` segments at
// compile time. Each placeholder is replaced with the formatted value
// of the Go expression `expr`, evaluated in the caller's scope.
//
//	name := "world"
//	q.F("hello {name}, you are {age+1}")
//	// → fmt.Sprintf("hello %v, you are %v", name, age+1)
//
// The format must be a Go string literal — passing a runtime string
// surfaces a diagnostic. For runtime-built formats, use fmt.Sprintf
// directly.
func F(format string) string {
	panicUnrewritten("q.F")
	return ""
}

// Ferr is `errors.New(q.F(format))` shaped — useful when the
// interpolation result is the immediate error.
//
//	return q.Ferr("user {id} not found")
//	// → errors.New(fmt.Sprintf("user %v not found", id))
func Ferr(format string) error {
	panicUnrewritten("q.Ferr")
	return nil
}

// Fln writes the interpolated string + "\n" to q.DebugWriter
// (defaults to os.Stderr). Useful for ad-hoc diagnostics that don't
// warrant a full slog setup.
//
//	q.Fln("processing {len(items)} items for {user.Name}")
func Fln(format string) {
	panicUnrewritten("q.Fln")
}
