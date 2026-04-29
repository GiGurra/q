// example/data mirrors docs/api/data.md one-to-one (representative
// shapes from each section). Run with:
//
//	go run -toolexec=q ./example/data
package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

type Item struct {
	Name string
	Cat  string
	Score int
}

// ---------- "Two flavours per fallible op — Map / MapErr" ----------
//
//	items := q.Map(rows, parseRow)
//	items, err := q.MapErr(rows, parseRowErr)
//	items := q.Try(q.MapErr(rows, parseRowErr))
func mapDemo() []int {
	return q.Map([]int{1, 2, 3}, func(n int) int { return n * 10 })
}

func mapErrDemo() ([]int, error) {
	return q.MapErr([]string{"1", "2", "3"}, strconv.Atoi)
}

func mapErrFailing() ([]int, error) {
	return q.MapErr([]string{"1", "bad", "3"}, strconv.Atoi)
}

func mapErrViaTry() ([]int, error) {
	items := q.Try(q.MapErr([]string{"1", "2", "3"}, strconv.Atoi))
	return items, nil
}

// ---------- Filter / FlatMap / GroupBy ----------

func filterDemo() []int {
	return q.Filter([]int{1, 2, 3, 4, 5}, func(n int) bool { return n%2 == 0 })
}

func flatMapDemo() []string {
	return q.FlatMap([]int{1, 2}, func(n int) []string {
		return []string{fmt.Sprintf("a%d", n), fmt.Sprintf("b%d", n)}
	})
}

func groupByDemo() map[string][]Item {
	items := []Item{{"x", "fruit", 1}, {"y", "veg", 2}, {"z", "fruit", 3}}
	return q.GroupBy(items, func(it Item) string { return it.Cat })
}

// ---------- Map ops ----------

func mapValuesDemo() map[string]int {
	items := []Item{{"x", "fruit", 1}, {"y", "veg", 2}, {"z", "fruit", 3}}
	groups := q.GroupBy(items, func(it Item) string { return it.Cat })
	return q.MapValues(groups, func(g []Item) int { return len(g) })
}

func mapKeysDemo() map[string]int {
	m := map[string]int{"a": 1, "b": 2}
	return q.MapKeys(m, strings.ToUpper)
}

// ---------- Predicate searches ----------

func existsForAllFind() (bool, bool, int, bool) {
	xs := []int{1, 2, 3, 4}
	exists := q.Exists(xs, func(n int) bool { return n > 3 })
	forAll := q.ForAll(xs, func(n int) bool { return n > 0 })
	v, ok := q.Find(xs, func(n int) bool { return n%2 == 0 })
	return exists, forAll, v, ok
}

// ---------- Reductions ----------

func foldReduceDemo() (int, int, string) {
	nums := []int{1, 2, 3, 4}
	sum := q.Fold(nums, 0, func(acc, n int) int { return acc + n })
	total := q.Reduce(nums, func(a, b int) int { return a + b })
	csv := q.Fold([]Item{{"x", "f", 1}, {"y", "v", 2}, {"z", "f", 3}}, "", func(acc string, it Item) string {
		if acc == "" {
			return it.Name
		}
		return acc + "," + it.Name
	})
	return sum, total, csv
}

func foldErrDemo() (int, error) {
	return q.FoldErr([]string{"10", "20", "30"}, 0, func(acc int, s string) (int, error) {
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, err
		}
		return acc + n, nil
	})
}

// ---------- Set / shape ----------

func setShapeDemo() ([]int, []int, []int) {
	xs := []int{1, 2, 2, 3, 3, 3, 4}
	dist := q.Distinct(xs)
	take := q.Take(xs, 3)
	drop := q.Drop(xs, 3)
	return dist, take, drop
}

func partitionDemo() ([]int, []int) {
	return q.Partition([]int{1, 2, 3, 4, 5}, func(n int) bool { return n%2 == 0 })
}

func chunkDemo() [][]int {
	return q.Chunk([]int{1, 2, 3, 4, 5}, 2)
}

// ---------- Sort ----------

func sortDemo() ([]int, []Item) {
	xs := q.Sort([]int{3, 1, 4, 1, 5, 9, 2})
	items := q.SortBy([]Item{{"x", "c", 1}, {"y", "a", 2}, {"z", "b", 3}}, func(it Item) string { return it.Cat })
	return xs, items
}

// ---------- Aggregations ----------

func aggDemo() (int, int, int, bool, int, bool) {
	xs := []int{3, 1, 4, 1, 5}
	sum := q.Sum(xs)
	mn, mnOk := q.Min(xs)
	mx, mxOk := q.Max(xs)
	return sum, mn, mx, mnOk, 0, mxOk
}

