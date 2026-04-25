package q

// q.GoroutineID — the runtime-internal goroutine ID Go deliberately
// hides. q's toolexec preprocessor injects an exported GoroutineID
// function into the runtime package's compile (delegating to
// runtime.getg().goid), and this wrapper //go:linkname-pulls it.
// Without -toolexec=q the link fails, same as the rest of pkg/q.

import _ "unsafe" // for //go:linkname

//go:linkname runtimeGoroutineID runtime.GoroutineID
func runtimeGoroutineID() uint64

// GoroutineID returns the current goroutine's runtime ID — the integer
// shown in panic stack traces ("goroutine 17 [running]:") and the
// goroutine pprof profile. Type matches runtime.g.goid (uint64).
// Stable for the goroutine's lifetime.
func GoroutineID() uint64 {
	return runtimeGoroutineID()
}
