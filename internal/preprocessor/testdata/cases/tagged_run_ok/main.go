// Fixture: q.Tagged + q.MkTag + q.UnTag + .Value() — phantom types.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// Brand types — zero-sized, exist only to make the Tagged
// instantiations distinct.
type _userID struct{}
type _orderID struct{}

// Aliases — typical user-facing surface for a brand.
type UserID = q.Tagged[int, _userID]
type OrderID = q.Tagged[int, _orderID]

func main() {
	// Construct via q.MkTag — T-first, U inferred from the value.
	uid := q.MkTag[_userID](42)
	oid := q.MkTag[_orderID](42)

	// Underlying values match...
	fmt.Println("uid value:", q.UnTag(uid))
	fmt.Println("oid value:", oid.Value())

	// ...but the brands are distinct types. The next line would not
	// compile if we tried `var u UserID = oid` — which is the whole
	// point — so we just demonstrate equality is also brand-aware.
	uidB := q.MkTag[_userID](42)
	fmt.Println("uid == uidB:", uid == uidB)

	// Same underlying, different brand → can't be compared with ==
	// (different Go types). We can compare unwrapped values:
	fmt.Println("uid underlying == oid underlying:",
		q.UnTag(uid) == q.UnTag(oid))

	// Round-trip arithmetic: there's no operator overload, so do it
	// underlying then re-wrap.
	next := q.MkTag[_userID](q.UnTag(uid) + 1)
	fmt.Println("next uid value:", next.Value())

	// Aliases interchange freely with the spelled-out type — UserID
	// is q.Tagged[int, _userID].
	asAlias := UserID(uid)
	fmt.Println("asAlias value:", asAlias.Value())

	// String-backed brand for variety.
	type _email struct{}
	email := q.MkTag[_email]("user@example.com")
	fmt.Println("email value:", q.UnTag(email))
}
