// example/trace mirrors docs/api/trace.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/trace
package main

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/GiGurra/q/pkg/q"
)

type Row struct{ ID int }

var ErrNotFound = errors.New("not found")

func dbQuery(id int) (Row, error) {
	if id == 0 {
		return Row{}, ErrNotFound
	}
	return Row{ID: id}, nil
}

// Recovery hook for q.TraceE.Catch.
func backfillRow(_ error) (Row, error) { return Row{ID: -1}, nil }

// ---------- "What q.Trace does" ----------
func loadRow(id int) (Row, error) {
	row := q.Trace(dbQuery(id))
	return row, nil
}

// ---------- "Chain methods on q.TraceE" ----------
func loadRowErr(id int) (Row, error) {
	row := q.TraceE(dbQuery(id)).Err(errors.New("custom err"))
	return row, nil
}

func loadRowErrF(id int) (Row, error) {
	row := q.TraceE(dbQuery(id)).ErrF(func(e error) error { return fmt.Errorf("wrapped: %w", e) })
	return row, nil
}

func loadRowWrap(id int) (Row, error) {
	row := q.TraceE(dbQuery(id)).Wrap("loading user")
	return row, nil
}

func loadRowWrapf(id int) (Row, error) {
	row := q.TraceE(dbQuery(id)).Wrapf("loading user %d", id)
	return row, nil
}

func loadRowCatch(id int) (Row, error) {
	row := q.TraceE(dbQuery(id)).Catch(backfillRow)
	return row, nil
}

// stripFileLine pins line numbers so test output is stable across edits.
var fileLineRE = regexp.MustCompile(`main\.go:\d+`)

func stripPath(s string) string { return fileLineRE.ReplaceAllString(s, "main.go:N") }

func main() {
	if r, err := loadRow(7); err != nil {
		fmt.Printf("loadRow(7): err=%s\n", stripPath(err.Error()))
	} else {
		fmt.Printf("loadRow(7): row.ID=%d\n", r.ID)
	}

	_, err := loadRow(0)
	fmt.Printf("loadRow(0): err=%s\n", stripPath(err.Error()))
	fmt.Printf("loadRow(0).is(ErrNotFound): %v\n", errors.Is(err, ErrNotFound))

	_, err = loadRowErr(0)
	fmt.Printf("loadRowErr(0): err=%s\n", stripPath(err.Error()))

	_, err = loadRowErrF(0)
	fmt.Printf("loadRowErrF(0): err=%s\n", stripPath(err.Error()))

	_, err = loadRowWrap(0)
	fmt.Printf("loadRowWrap(0): err=%s\n", stripPath(err.Error()))

	_, err = loadRowWrapf(0)
	fmt.Printf("loadRowWrapf(0): err=%s\n", stripPath(err.Error()))

	r, err := loadRowCatch(0)
	if err != nil {
		fmt.Printf("loadRowCatch(0): err=%s\n", stripPath(err.Error()))
	} else {
		fmt.Printf("loadRowCatch(0): recovered row.ID=%d\n", r.ID)
	}
}
