// Package basic demonstrates the q preprocessor: a flat call site that
// the toolexec pass rewrites into the conventional `if err != nil {
// return …, err }` shape. Every q.* helper used here is ordinary Go
// from the IDE's perspective; the runtime body is link-gated, so
// forgetting `-toolexec=q` produces a loud link failure rather than a
// silent runtime panic.
package basic

import (
	"errors"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

// ErrNotFound is the canonical not-found error for the lookup example.
var ErrNotFound = errors.New("basic: not found")

// ParseAndDouble shows the smallest end-to-end use of q.Try. Without q
// the body would be the standard two-step:
//
//	n, err := strconv.Atoi(s)
//	if err != nil { return 0, err }
//	return n * 2, nil
func ParseAndDouble(s string) (int, error) {
	n := q.Try(strconv.Atoi(s))
	return n * 2, nil
}

// ParseWithContext shows fmt.Errorf-style wrapping at the call site
// via q.TryE(...).Wrapf(...) — the equivalent of `if err != nil {
// return 0, fmt.Errorf("parsing %q: %w", s, err) }`.
func ParseWithContext(s string) (int, error) {
	n := q.TryE(strconv.Atoi(s)).Wrapf("parsing %q", s)
	return n, nil
}

// LookupOrError shows nil-pointer bubbling via q.NotNilE(...).Err(err).
func LookupOrError(table map[string]*int, key string) (int, error) {
	p := q.NotNilE(table[key]).Err(ErrNotFound)
	return *p, nil
}

// LookupSentinel shows the bare q.NotNil form — bubbles q.ErrNil when
// the lookup misses.
func LookupSentinel(table map[string]*int, key string) (int, error) {
	p := q.NotNil(table[key])
	return *p, nil
}
