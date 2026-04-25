package q

import (
	"maps"
	"slices"
)

// data.go — Scala / samber/lo-style functional data ops over slices.
//
// Pure runtime helpers — none of these are rewritten by the
// preprocessor. Each call evaluates immediately like any other Go
// function. They are added to qRuntimeHelpers in scanner.go so the
// "unsupported q.* shape" diagnostic doesn't trip on standalone use.
//
// Two flavours per fallible op:
//
//   - Bare (Map, Filter, GroupBy, ...) — fn cannot fail.
//
//   - …Err (MapErr, FilterErr, ...) — fn can fail; the helper
//     returns (result, error) and short-circuits on the first error.
//     Designed to compose with q.Try / q.TryE:
//
//         items := q.Try(q.MapErr(rows, parseRow))
//         items := q.TryE(q.MapErr(rows, parseRow)).Wrap("loading users")
//
// No `…E` flavour is shipped: the user can always reach for q.TryE
// over the …Err variant to get the chain vocabulary.
//
// First wave is slice-input / slice-output only. Iterator (iter.Seq)
// variants are deferred until usage patterns settle.

// ForEach iterates slice and calls fn for each element, in input
// order. Side-effect-only — no result collected. Almost identical to
// a plain `for _, v := range slice { fn(v) }`; the helper exists so
// "swap to parallel" is a one-line change to q.ParForEach without
// restructuring the loop:
//
//	q.ForEach(items, func(it Item) { log(it) })
//	q.ParForEach(ctx, items, func(it Item) { log(it) }) // parallel
func ForEach[T any](slice []T, fn func(T)) {
	for _, v := range slice {
		fn(v)
	}
}

// ForEachErr iterates slice and calls fn for each element, in input
// order. First error short-circuits and is returned; subsequent
// elements are not visited. Compose with q.Check / q.CheckE for the
// bubble path:
//
//	q.Check(q.ForEachErr(rows, validateRow))
func ForEachErr[T any](slice []T, fn func(T) error) error {
	for _, v := range slice {
		if err := fn(v); err != nil {
			return err
		}
	}
	return nil
}

// Map applies fn to each element of slice and returns the collected
// results in input order. Output length always equals input length.
//
//	doubled := q.Map(nums, func(n int) int { return n * 2 })
func Map[T, R any](slice []T, fn func(T) R) []R {
	out := make([]R, len(slice))
	for i, v := range slice {
		out[i] = fn(v)
	}
	return out
}

// MapErr is Map with a fallible fn. Returns (results, nil) on full
// success or (nil, err) on the first failure — remaining elements are
// not visited. Compose with q.Try / q.TryE for the bubble path:
//
//	users := q.Try(q.MapErr(rows, parseUser))
func MapErr[T, R any](slice []T, fn func(T) (R, error)) ([]R, error) {
	out := make([]R, len(slice))
	for i, v := range slice {
		r, err := fn(v)
		if err != nil {
			return nil, err
		}
		out[i] = r
	}
	return out, nil
}

// FlatMap applies fn to each element and concatenates the per-element
// slices into a single output slice (in input order).
//
//	pairs := q.FlatMap(items, func(it Item) []Pair { return it.Pairs })
func FlatMap[T, R any](slice []T, fn func(T) []R) []R {
	var out []R
	for _, v := range slice {
		out = append(out, fn(v)...)
	}
	return out
}

// FlatMapErr is FlatMap with a fallible fn. First error short-circuits.
func FlatMapErr[T, R any](slice []T, fn func(T) ([]R, error)) ([]R, error) {
	var out []R
	for _, v := range slice {
		rs, err := fn(v)
		if err != nil {
			return nil, err
		}
		out = append(out, rs...)
	}
	return out, nil
}

// Filter returns the elements for which pred returns true, in input
// order. Allocates a new slice; the input is not mutated.
//
//	active := q.Filter(users, func(u User) bool { return u.Active })
func Filter[T any](slice []T, pred func(T) bool) []T {
	var out []T
	for _, v := range slice {
		if pred(v) {
			out = append(out, v)
		}
	}
	return out
}

// FilterErr is Filter with a fallible predicate. First error
// short-circuits.
func FilterErr[T any](slice []T, pred func(T) (bool, error)) ([]T, error) {
	var out []T
	for _, v := range slice {
		ok, err := pred(v)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, v)
		}
	}
	return out, nil
}

