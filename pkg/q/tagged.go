package q

// tagged.go — phantom types ("brands") that share an underlying type
// but stay distinct at compile time. Pure runtime — no preprocessor
// involvement; the type distinction comes from Go's structural rules
// applied to a generic struct with an unexported field.

// Tagged is a phantom-typed wrapper carrying a value of type U with
// a brand type T. Two Tagged instantiations with different T are
// distinct Go types Go won't implicitly convert between, even when U
// is the same:
//
//	type _userID  struct{}
//	type _orderID struct{}
//	type UserID  = q.Tagged[int, _userID]
//	type OrderID = q.Tagged[int, _orderID]
//
//	var u UserID  = q.MkTag[_userID](42)
//	var o OrderID = u // compile error: cannot use UserID as OrderID
//
// The wrapped value is held in an unexported field, so the only way
// to construct a Tagged is via q.MkTag — which is what makes the
// brand load-bearing. Without that, users could write
// `Tagged[U, T]{...}` directly and bypass the discipline.
//
// Equality: Tagged[U, T] is comparable iff U is comparable. The
// generated == compares the underlying U (since the brand T appears
// only as a phantom type parameter, not as a runtime field).
//
// Operator support: Go has no operator overloading, so arithmetic
// like `userID + 1` does not compile against UserID. Round-trip
// through UnTag / MkTag when the underlying op is needed:
//
//	next := q.MkTag[_userID](q.UnTag(uid) + 1)
//
// JSON / serialization: Tagged has no MarshalJSON method, so the
// default encoding/json output is `{}` (the unexported field is
// invisible). Add MarshalJSON / UnmarshalJSON methods on the brand
// alias when wire-format support is needed — encoding/json picks the
// alias's methods in preference to Tagged's.
type Tagged[U any, T any] struct {
	v U
}

// MkTag wraps v as a Tagged[U, T]. The brand T must be supplied
// explicitly as a type argument; U is inferred from v's type.
//
// Example:
//
//	type _userID struct{}
//	uid := q.MkTag[_userID](42) // q.Tagged[int, _userID]
//
// Plain runtime function — not rewritten by the preprocessor.
func MkTag[T any, U any](v U) Tagged[U, T] {
	return Tagged[U, T]{v: v}
}

// UnTag returns the underlying value carried by a Tagged. Both type
// parameters are inferred from the argument:
//
//	n := q.UnTag(uid) // int
//
// Plain runtime function — not rewritten by the preprocessor.
// Reach for .Value() for the method-call form.
func UnTag[U any, T any](v Tagged[U, T]) U {
	return v.v
}

// Value is the method form of UnTag — `uid.Value()` reads naturally
// at the call site without a separate top-level helper. Identical
// semantics to UnTag.
func (t Tagged[U, T]) Value() U {
	return t.v
}
