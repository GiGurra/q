// Fixture: q.Map / q.FlatMap / q.Filter / q.GroupBy / q.Exists /
// q.ForAll / q.Find / q.Fold / q.Reduce / q.Distinct / q.Partition /
// q.Chunk / q.Count / q.Take / q.Drop and their …Err variants. All
// pure runtime helpers — no preprocessor rewriting expected. The
// fixture also exercises composition with q.Try / q.TryE / q.Ok /
// q.OkE (which DO rewrite) over the …Err / Find / Reduce shapes.
package main

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	// Map / MapErr
	doubled := q.Map([]int{1, 2, 3}, func(n int) int { return n * 2 })
	fmt.Println("Map:", doubled)

	parsed, err := q.MapErr([]string{"1", "2", "3"}, strconv.Atoi)
	fmt.Println("MapErr ok:", parsed, err)

	_, err = q.MapErr([]string{"1", "x", "3"}, strconv.Atoi)
	fmt.Println("MapErr fail:", err != nil)

	// MapErr composes with q.Try
	if got, err := tryMap([]string{"10", "20", "30"}); err == nil {
		fmt.Println("q.Try(MapErr) ok:", got)
	}
	if _, err := tryMap([]string{"10", "x", "30"}); err != nil {
		fmt.Println("q.Try(MapErr) fail:", err.Error())
	}

	// MapErr composes with q.TryE.Wrap
	if _, err := tryEMap([]string{"x"}); err != nil {
		fmt.Println("q.TryE(MapErr).Wrap fail:", err.Error())
	}

	// FlatMap / FlatMapErr
	flat := q.FlatMap([]int{1, 2, 3}, func(n int) []int { return []int{n, n * 10} })
	fmt.Println("FlatMap:", flat)

	flatErr, _ := q.FlatMapErr([]int{1, 2}, func(n int) ([]int, error) {
		return []int{n, n * 10}, nil
	})
	fmt.Println("FlatMapErr ok:", flatErr)

	_, err = q.FlatMapErr([]int{1, 2}, func(n int) ([]int, error) {
		if n == 2 {
			return nil, errors.New("flat-fail")
		}
		return []int{n}, nil
	})
	fmt.Println("FlatMapErr fail:", err)

	// Filter / FilterErr
	even := q.Filter([]int{1, 2, 3, 4, 5}, func(n int) bool { return n%2 == 0 })
	fmt.Println("Filter:", even)

	parsedFilter, _ := q.FilterErr([]string{"1", "2", "3"}, func(s string) (bool, error) {
		n, err := strconv.Atoi(s)
		if err != nil {
			return false, err
		}
		return n%2 == 1, nil
	})
	fmt.Println("FilterErr ok:", parsedFilter)

	_, err = q.FilterErr([]string{"1", "x"}, func(s string) (bool, error) {
		_, err := strconv.Atoi(s)
		return true, err
	})
	fmt.Println("FilterErr fail:", err != nil)

	// GroupBy
	type item struct {
		cat string
		v   int
	}
	items := []item{{"a", 1}, {"b", 2}, {"a", 3}, {"b", 4}, {"a", 5}}
	grouped := q.GroupBy(items, func(it item) string { return it.cat })
	// Map iteration order is random; print sorted by key for stability.
	keys := make([]string, 0, len(grouped))
	for k := range grouped {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("GroupBy[%s]=%v\n", k, grouped[k])
	}

	// Exists / ExistsErr
	fmt.Println("Exists yes:", q.Exists([]int{1, 2, 3}, func(n int) bool { return n == 2 }))
	fmt.Println("Exists no:", q.Exists([]int{1, 2, 3}, func(n int) bool { return n == 9 }))

	yes, _ := q.ExistsErr([]string{"1", "2"}, func(s string) (bool, error) {
		n, err := strconv.Atoi(s)
		if err != nil {
			return false, err
		}
		return n == 2, nil
	})
	fmt.Println("ExistsErr yes:", yes)

	_, err = q.ExistsErr([]string{"x"}, func(s string) (bool, error) {
		_, err := strconv.Atoi(s)
		return true, err
	})
	fmt.Println("ExistsErr fail:", err != nil)

	// ForAll / ForAllErr
	fmt.Println("ForAll true:", q.ForAll([]int{2, 4, 6}, func(n int) bool { return n%2 == 0 }))
	fmt.Println("ForAll false:", q.ForAll([]int{2, 3, 6}, func(n int) bool { return n%2 == 0 }))
	fmt.Println("ForAll empty:", q.ForAll([]int{}, func(n int) bool { return false }))

	all, _ := q.ForAllErr([]int{2, 4}, func(n int) (bool, error) { return n%2 == 0, nil })
	fmt.Println("ForAllErr true:", all)

	// Find — bare comma-ok and composed with q.Ok / q.OkE
	if v, ok := q.Find([]int{10, 20, 30}, func(n int) bool { return n > 15 }); ok {
		fmt.Println("Find found:", v)
	}
	if _, ok := q.Find([]int{1, 2, 3}, func(n int) bool { return n > 99 }); !ok {
		fmt.Println("Find missing")
	}

	// q.Ok composition: bubble q.ErrNotOk on missing
	if _, err := findFirstAdmin([]string{"alice", "bob"}); err != nil {
		fmt.Println("q.Ok(Find) fail:", errors.Is(err, q.ErrNotOk))
	}
	// q.OkE.Wrap composition: custom message
	if _, err := findFirstAdminWrapped([]string{"alice"}); err != nil {
		fmt.Println("q.OkE(Find).Wrap fail:", err.Error())
	}

	// Fold / FoldErr — explicit init, R can differ from T.
	sum := q.Fold([]int{1, 2, 3, 4}, 0, func(acc, n int) int { return acc + n })
	fmt.Println("Fold:", sum)

	sumStr, _ := q.FoldErr([]string{"1", "2", "3"}, 0, func(acc int, s string) (int, error) {
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, err
		}
		return acc + n, nil
	})
	fmt.Println("FoldErr ok:", sumStr)

	_, err = q.FoldErr([]string{"1", "x"}, 0, func(acc int, s string) (int, error) {
		n, err := strconv.Atoi(s)
		return acc + n, err
	})
	fmt.Println("FoldErr fail:", err != nil)

	// Reduce — no init, T-only, zero-value on empty.
	mx := q.Reduce([]int{3, 1, 4, 1, 5, 9, 2, 6}, func(a, b int) int {
		if a > b {
			return a
		}
		return b
	})
	fmt.Println("Reduce max:", mx)

	emptySum := q.Reduce([]int{}, func(a, b int) int { return a + b })
	fmt.Println("Reduce empty:", emptySum)

	// Single-element input returns the element unchanged (fn not called).
	one := q.Reduce([]int{42}, func(a, b int) int { return a + b })
	fmt.Println("Reduce one:", one)

	// String concat — zero "" is the monoid identity, so this works for empty too.
	joined := q.Reduce([]string{"a", "b", "c"}, func(a, b string) string { return a + b })
	fmt.Println("Reduce concat:", joined)

	// Distinct
	fmt.Println("Distinct ints:", q.Distinct([]int{1, 2, 1, 3, 2, 4, 1}))
	fmt.Println("Distinct strs:", q.Distinct([]string{"a", "b", "a", "c", "b"}))
	fmt.Println("Distinct empty:", q.Distinct([]int{}))

	// Partition
	yesP, noP := q.Partition([]int{1, 2, 3, 4, 5}, func(n int) bool { return n%2 == 0 })
	fmt.Println("Partition yes:", yesP)
	fmt.Println("Partition no:", noP)

	// Chunk
	fmt.Println("Chunk 2:", q.Chunk([]int{1, 2, 3, 4, 5}, 2))
	fmt.Println("Chunk 5:", q.Chunk([]int{1, 2, 3}, 5))

	// Count
	fmt.Println("Count:", q.Count([]int{1, 2, 3, 4, 5}, func(n int) bool { return n > 2 }))

	// Take / Drop
	fmt.Println("Take 3:", q.Take([]int{1, 2, 3, 4, 5}, 3))
	fmt.Println("Take 99:", q.Take([]int{1, 2}, 99))
	fmt.Println("Take 0:", q.Take([]int{1, 2}, 0))

	fmt.Println("Drop 2:", q.Drop([]int{1, 2, 3, 4, 5}, 2))
	fmt.Println("Drop 99:", q.Drop([]int{1, 2}, 99))

	// MapValues — pairs with GroupBy for "group then aggregate."
	{
		type item struct {
			cat string
			v   int
		}
		items := []item{{"a", 1}, {"b", 2}, {"a", 3}, {"b", 4}, {"a", 5}}
		counts := q.MapValues(q.GroupBy(items, func(it item) string { return it.cat }), func(g []item) int { return len(g) })
		keys := make([]string, 0, len(counts))
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("MapValues counts[%s]=%d\n", k, counts[k])
		}
	}

	// MapValuesErr happy path + fail path.
	{
		m := map[string]string{"a": "1", "b": "2"}
		ok, _ := q.MapValuesErr(m, strconv.Atoi)
		ks := make([]string, 0, len(ok))
		for k := range ok {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			fmt.Printf("MapValuesErr ok[%s]=%d\n", k, ok[k])
		}
		_, err := q.MapValuesErr(map[string]string{"a": "1", "b": "x"}, strconv.Atoi)
		fmt.Println("MapValuesErr fail:", err != nil)
	}

	// MapEntries — transform keys and values together.
	{
		m := map[string]int{"a": 1, "b": 2, "c": 3}
		out := q.MapEntries(m, func(k string, v int) (string, int) {
			return strings.ToUpper(k), v * 10
		})
		ks := make([]string, 0, len(out))
		for k := range out {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			fmt.Printf("MapEntries[%s]=%d\n", k, out[k])
		}
	}

	// MapEntriesErr fail bubble.
	{
		_, err := q.MapEntriesErr(map[string]string{"a": "1", "b": "x"}, func(k, v string) (string, int, error) {
			n, err := strconv.Atoi(v)
			return k, n, err
		})
		fmt.Println("MapEntriesErr fail:", err != nil)
	}

	// Keys / Values — sorted for stable output.
	{
		m := map[string]int{"a": 1, "b": 2, "c": 3}
		ks := q.Keys(m)
		sort.Strings(ks)
		fmt.Println("Keys:", ks)
		vs := q.Values(m)
		sort.Ints(vs)
		fmt.Println("Values:", vs)
	}

	// MapKeys — rename keys.
	{
		m := map[string]int{"a": 1, "b": 2, "c": 3}
		upper := q.MapKeys(m, strings.ToUpper)
		ks := make([]string, 0, len(upper))
		for k := range upper {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			fmt.Printf("MapKeys upper[%s]=%d\n", k, upper[k])
		}
	}

	// Zip / Unzip — round-trip + truncates to shorter length.
	{
		names := []string{"alice", "bob", "carol"}
		ages := []int{30, 25, 28}
		pairs := q.Zip(names, ages)
		for _, p := range pairs {
			fmt.Printf("Zip: %s/%d\n", p.First, p.Second)
		}
		// Truncates to shorter length.
		short := q.Zip([]int{1, 2, 3}, []string{"a", "b"})
		fmt.Println("Zip truncate len:", len(short))
		// Round-trip via Unzip.
		ns, as := q.Unzip(pairs)
		fmt.Println("Unzip names:", ns)
		fmt.Println("Unzip ages:", as)
	}

	// ZipMap.
	{
		m := q.ZipMap([]string{"a", "b", "c"}, []int{1, 2, 3})
		ks := q.Keys(m)
		sort.Strings(ks)
		for _, k := range ks {
			fmt.Printf("ZipMap[%s]=%d\n", k, m[k])
		}
	}

	// Pipeline composition: chain bare ops
	pipeline := q.Fold(
		q.Filter(
			q.Map([]int{1, 2, 3, 4, 5}, func(n int) int { return n * n }),
			func(n int) bool { return n > 5 },
		),
		0,
		func(acc, n int) int { return acc + n },
	)
	fmt.Println("Pipeline:", pipeline)
}


func tryMap(strs []string) ([]int, error) {
	return q.Try(q.MapErr(strs, strconv.Atoi)), nil
}

func tryEMap(strs []string) ([]int, error) {
	return q.TryE(q.MapErr(strs, strconv.Atoi)).Wrap("parsing"), nil
}

func findFirstAdmin(names []string) (string, error) {
	return q.Ok(q.Find(names, func(s string) bool { return s == "admin" })), nil
}

func findFirstAdminWrapped(names []string) (string, error) {
	return q.OkE(q.Find(names, func(s string) bool { return s == "admin" })).Wrap("no admin"), nil
}
