package q

// reflect.go — compile-time reflection. Each call site folds to a
// literal at the rewrite pass; no runtime `reflect` calls, no method
// values, no interface boxing. The cost is paid in bytes once at
// build time.
//
// What's covered:
//
//   - q.Fields[T]() lists T's exported field names (struct only).
//   - q.AllFields[T]() lists every field, exported or not.
//   - q.TypeName[T]() yields T's defined type name as a string.
//   - q.Tag[T](field, key) yields a struct tag value at compile time.
//
// All take a struct type T (or *T) at the type-arg position; pointer
// indirection is followed so q.Fields[*User]() and q.Fields[User]()
// produce the same result.
//
// For struct-tag-driven serialisation/codegen, q.Fields + q.Tag are
// often enough to write a small per-type marshaller without runtime
// reflection — drop into per-field switches keyed off the field name.

// Fields returns the names of T's EXPORTED fields in source
// declaration order. T must be a struct type (or *struct). Embedded
// fields are returned by their declared name (the type's
// unqualified identifier).
//
//	type User struct {
//	    ID   int    `json:"id"`
//	    Name string `json:"name"`
//	    pwd  string // unexported — excluded
//	}
//	q.Fields[User]() // []string{"ID", "Name"}
func Fields[T any]() []string {
	panicUnrewritten("q.Fields")
	return nil
}

// AllFields returns the names of every field on T, including
// unexported ones, in source declaration order.
func AllFields[T any]() []string {
	panicUnrewritten("q.AllFields")
	return nil
}

// TypeName returns T's defined type name as a string. For named
// types: just the identifier (e.g. `"User"`, `"Color"`). For pointer
// types: the dereferenced name (`*User` → `"User"`). For slice /
// map / chan / function / unnamed-struct types: a representation
// matching `go/types` formatting.
//
// The result is a constant the Go compiler folds into the binary —
// no runtime reflection is needed.
//
//	q.TypeName[User]()    // "User"
//	q.TypeName[*User]()   // "User"
//	q.TypeName[[]User]()  // "[]User"
func TypeName[T any]() string {
	panicUnrewritten("q.TypeName")
	return ""
}

// Tag returns the value of the struct-tag entry for `key` on T's
// `field`. T must be a struct (or pointer to struct). Both `field`
// and `key` MUST be Go string literals — the rewriter validates at
// compile time that the field exists. Returns "" when the tag is
// present but the key is absent (matches `reflect.StructTag.Get`).
//
//	type User struct {
//	    ID   int    `json:"id"   db:"user_id"`
//	    Name string `json:"name,omitempty"`
//	}
//	q.Tag[User]("ID", "json")    // "id"
//	q.Tag[User]("ID", "db")      // "user_id"
//	q.Tag[User]("Name", "json")  // "name,omitempty"
//	q.Tag[User]("Name", "db")    // "" — key absent
func Tag[T any](field, key string) string {
	panicUnrewritten("q.Tag")
	return ""
}
