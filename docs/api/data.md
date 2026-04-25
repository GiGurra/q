# Functional data ops: `q.Map`, `q.Filter`, `q.Fold`, `q.Reduce`, …

Functional data manipulation over slices. Pure runtime helpers — no preprocessor rewriting on the value path. The `…Err` flavours flow naturally through `q.Try` / `q.TryE` / `q.Ok` for the bubble path. Inspiration drawn from Scala collections and [samber/lo](https://github.com/samber/lo).

## Two flavours per fallible op

```go
// Bare — fn cannot fail.
items := q.Map(rows, parseRow)

// …Err — fn returns (R, error). First error short-circuits.
items, err := q.MapErr(rows, parseRowErr)

// Compose with q.Try / q.TryE for the bubble shape:
items := q.Try(q.MapErr(rows, parseRowErr))
items := q.TryE(q.MapErr(rows, parseRowErr)).Wrap("loading users")
```

There is **no `…E` chain flavour** of these helpers. `q.TryE(q.MapErr(…)).Wrap(…)` already produces the chain shape without a separate API. Don't multiply entry points without earning it.

## Catalog (first wave)

```go
// Slice transforms
func Map[T, R any](slice []T, fn func(T) R) []R
func MapErr[T, R any](slice []T, fn func(T) (R, error)) ([]R, error)

func FlatMap[T, R any](slice []T, fn func(T) []R) []R
func FlatMapErr[T, R any](slice []T, fn func(T) ([]R, error)) ([]R, error)

func Filter[T any](slice []T, pred func(T) bool) []T
func FilterErr[T any](slice []T, pred func(T) (bool, error)) ([]T, error)

func GroupBy[T any, K comparable](slice []T, fn func(T) K) map[K][]T

// Map ops (operate on map[K]V → map[K]V2)
func MapValues[K comparable, V1, V2 any](m map[K]V1, fn func(V1) V2) map[K]V2
func MapValuesErr[K comparable, V1, V2 any](m map[K]V1, fn func(V1) (V2, error)) (map[K]V2, error)
func MapKeys[K1, K2 comparable, V any](m map[K1]V, fn func(K1) K2) map[K2]V
func MapKeysErr[K1, K2 comparable, V any](m map[K1]V, fn func(K1) (K2, error)) (map[K2]V, error)
func MapEntries[K1, K2 comparable, V1, V2 any](m map[K1]V1, fn func(K1, V1) (K2, V2)) map[K2]V2
func MapEntriesErr[K1, K2 comparable, V1, V2 any](m map[K1]V1, fn func(K1, V1) (K2, V2, error)) (map[K2]V2, error)
func Keys[K comparable, V any](m map[K]V) []K     // slices.Collect(maps.Keys(m))
func Values[K comparable, V any](m map[K]V) []V   // slices.Collect(maps.Values(m))

// Zip / Unzip
type Pair[A, B any] struct{ First A; Second B }
func Zip[A, B any](as []A, bs []B) []Pair[A, B]            // truncates to min(len(as), len(bs))
func Unzip[A, B any](pairs []Pair[A, B]) ([]A, []B)
func ZipMap[K comparable, V any](keys []K, values []V) map[K]V

// Predicate searches (short-circuiting)
func Exists[T any](slice []T, pred func(T) bool) bool          // any
func ExistsErr[T any](slice []T, pred func(T) (bool, error)) (bool, error)
func ForAll[T any](slice []T, pred func(T) bool) bool          // all (vacuously true on empty)
func ForAllErr[T any](slice []T, pred func(T) (bool, error)) (bool, error)
func Find[T any](slice []T, pred func(T) bool) (T, bool)       // first match (comma-ok)

// Reductions
func Fold[T, R any](slice []T, init R, fn func(R, T) R) R                 // Scala foldLeft
func FoldErr[T, R any](slice []T, init R, fn func(R, T) (R, error)) (R, error)
func Reduce[T any](slice []T, fn func(T, T) T) T                          // no init; zero on empty

// Set / shape
func Distinct[T comparable](slice []T) []T                              // first-occurrence preserving
func DistinctBy[T any, K comparable](slice []T, fn func(T) K) []T       // dedup by derived key
func Partition[T any](slice []T, pred func(T) bool) ([]T, []T) // (yes, no)
func Chunk[T any](slice []T, n int) [][]T                      // panics if n <= 0
func Count[T any](slice []T, pred func(T) bool) int            // walks all (no short-circuit)
func Take[T any](slice []T, n int) []T                         // first n
func Drop[T any](slice []T, n int) []T                         // skip first n
```

## `Fold` vs `Reduce`

The two are distinct — keep the Scala separation rather than collapsing into one over-loaded `Reduce`.

|                   | `q.Fold`                          | `q.Reduce`                                      |
|-------------------|-----------------------------------|-------------------------------------------------|
| Init value        | explicit                          | first element (or zero on empty)                |
| Accumulator type  | may differ from element type      | same as element type                            |
| Empty input       | returns `init`                    | returns `T`'s zero value                        |
| Single element    | `fn(init, x)` runs once           | returns the element unchanged (fn not called)   |

```go
// Fold — explicit identity, R can differ from T
sum := q.Fold(nums, 0, func(acc, n int) int { return acc + n })
csv := q.Fold(items, "", func(acc string, it Item) string {
    if acc == "" { return it.Name }
    return acc + "," + it.Name
})

// Reduce — no init, T-only
total := q.Reduce(nums, func(a, b int) int { return a + b })
joined := q.Reduce(parts, func(a, b string) string { return a + "/" + b })
```

### `q.Reduce` on empty input

`q.Reduce` returns `T`'s zero value when the slice is empty. This is sound when fn is **monoidal** — i.e. `fn(zero, x) == x`:

- ✅ sum (`0 + x == x`)
- ✅ string concat (`"" + x == x`)
- ✅ slice append (`nil append x == x`)

It is **silently wrong** for non-monoidal fn:

- ❌ max — `max(0, -5)` is `0`, but the empty result is meaningless
- ❌ min — same in reverse
- ❌ multiply — `0 * x` is `0`, identity should be `1`
- ❌ struct types — zero `T{}` rarely satisfies `fn(zero, x) == x`

For the second category, reach for `q.Fold` with an explicit identity:

```go
mx := q.Fold(scores, math.MinInt, func(a, b int) int {
    if a > b { return a }
    return b
})
```

Or, if your fn really has no natural identity, distinguish empty up front:

```go
if len(scores) == 0 {
    return 0, errors.New("no scores")
}
mx := q.Reduce(scores, max)
```

## Map ops: `MapValues` and `MapKeys`

These operate on `map[K]V`, not `[]T` — they're the natural complement to slice transforms when you're already working with maps (often the output of `q.GroupBy`).

```go
// Group then aggregate — the most common shape
counts := q.MapValues(q.GroupBy(items, byCat),
    func(g []Item) int { return len(g) })

sums := q.MapValues(q.GroupBy(orders, byCustomer),
    func(g []Order) int { return q.Fold(g, 0, addAmount) })

// Rename keys
upper := q.MapKeys(byCat, strings.ToUpper)
```

Caveats:

- **Iteration order is map-random.** `MapValuesErr` / `MapKeysErr` short-circuit on the first error, but "first" is whichever the runtime visited first — not input-defined.
- **`MapKeys` collisions are last-write-wins.** If two source keys map to the same target key, only one value survives, and which one is undefined.

### `q.MapEntries` for the combined transform

When both keys and values change in a way that depends on each other, `q.MapEntries(m, func(K1, V1) (K2, V2)) map[K2]V2` does it in a single pass. Otherwise you'd chain `MapValues` then `MapKeys` (two passes) or write the loop by hand:

```go
canonical := q.MapEntries(byID, func(id int, a alias) (string, int) {
    return strings.ToLower(a.name), a.v
})
```

### `q.Keys` / `q.Values`

Thin wrappers over `slices.Collect(maps.Keys(m))` / `slices.Collect(maps.Values(m))` — the stdlib already provides this since Go 1.23, q just saves the import + the two-step incantation. Order is unspecified.

### Why no `q.ToMap` / `q.Associate`?

Building a map from a slice via a `func(T) (K, V)` projection is a one-liner — `for _, x := range xs { k, v := fn(x); m[k] = v }` — and silently drops collisions either to first or last value, depending on which side of the loop wins. `q.GroupBy` + `q.MapValues` makes the keep-first / keep-last / aggregate decision explicit.

## Pipelining

The bare ops chain naturally because each returns a slice (or compatible). Read inside-out the way Go forces — there is no method-chain syntax:

```go
total := q.Fold(
    q.Filter(
        q.Map(items, scoreOf),
        func(s int) bool { return s > 50 },
    ),
    0,
    func(acc, s int) int { return acc + s },
)
```

A chain of three nested calls is the upper end of comfortable; past that, name the intermediates:

```go
scores := q.Map(items, scoreOf)
high   := q.Filter(scores, func(s int) bool { return s > 50 })
total  := q.Fold(high, 0, func(acc, s int) int { return acc + s })
```

The ops compile to plain `for` loops with no per-element heap allocation beyond the output slice — same code you'd write by hand.

## Iterator (`iter.Seq`) variants — deferred

Go 1.23 ships `iter.Seq` / `iter.Seq2`. q's first wave is slice-only. Iterator-input variants (`q.MapSeq`, `q.FilterSeq`, …) are a follow-up wave once usage patterns settle. Slice → iterator can be done by hand via `slices.Values`; the reverse via `slices.Collect`. q won't paper over the conversion until there's a clear ergonomic win.

## Why no `…E` chain flavour?

`q.TryE(q.MapErr(…)).Wrap(…)` already produces the chain shape via existing rewriter machinery. A separate `q.MapE` would duplicate that without adding capability.

## See also

- [`q.Try` / `q.TryE`](try.md) — bubble + chain over `(T, error)`. Pairs with `…Err` variants.
- [`q.Ok` / `q.OkE`](ok.md) — bubble + chain over `(T, bool)`. Pairs with `q.Find`.
- [`q.AwaitAll`](await_multi.md) — concurrent `[]Future[T] → []T`. Different concern (parallelism over completed values vs. functional ops over a slice).
- [`q.ParMap` (TODO #81)](https://github.com/GiGurra/q/blob/main/docs/planning/TODO.md) — parallel variants of these ops, default `runtime.NumCPU()`. Not yet shipped.