// MapValues transforms each value of m via fn, preserving keys.
// Pairs naturally with q.GroupBy for "group then aggregate"
// pipelines:
//
//	counts := q.MapValues(q.GroupBy(items, byCat),
//	    func(group []Item) int { return len(group) })
//
//	sums := q.MapValues(q.GroupBy(items, byCat),
//	    func(g []Item) int { return q.Fold(g, 0, addAmount) })
//
// Allocates a fresh map; the input is not mutated.
func MapValues[K comparable, V1, V2 any](m map[K]V1, fn func(V1) V2) map[K]V2 {
	out := make(map[K]V2, len(m))
	for k, v := range m {
		out[k] = fn(v)
	}
	return out
}

// MapValuesErr is MapValues with a fallible fn. First error
// short-circuits and is returned; the partial map is discarded.
// Iteration order over m is map-random — there is no notion of
// "first error in input order" because Go's range over a map
// doesn't define one.
func MapValuesErr[K comparable, V1, V2 any](m map[K]V1, fn func(V1) (V2, error)) (map[K]V2, error) {
	out := make(map[K]V2, len(m))
	for k, v := range m {
		v2, err := fn(v)
		if err != nil {
			return nil, err
		}
		out[k] = v2
	}
	return out, nil
}

// MapKeys transforms each key of m via fn, preserving values. If
// fn produces collisions (two K1 keys mapping to the same K2 key),
// last-write-wins by Go's map-iteration semantics — the iteration
// order over m is undefined, so the surviving value is also
// undefined. Avoid collisions if it matters.
//
// Allocates a fresh map; the input is not mutated.
func MapKeys[K1, K2 comparable, V any](m map[K1]V, fn func(K1) K2) map[K2]V {
	out := make(map[K2]V, len(m))
	for k, v := range m {
		out[fn(k)] = v
	}
	return out
}

// MapKeysErr is MapKeys with a fallible fn. First error
// short-circuits.
func MapKeysErr[K1, K2 comparable, V any](m map[K1]V, fn func(K1) (K2, error)) (map[K2]V, error) {
	out := make(map[K2]V, len(m))
	for k, v := range m {
		k2, err := fn(k)
		if err != nil {
			return nil, err
		}
		out[k2] = v
	}
	return out, nil
}

// Pair carries two values. Used by q.Zip / q.Unzip and any callers
// that want a tiny tuple-like value type. Field names follow Go's
// stdlib (e.g. `database/sql.NullString.{Valid, String}`-ish — not
// `Key/Value` because Pair isn't always a key/value pair).
type Pair[A, B any] struct {
	First  A
	Second B
}

// Zip pairs the elements of as and bs in input order. The output
// length is min(len(as), len(bs)) — extra elements of the longer
// slice are dropped. Allocates a fresh slice.
//
//	pairs := q.Zip(names, ages)
//	for _, p := range pairs {
//	    fmt.Println(p.First, p.Second)
//	}
func Zip[A, B any](as []A, bs []B) []Pair[A, B] {
	n := min(len(as), len(bs))
	out := make([]Pair[A, B], n)
	for i := range n {
		out[i] = Pair[A, B]{First: as[i], Second: bs[i]}
	}
	return out
}

// Unzip splits a slice of pairs back into two parallel slices. The
// inverse of q.Zip:
//
//	names, ages := q.Unzip(q.Zip(names, ages))  // round-trip
func Unzip[A, B any](pairs []Pair[A, B]) ([]A, []B) {
	if len(pairs) == 0 {
		return nil, nil
	}
	as := make([]A, len(pairs))
	bs := make([]B, len(pairs))
	for i, p := range pairs {
		as[i] = p.First
		bs[i] = p.Second
	}
	return as, bs
}

// ZipMap pairs keys and values into a map. Output length is
// min(len(keys), len(values)) — extras dropped. Collisions among
// keys are last-write-wins.
//
//	byID := q.ZipMap(ids, names)
func ZipMap[K comparable, V any](keys []K, values []V) map[K]V {
	n := min(len(keys), len(values))
	out := make(map[K]V, n)
	for i := range n {
		out[keys[i]] = values[i]
	}
	return out
}

