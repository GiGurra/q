// example/reflection mirrors docs/api/reflection.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/reflection
package main

import (
	"errors"
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- "At a glance" ----------
type User struct {
	ID    int    `json:"id"   db:"user_id"`
	Name  string `json:"name,omitempty" db:"full_name"`
	Email string `json:"email"`
	pwd   string // unexported — present so q.AllFields includes it; never read
}

var _ = User{}.pwd //nolint:unused // pwd is referenced via q.AllFields, not directly

// ---------- "DB-tag → value mapping" ----------
//
// Doc snippet uses a User type with db tags on every field; here we
// reuse the at-a-glance User (which has no db tag on Email) so the
// Email entry's key folds to "" — same behaviour the at-a-glance
// section demonstrates with q.Tag[User]("Email", "db").
func rowOf(u User) map[string]any {
	return map[string]any{
		q.Tag[User]("ID", "db"):    u.ID,
		q.Tag[User]("Name", "db"):  u.Name,
		q.Tag[User]("Email", "db"): u.Email,
	}
}

// ---------- "Type-aware error messages" ----------
//
// q.F / q.Ferr placeholders are not re-scanned for nested q.* calls,
// so hoist the q.TypeName call into a variable first.
func decodeErrFor(err error) string {
	if err == nil {
		return ""
	}
	typeName := q.TypeName[User]()
	_ = typeName // referenced by q.Ferr placeholder below; compiler can't see the use
	out := q.Ferr("decoding {typeName}: {err}")
	return out.Error()
}

// ---------- "Auto-generated marshaller" — uses q.Fields ----------
func encode(v User) map[string]any {
	out := map[string]any{}
	for _, name := range q.Fields[User]() {
		switch name {
		case "ID":
			out[name] = v.ID
		case "Name":
			out[name] = v.Name
		case "Email":
			out[name] = v.Email
		}
	}
	return out
}

func main() {
	// q.Fields — exported only.
	fmt.Printf("Fields[User]: %v\n", q.Fields[User]())
	// q.AllFields — every field.
	fmt.Printf("AllFields[User]: %v\n", q.AllFields[User]())
	// q.TypeName — pointer indirection followed.
	fmt.Printf("TypeName[User]: %s\n", q.TypeName[User]())
	fmt.Printf("TypeName[*User]: %s\n", q.TypeName[*User]())
	// q.Tag — by field & key.
	fmt.Printf("Tag[User](ID,json): %q\n", q.Tag[User]("ID", "json"))
	fmt.Printf("Tag[User](ID,db): %q\n", q.Tag[User]("ID", "db"))
	fmt.Printf("Tag[User](Name,db): %q\n", q.Tag[User]("Name", "db"))
	fmt.Printf("Tag[User](Email,db): %q\n", q.Tag[User]("Email", "db"))

	// DB-tag → value mapping.
	row := rowOf(User{ID: 7, Name: "Ada", Email: "ada@example.com"})
	fmt.Printf("rowOf.user_id=%v full_name=%v missing-key(\"\")=%v\n", row["user_id"], row["full_name"], row[""])

	// Type-aware error messages.
	fmt.Printf("decodeErrFor: %s\n", decodeErrFor(errors.New("EOF")))

	// Auto-marshaller.
	u := User{ID: 1, Name: "Ada", Email: "ada@example.com"}
	enc := encode(u)
	fmt.Printf("encode.ID=%v Name=%v Email=%v len=%d\n", enc["ID"], enc["Name"], enc["Email"], len(enc))
}
