// Fixture: q.NewScope().BoundTo(ctx) plus q.Assemble(...).WithScope(scope).
// When ctx is cancelled, the scope closes and all assembly cleanups
// fire in reverse order. Mid-flight close (rare race) returns
// ErrScopeClosed; this fixture verifies the simpler "cancel happens
// after assembly success" path.
package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

type DB struct {
	id  int
	out *[]string
}

func (d *DB) Close() { *d.out = append(*d.out, fmt.Sprintf("db.Close#%d", d.id)) }

var nextID int

func newDB(out *[]string) *DB {
	nextID++
	*out = append(*out, fmt.Sprintf("build:db#%d", nextID))
	return &DB{id: nextID, out: out}
}

func main() {
	var trace []string

	ctx, cancel := context.WithCancel(context.Background())
	scope := q.NewScope().BoundTo(ctx)

	db, err := q.Assemble[*DB](&trace, newDB).WithScope(scope)
	if err != nil {
		fmt.Println("assemble:", err)
		return
	}
	fmt.Println("db-id:", db.id)
	fmt.Println("trace-pre:", trace)

	cancel()
	for range 200 {
		if scope.Closed() {
			break
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	fmt.Println("trace-post:", trace)

	// Post-cancel: a fresh assembly attempt errors with ErrScopeClosed.
	_, err = q.Assemble[*DB](&trace, newDB).WithScope(scope)
	fmt.Println("post-cancel-err:", errors.Is(err, q.ErrScopeClosed))
}
