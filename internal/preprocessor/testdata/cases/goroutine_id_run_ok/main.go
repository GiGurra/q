// Fixture: q.GoroutineID. Verifies the runtime-injection path:
// the toolexec preprocessor adds an exported runtime.GoroutineID
// function to the stdlib runtime compile, and pkg/q's GoroutineID
// linkname-pulls it. Without -toolexec=q the link fails (same gate
// as the rest of pkg/q).
package main

import (
	"fmt"
	"sync"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	mainID := q.GoroutineID()
	fmt.Printf("main id nonzero: %v\n", mainID != 0)

	repeat := q.GoroutineID()
	fmt.Printf("main id stable across calls: %v\n", repeat == mainID)

	const N = 4
	var wg sync.WaitGroup
	var mu sync.Mutex
	childIDs := make(map[uint64]struct{})
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := q.GoroutineID()
			mu.Lock()
			childIDs[id] = struct{}{}
			mu.Unlock()
		}()
	}
	wg.Wait()

	fmt.Printf("distinct child ids: %d\n", len(childIDs))

	allDifferentFromMain := true
	for id := range childIDs {
		if id == mainID {
			allDifferentFromMain = false
		}
	}
	fmt.Printf("children differ from main: %v\n", allDifferentFromMain)
}
