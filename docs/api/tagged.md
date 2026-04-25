# Phantom types: `q.Tagged` / `q.MkTag` / `q.UnTag`

Go has no nominal-typing primitive for "two values that share an underlying type but should not be interchangeable" — `type UserID int` followed by `type OrderID int` *does* produce two distinct types, but with `int` constants like `42` implicitly assignable to either, and arithmetic operators that don't help disambiguate at the call site. Phantom types — a generic struct branded by a unique tag type — close the gap.

```go
type _userID struct{}
type _orderID struct{}
type UserID  = q.Tagged[int, _userID]
type OrderID = q.Tagged[int, _orderID]

uid := q.MkTag[_userID](42)
oid := q.MkTag[_orderID](42)

// uid and oid are distinct Go types — not interchangeable, even
// though both wrap an int. Tries to assign one to the other are
// compile errors.
var u UserID = oid // compile error: cannot use OrderID as UserID
```

Pure runtime helpers — no preprocessor work involved. `q.Tagged` is a generic struct with an unexported field; the only constructor is `q.MkTag`. That's what makes the brand load-bearing — without the unexported field, users could write `Tagged[U, T]{...}` directly and bypass the discipline.

## Surface

```go
type Tagged[U any, T any] struct { /* unexported v U */ }

func MkTag[T any, U any](v U) Tagged[U, T]   // brand explicit, value inferred
func UnTag[U any, T any](v Tagged[U, T]) U   // both inferred
func (t Tagged[U, T]) Value() U              // method form of UnTag
```

`MkTag`'s type parameters are ordered T-first so users only have to spell the brand at the call site:

```go
uid := q.MkTag[_userID](42)        // U=int inferred from 42
// vs the symmetric ordering, which would force both:
//   q.MkTag[int, _userID](42)
```

`UnTag` and `.Value()` are interchangeable. `q.UnTag(uid)` reads as a top-level operation; `uid.Value()` reads as a property of the wrapper. Pick whichever fits your call site.

## Brand types

A brand is just a type. The convention is a private zero-sized struct named after the role:

```go
type _userID struct{}
type UserID = q.Tagged[int, _userID]
```

The brand never has values — its only role is to differentiate the `Tagged` instantiation. Defining it as private (`_userID`) hides the type from package consumers, so they can only construct `UserID` via the constructor your package exposes.

A common pattern: package-private brand + public constructor:

```go
package users

type _userIDBrand struct{}
type ID = q.Tagged[int, _userIDBrand]

func NewID(n int) ID         { return q.MkTag[_userIDBrand](n) }
func IDValue(id ID) int      { return q.UnTag(id) }
```

Now downstream code can manipulate `users.ID` only through the package's `NewID` / `IDValue` (or the `.Value()` method, which is always available).

## Operators don't carry through

Go has no operator overloading. `userID + 1` is not valid against a `UserID`; you have to round-trip through the underlying:

```go
next := q.MkTag[_userID](q.UnTag(uid) + 1)
```

The TODO entry weighed adding rewriter sugar that would auto-wrap `q.MkTag[T](q.UnTag(x) + 1)` for arithmetic on Tagged values. The conclusion: not worth the rewriter complexity. The verbosity is the price of zero-cost branding.

## Equality

`Tagged[U, T]` is comparable iff `U` is comparable. The generated `==` compares the underlying value (the brand `T` appears only as a phantom type parameter, not a runtime field). Same-brand values with the same underlying compare equal:

```go
a := q.MkTag[_userID](42)
b := q.MkTag[_userID](42)
fmt.Println(a == b) // true
```

Different brands can't be compared with `==` — they're different types. Compare the unwrapped values when the goal is "are these the same number, regardless of branding":

```go
q.UnTag(uid) == q.UnTag(oid) // OK
```

## JSON / serialization

`Tagged` does not implement `json.Marshaler` itself — its only field is unexported, so the default encoding/json output would be an empty object `{}`. This is intentional: brand-typed values often want custom wire formats (e.g., string-encoded user IDs) and deferring the serialization decision to user code is cleaner than baking in a default.

The recommended pattern: declare `MarshalJSON` / `UnmarshalJSON` on your brand alias rather than on `Tagged` itself. Go's method-set rules pick up the alias's methods correctly:

```go
func (id ID) MarshalJSON() ([]byte, error) {
    return json.Marshal(q.UnTag(id))
}
func (id *ID) UnmarshalJSON(data []byte) error {
    var n int
    if err := json.Unmarshal(data, &n); err != nil {
        return err
    }
    *id = q.MkTag[_userIDBrand](n)
    return nil
}
```

## See also

- [`q.NotNil`](notnil.md) — runtime guard for nil pointers; orthogonal but often paired with branded types whose underlying is `*T`.
- [`q.As`](as.md) — typed forwarding for type assertions; also no runtime cost beyond the assertion.