// Keys returns a slice of m's keys in unspecified order. Thin
// wrapper over `slices.Collect(maps.Keys(m))` — saves the import +
// the two-step incantation at the call site.
func Keys[K comparable, V any](m map[K]V) []K {
	return slices.Collect(maps.Keys(m))
}

// Values returns a slice of m's values in unspecified order. Thin
// wrapper over `slices.Collect(maps.Values(m))`.
func Values[K comparable, V any](m map[K]V) []V {
	return slices.Collect(maps.Values(m))
}

// MapEntries transforms each (key, value) pair of m via fn,
// producing a new map with possibly different K2 / V2 types. The
// one-pass form of MapKeys + MapValues when both transformations
// depend on each other or on the original entry.
//
//	type alias struct{ name string; v int }
//	canonical := q.MapEntries(byID, func(id int, a alias) (string, int) {
//	    return strings.ToLower(a.name), a.v
//	})
//
// Same collision caveat as MapKeys: if two source pairs produce the
// same K2, last-write-wins (and "last" is undefined per Go's
// map-iteration semantics).
func MapEntries[K1, K2 comparable, V1, V2 any](m map[K1]V1, fn func(K1, V1) (K2, V2)) map[K2]V2 {
	out := make(map[K2]V2, len(m))
	for k, v := range m {
		k2, v2 := fn(k, v)
		out[k2] = v2
	}
	return out
}

// MapEntriesErr is MapEntries with a fallible fn (returns
// `(K2, V2, error)`). First error short-circuits.
func MapEntriesErr[K1, K2 comparable, V1, V2 any](m map[K1]V1, fn func(K1, V1) (K2, V2, error)) (map[K2]V2, error) {
	out := make(map[K2]V2, len(m))
	for k, v := range m {
		k2, v2, err := fn(k, v)
		if err != nil {
			return nil, err
		}
		out[k2] = v2
	}
	return out, nil
}

// GroupBy buckets each element by the key fn returns. Bucket order
// within a group preserves input order. The result map is freshly
// allocated.
//
//	byCat := q.GroupBy(items, func(it Item) string { return it.Category })
func GroupBy[T any, K comparable](slice []T, fn func(T) K) map[K][]T {
	out := make(map[K][]T)
	for _, v := range slice {
		k := fn(v)
		out[k] = append(out[k], v)
	}
	return out
}

// Exists reports whether any element satisfies pred. Short-circuits
// on the first match (Scala's `exists`, samber/lo's `SomeBy`).
func Exists[T any](slice []T, pred func(T) bool) bool {
	return slices.ContainsFunc(slice, pred)
}