// ---------- Zip ----------

func zipDemo() ([]q.Pair[int, string], map[int]string) {
	pairs := q.Zip([]int{1, 2, 3}, []string{"a", "b", "c"})
	zm := q.ZipMap([]int{1, 2}, []string{"a", "b"})
	return pairs, zm
}

// ---------- ForEach ----------

func forEachDemo() string {
	var b strings.Builder
	q.ForEach([]int{1, 2, 3}, func(n int) { fmt.Fprintf(&b, "%d.", n) })
	return b.String()
}

func forEachErrDemo() error {
	return q.ForEachErr([]string{"1", "2", "bad", "4"}, func(s string) error {
		if _, err := strconv.Atoi(s); err != nil {
			return errors.New("stop at " + s)
		}
		return nil
	})
}

// ---------- Pipelining (named intermediates) ----------

func pipeline(items []Item) int {
	scores := q.Map(items, func(it Item) int { return it.Score })
	high := q.Filter(scores, func(s int) bool { return s > 50 })
	total := q.Fold(high, 0, func(acc, s int) int { return acc + s })
	return total
}

func main() {
	fmt.Printf("mapDemo: %v\n", mapDemo())
	if vs, err := mapErrDemo(); err != nil {
		fmt.Printf("mapErrDemo: err=%s\n", err)
	} else {
		fmt.Printf("mapErrDemo: %v\n", vs)
	}
	if _, err := mapErrFailing(); err != nil {
		fmt.Printf("mapErrFailing: err=%s\n", err)
	}
	if vs, err := mapErrViaTry(); err != nil {
		fmt.Printf("mapErrViaTry: err=%s\n", err)
	} else {
		fmt.Printf("mapErrViaTry: %v\n", vs)
	}

	fmt.Printf("filterDemo: %v\n", filterDemo())
	fmt.Printf("flatMapDemo: %v\n", flatMapDemo())
	g := groupByDemo()
	// determinism: print sorted keys
	keys := q.Sort(q.Keys(g))
	for _, k := range keys {
		fmt.Printf("groupByDemo[%s]: %v\n", k, g[k])
	}

	mv := mapValuesDemo()
	mvKeys := q.Sort(q.Keys(mv))
	for _, k := range mvKeys {
		fmt.Printf("mapValuesDemo[%s]: %d\n", k, mv[k])
	}
	mk := mapKeysDemo()
	mkKeys := q.Sort(q.Keys(mk))
	for _, k := range mkKeys {
		fmt.Printf("mapKeysDemo[%s]: %d\n", k, mk[k])
	}

	exists, forAll, v, ok := existsForAllFind()
	fmt.Printf("exists=%v forAll=%v find=%d ok=%v\n", exists, forAll, v, ok)

	s, t, c := foldReduceDemo()
	fmt.Printf("Fold-sum=%d Reduce-sum=%d Fold-csv=%s\n", s, t, c)
	if total, err := foldErrDemo(); err != nil {
		fmt.Printf("foldErrDemo: err=%s\n", err)
	} else {
		fmt.Printf("foldErrDemo: %d\n", total)
	}

	dist, take, drop := setShapeDemo()
	fmt.Printf("Distinct=%v Take=%v Drop=%v\n", dist, take, drop)

	yes, no := partitionDemo()
	fmt.Printf("Partition: yes=%v no=%v\n", yes, no)
	fmt.Printf("Chunk: %v\n", chunkDemo())

	sx, si := sortDemo()
	fmt.Printf("Sort: %v\n", sx)
	fmt.Printf("SortBy: %v\n", si)

	sum, mn, mx, mnOk, _, mxOk := aggDemo()
	fmt.Printf("Sum=%d Min=%d/ok=%v Max=%d/ok=%v\n", sum, mn, mnOk, mx, mxOk)

	pairs, zm := zipDemo()
	fmt.Printf("Zip: %v\n", pairs)
	zmKeys := q.Sort(q.Keys(zm))
	for _, k := range zmKeys {
		fmt.Printf("ZipMap[%d]: %s\n", k, zm[k])
	}

	fmt.Printf("ForEach: %s\n", forEachDemo())
	if err := forEachErrDemo(); err != nil {
		fmt.Printf("ForEachErr: err=%s\n", err)
	}

	items := []Item{{"a", "x", 80}, {"b", "x", 30}, {"c", "x", 75}, {"d", "x", 10}}
	fmt.Printf("pipeline: %d\n", pipeline(items))
}
