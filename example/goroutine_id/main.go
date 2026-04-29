// example/goroutine_id mirrors docs/api/goroutine_id.md one-to-one.
// Run with:
//
//	go run -toolexec=q ./example/goroutine_id
package main

import (
	"fmt"
	"sync"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- "Usage" ----------
//
//	id := q.GoroutineID()
func usage() (currentID uint64, childID uint64, sameAsCurrent bool) {
	currentID = q.GoroutineID()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		childID = q.GoroutineID()
	}()
	wg.Wait()

	sameAsCurrent = currentID == childID
	return
}

func main() {
	cur, child, same := usage()
	fmt.Printf("currentID > 0: %v\n", cur > 0)
	fmt.Printf("childID > 0: %v\n", child > 0)
	// Inheritance — child gets its own ID; doc explicitly notes "no
	// inheritance".
	fmt.Printf("child==current: %v (expected false)\n", same)
}