// ExistsErr is Exists with a fallible predicate. First error
// short-circuits ahead of any "found" decision.
func ExistsErr[T any](slice []T, pred func(T) (bool, error)) (bool, error) {
	for _, v := range slice {
		ok, err := pred(v)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// ForAll reports whether every element satisfies pred. Short-circuits
// on the first miss (Scala's `forall`, samber/lo's `EveryBy`).
// Vacuously true on an empty slice.
func ForAll[T any](slice []T, pred func(T) bool) bool {
	for _, v := range slice {
		if !pred(v) {
			return false
		}
	}
	return true
}

// ForAllErr is ForAll with a fallible predicate. First error
// short-circuits ahead of any "all match" decision.
func ForAllErr[T any](slice []T, pred func(T) (bool, error)) (bool, error) {
	for _, v := range slice {
		ok, err := pred(v)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// Find returns the first element satisfying pred, with ok=true; or
// (zero, false) if no element matches. Pairs naturally with q.Ok /
// q.OkE for bubble-on-not-found shapes:
//
//	user := q.Ok(q.Find(users, isAdmin))
//	user := q.OkE(q.Find(users, isAdmin)).Wrap("no admin user")
func Find[T any](slice []T, pred func(T) bool) (T, bool) {
	for _, v := range slice {
		if pred(v) {
			return v, true
		}
	}
	var zero T
	return zero, false
}

// Fold folds slice with init via fn (fold-left). The accumulator
// type R may differ from the element type T. When slice is empty,
// returns init unchanged. Scala's `foldLeft` shape.
//
//	sum := q.Fold(nums, 0, func(acc, n int) int { return acc + n })
//	csv := q.Fold(items, "", func(acc string, it item) string {
//	    if acc == "" { return it.Name }
//	    return acc + "," + it.Name
//	})
func Fold[T, R any](slice []T, init R, fn func(R, T) R) R {
	acc := init
	for _, v := range slice {
		acc = fn(acc, v)
	}
	return acc
}

// FoldErr is Fold with a fallible step fn. First error
// short-circuits — the partial accumulator is not returned (use the
// bubble path for "errored after partial work").
func FoldErr[T, R any](slice []T, init R, fn func(R, T) (R, error)) (R, error) {
	acc := init
	for _, v := range slice {
		next, err := fn(acc, v)
		if err != nil {
			var zero R
			return zero, err
		}
		acc = next
	}
	return acc, nil
}

// Reduce collapses slice into a single element using fn. The
// accumulator starts as the first element; fn is called for each
// subsequent element. T-only — both inputs and output share the
// element type. On empty input returns the zero value of T (no
// panic, no error sentinel) — Scala's `reduceLeft` panics; q's
// version leans on Go's zero-value default.
//
//	sum  := q.Reduce(nums, func(a, b int) int { return a + b })
//	first := q.Reduce(items, func(a, _ Item) Item { return a })
//
// Caveat: when fn is non-monoidal — i.e. `fn(zero, x) != x` — the
// empty-input result is mathematically meaningless: it's zero, not
// "no result". For max/min/multiply and similar, distinguish empty
// up front (`if len(slice) == 0`) or reach for q.Fold with an
// explicit identity:
//
//	mx := q.Fold(nums, math.MinInt, func(a, b int) int {
//	    if a > b { return a }
//	    return b
//	})
func Reduce[T any](slice []T, fn func(T, T) T) T {
	if len(slice) == 0 {
		var zero T
		return zero
	}
	acc := slice[0]
	for _, v := range slice[1:] {
		acc = fn(acc, v)
	}
	return acc
}

// Distinct returns each unique element preserving first-occurrence
// order. T must be comparable (uses a map for O(n) deduplication).
func Distinct[T comparable](slice []T) []T {
	if len(slice) == 0 {
		return nil
	}
	seen := make(map[T]struct{}, len(slice))
	out := make([]T, 0, len(slice))
	for _, v := range slice {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// Partition splits slice into (matching, nonMatching) by pred.
// Both slices preserve input order. Allocates two new slices.
func Partition[T any](slice []T, pred func(T) bool) ([]T, []T) {
	var yes, no []T
	for _, v := range slice {
		if pred(v) {
			yes = append(yes, v)
		} else {
			no = append(no, v)
		}
	}
	return yes, no
}

// Chunk groups slice into sub-slices of size n (the last may be
// shorter). Panics if n <= 0 — that's a programming error, not a
// recoverable runtime failure.
//
//	pages := q.Chunk(items, 50)
func Chunk[T any](slice []T, n int) [][]T {
	if n <= 0 {
		panic("q.Chunk: n must be positive")
	}
	if len(slice) == 0 {
		return nil
	}
	out := make([][]T, 0, (len(slice)+n-1)/n)
	for i := 0; i < len(slice); i += n {
		end := min(i+n, len(slice))
		out = append(out, slice[i:end])
	}
	return out
}

// Count returns the number of elements matching pred. Walks the
// whole slice; does not short-circuit (use q.Exists for that).
func Count[T any](slice []T, pred func(T) bool) int {
	n := 0
	for _, v := range slice {
		if pred(v) {
			n++
		}
	}
	return n
}

// Take returns the first n elements (or all of them if n exceeds
// len(slice)). Negative n is treated as 0.
func Take[T any](slice []T, n int) []T {
	if n <= 0 {
		return nil
	}
	n = min(n, len(slice))
	out := make([]T, n)
	copy(out, slice[:n])
	return out
}

// Drop returns slice with the first n elements removed (or empty if
// n exceeds len(slice)). Negative n is treated as 0.
func Drop[T any](slice []T, n int) []T {
	if n <= 0 {
		n = 0
	}
	if n >= len(slice) {
		return nil
	}
	out := make([]T, len(slice)-n)
	copy(out, slice[n:])
	return out
}
