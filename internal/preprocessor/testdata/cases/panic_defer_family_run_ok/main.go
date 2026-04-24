// Fixture: statement-only panic/defer helpers — q.Lock, q.Go,
// q.TODO, q.Unreachable, q.Assert. All return nothing; the
// preprocessor rewrites them into their conventional
// defer/panic/goroutine shapes.
package main

import (
	"fmt"
	"sync"

	"github.com/GiGurra/q/pkg/q"
)

// store exercises q.Lock on both sync.Mutex (write lock) and
// sync.RWMutex (read lock via RLocker()).
type store struct {
	mu   sync.Mutex
	data map[string]int

	rwm   sync.RWMutex
	cache map[string]int
}

func newStore() *store {
	return &store{
		data:  map[string]int{},
		cache: map[string]int{},
	}
}

func (s *store) Set(k string, v int) {
	q.Lock(&s.mu)
	s.data[k] = v
}

func (s *store) Get(k string) int {
	q.Lock(&s.mu)
	return s.data[k]
}

func (s *store) CacheRead(k string) int {
	q.Lock(s.rwm.RLocker())
	return s.cache[k]
}

// stripLineNumber replaces the digits after "main.go:" with a
// literal "N" so the fixture expected-run output stays stable across
// edits to this source. Matches exactly `main.go:<digits>` once.
func stripLineNumber(s string) string {
	marker := "main.go:"
	i := 0
	for ; i+len(marker) <= len(s); i++ {
		if s[i:i+len(marker)] == marker {
			break
		}
	}
	if i+len(marker) > len(s) {
		return s
	}
	j := i + len(marker)
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	return s[:i+len(marker)] + "N" + s[j:]
}

// catchPanic runs body and recovers, returning the panic value as a
// string. Used to exercise q.TODO / q.Unreachable / q.Assert which
// all panic.
func catchPanic(name string, body func()) {
	defer func() {
		if r := recover(); r != nil {
			msg := fmt.Sprintf("%v", r)
			fmt.Printf("%s: %s\n", name, stripLineNumber(msg))
		}
	}()
	body()
}

// assertPasses / assertFails exercise q.Assert in both branches.
func assertPasses() {
	q.Assert(1+1 == 2, "arithmetic holds")
	fmt.Println("assertPasses: survived")
}

func assertFails() {
	q.Assert(false, "forced fail")
}

func assertNoMsg() {
	q.Assert(false)
}

func todoNoMsg() {
	q.TODO()
}

func todoWithMsg() {
	q.TODO("implement parser")
}

func unreachableNoMsg() {
	switch 3 {
	case 1, 2, 3:
		fmt.Println("reached 3")
	default:
		q.Unreachable()
	}
}

// goNormal exercises the success path.
func goNormal(done chan<- int) {
	q.Go(func() {
		done <- 7
	})
}

func main() {
	s := newStore()
	s.Set("a", 1)
	s.Set("b", 2)
	s.cache["x"] = 99
	fmt.Printf("Get(a)=%d\n", s.Get("a"))
	fmt.Printf("Get(b)=%d\n", s.Get("b"))
	fmt.Printf("CacheRead(x)=%d\n", s.CacheRead("x"))

	assertPasses()
	catchPanic("assertFails", assertFails)
	catchPanic("assertNoMsg", assertNoMsg)
	catchPanic("todoNoMsg", todoNoMsg)
	catchPanic("todoWithMsg", todoWithMsg)

	unreachableNoMsg()

	// q.Go happy path. The panic path is covered by a dedicated
	// fixture (q.Go's recover prints to stderr from a goroutine,
	// which races with stdout under combined-output capture).
	done := make(chan int, 1)
	goNormal(done)
	fmt.Printf("goNormal: %d\n", <-done)
}
