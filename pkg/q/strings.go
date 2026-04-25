package q

// strings.go — compile-time string-case transforms. Each call site
// rewrites to a Go string literal at compile time. Useful for the
// codegen-adjacent stuff Go forces you to type out: column names,
// env var keys, URL slugs, JSON field names.
//
// All take a single string-literal argument. Dynamic strings are
// rejected at scan time — there's no point doing the work at
// compile time on a runtime string.

// Upper returns format with all letters mapped to upper case (ASCII).
//
//	q.Upper("hello")  // "HELLO"
func Upper(s string) string {
	panicUnrewritten("q.Upper")
	return ""
}

// Lower returns format with all letters mapped to lower case (ASCII).
//
//	q.Lower("HELLO")  // "hello"
func Lower(s string) string {
	panicUnrewritten("q.Lower")
	return ""
}

// Snake converts s to snake_case. CamelCase / PascalCase / kebab-case /
// space-separated inputs all produce lower_underscore output.
//
//	q.Snake("HelloWorld")    // "hello_world"
//	q.Snake("XMLHttpRequest") // "xml_http_request"
//	q.Snake("hello-world")   // "hello_world"
func Snake(s string) string {
	panicUnrewritten("q.Snake")
	return ""
}

// Kebab converts s to kebab-case. Separators are dashes.
//
//	q.Kebab("HelloWorld")     // "hello-world"
//	q.Kebab("XMLHttpRequest") // "xml-http-request"
//	q.Kebab("hello_world")    // "hello-world"
func Kebab(s string) string {
	panicUnrewritten("q.Kebab")
	return ""
}

// Camel converts s to camelCase: first word lower, subsequent
// words upper-initial. Separators (_, -, space) are removed.
//
//	q.Camel("hello_world")   // "helloWorld"
//	q.Camel("hello-world")   // "helloWorld"
//	q.Camel("hello world")   // "helloWorld"
//	q.Camel("HelloWorld")    // "helloWorld"
func Camel(s string) string {
	panicUnrewritten("q.Camel")
	return ""
}

// Pascal converts s to PascalCase: every word upper-initial,
// separators removed.
//
//	q.Pascal("hello_world")  // "HelloWorld"
//	q.Pascal("hello-world")  // "HelloWorld"
//	q.Pascal("helloWorld")   // "HelloWorld"
func Pascal(s string) string {
	panicUnrewritten("q.Pascal")
	return ""
}

// Title returns s with the first letter of each space-separated
// word upper-cased.
//
//	q.Title("hello world")  // "Hello World"
func Title(s string) string {
	panicUnrewritten("q.Title")
	return ""
}
