// example/lock mirrors docs/api/lock.md one-to-one. Run with:
//
//	go run -toolexec=q ./example/lock
package main

import (
	"fmt"
	"sync"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- "What q.Lock does" ----------
//
//	func (s *Store) Set(k, v string) {
//	    q.Lock(&s.mu)
//	    s.data[k] = v
//	}
type Store struct {
	mu   sync.Mutex
	data map[string]string
}

func NewStore() *Store { return &Store{data: map[string]string{}} }

func (s *Store) Set(k, v string) {
	q.Lock(&s.mu)
	s.data[k] = v
}

func (s *Store) Get(k string) string {
	q.Lock(&s.mu)
	return s.data[k]
}

// ---------- "RWMutex read side" ----------
//
//	q.Lock(s.rwm.RLocker())
type RWStore struct {
	rwm  sync.RWMutex
	data map[string]string
}

func NewRWStore() *RWStore { return &RWStore{data: map[string]string{}} }

func (s *RWStore) Read(k string) string {
	q.Lock(s.rwm.RLocker())
	return s.data[k]
}

func (s *RWStore) Write(k, v string) {
	q.Lock(&s.rwm)
	s.data[k] = v
}

func main() {
	s := NewStore()
	s.Set("a", "1")
	s.Set("b", "2")
	fmt.Printf("Store.Get(a)=%s\n", s.Get("a"))
	fmt.Printf("Store.Get(b)=%s\n", s.Get("b"))

	// Concurrent writes to verify locking actually serialises.
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Set(fmt.Sprintf("k-%d", i), "v")
		}()
	}
	wg.Wait()
	fmt.Printf("Store.size=%d\n", len(s.data))

	rs := NewRWStore()
	rs.Write("x", "42")
	fmt.Printf("RWStore.Read(x)=%s\n", rs.Read("x"))
}
