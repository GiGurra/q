# Phase 5: q.Comptime — design + roadmap to "true inception comptime"

This document is the resume-point for the Phase 5 work on q. It captures (a) what's already shipped, (b) the design exploration of "compiler-calls-compiler-per-recursive-level", (c) the three concrete implementation paths, and (d) the recommended next step.

A fresh session with no prior context should be able to read this and continue without re-deriving the constraints.

## State (as of commit `bb47f53`, 2026-04-25)

### Phase 5.1 — q.Comptime — SHIPPED

**Surface:**
```go
var Fib = q.Comptime(func(n int) int {
    if n < 2 { return n }
    return Fib(n-1) + Fib(n-2)
})

// Anywhere else in the package (or in importing packages):
result := fib.Fib(40)   // computed at compile time, ~3s build
```

**What it does:**
- Each LEXICAL call site of `Fib(args)` (with args resolvable at preprocess time — literals, package-level constants, other `q.AtCompileTime` / `q.Comptime` LHS bindings) becomes one `q.AtCompileTime`-style invocation.
- The synthesis pass spawns ONE subprocess per call site. Inside the subprocess, the impl runs as ordinary recursive Go code.
- Recursion happens at runtime in the subprocess, NOT by re-invoking the compiler.

**Implementation:**
- pkg/q surface: `Comptime[F any](impl F) F` — runtime identity (returns its arg unchanged).
- Scanner pre-pass `collectComptimeBindings` walks all package files, records `var X = q.Comptime(funcLit)` decls into a package-wide map.
- Per-file scan consults the map; `classifyQCall` produces a `familyComptimeCall` shape for any call expression whose `Fun` is an ident matching a comptime binding.
- Synthesis pass extended: emits the impl declaration once per unique binding (with self-references rewritten to a synthesised name), then per-call lines that invoke the impl with args.
- Decl rewrite uses an IIFE-wrapped self-referencing local var (`func() T { var _qfn T; _qfn = <impl with X→_qfn>; return _qfn }()`) so the user's `var X = q.Comptime(...)` doesn't trip Go's init-cycle detector when the closure recursively references X.

**Fixtures (passing):**
- `atct_comptime_fib_run_ok` — recursive fib at preprocess time
- `atct_comptime_combined_run_ok` — Fact + Power, cross-comptime ref, composition with q.AtCompileTime

### Phase 5.2 / Phase 5.3 — NOT yet started — design under exploration

The original tiered plan was:
- **Tier 2** = multi-pass rewriting (macros emitting macros)
- **Tier 3** = hash-keyed comptime cache

Both are doable. Together they were *also* informally promised to deliver "compiler calls compiler per recursive level" — i.e., `Fib(40)` computed not by Go-recursion-in-one-subprocess but by N nested compiler invocations, memoised so it's linear cost instead of exponential.

**Re-examination shows that combination doesn't actually work cleanly with the current architecture.** The rest of this document is the design exploration of why, and what the actual paths forward are.

## The fundamental constraint

`q.AtCompileTime[R](fn func() R) R` requires its closure to be fully evaluable at preprocess time. The closure has zero parameters and is invoked once per call site.

Recursive comptime functions need to bind args at the previous level's runtime:
```go
var Fib = q.Comptime(func(n int) int {
    if n < 2 { return n }
    return Fib(n-1) + Fib(n-2)  // n bound at runtime in the subprocess
})
```

When the synthesis subprocess executes the impl with `n=40`, the runtime expression `n-1` is a Go expression evaluated at runtime in the subprocess, not at preprocess time. There's no clean way to wrap `Fib(n-1)` in `q.AtCompileTime[int](func() int { return Fib(n-1) })` because the closure would close over runtime `n`, not a preprocess-time constant.

So multi-pass rewriting alone doesn't bridge from "Phase 5.1 single-subprocess" to "compiler per recursive call". Either (a) we add runtime support, (b) we add a partial evaluator, or (c) we keep the manually-unrolled pattern.

## Three paths

### Path A — Runtime fork helper (q.NestedComptime)

**Estimate: 5-8 sessions.** This is the most direct path to actual compiler-per-recursion with caching.

**Surface:**
```go
var Fib = q.NestedComptime(func(n int) int {
    if n < 2 { return n }
    return Fib(n-1) + Fib(n-2)
})
```

**Architecture:**

- pkg/q exposes a runtime helper `q.ForkComptime[A, R any](key string, args A) R`.
- The rewriter detects `var X = q.NestedComptime(impl)` decls. Substitutes the var with a wrapper:
  ```go
  var Fib = func(n int) int { return q.ForkComptime[int, int]("Fib", n) }
  ```
  The original impl source is registered with q (per package) under the key.
- `ForkComptime`'s implementation:
  1. Compute cache key (impl-source-hash + args).
  2. Check the on-disk cache. Hit → return cached value.
  3. Miss → write a tiny synthesis program that imports the user's package (or has the impl inlined), calls `<impl>(args)`, prints the JSON-encoded result.
  4. Run `go run -toolexec=<qBin>` on that program.
  5. Parse the output, store in cache, return.
- **Recursion:** when the subprocess RUNS the impl, recursive calls go through `ForkComptime` again. Each one is a child subprocess. Cache short-circuits repeats.

**Termination:** when args hit the base case (`n < 2` for fib), the impl returns directly without recursing.

**Cache (Phase 5.3):**
- Lives at `$GOCACHE/q-comptime/<sha256>.json`.
- Cleared by `go clean -cache`.
- Key includes Go version + impl source hash + args (canonicalised JSON of args).
- Read-write via temp file + atomic rename to handle concurrent toolexec invocations.

**Implementation steps:**

1. **pkg/q runtime helper.** `ForkComptime[A, R](key string, argsJSON []byte) R` — takes args as already-encoded JSON to handle generic args. Builds the synthesis program, runs subprocess, parses, caches.
2. **Synthesis program template.** Embeds the registered impl source. Calls impl with decoded args. Prints JSON of result.
3. **Impl registration.** The rewriter, when it sees `var X = q.NestedComptime(impl)`, generates a small init() that registers `(key, implSrc)` with the runtime. The runtime keeps a map from key → impl source.
4. **Hash-keyed cache.** sha256 of (Go version + impl source + canonicalised args). Cache hit short-circuits the subprocess.
5. **Cycle detection.** Track active fork keys per process; if a fork would re-enter an active key with the same args, diagnostic.

**Tradeoffs:**
- Without cache: fib(N) = O(2^N) subprocesses. Even fib(7) is 41 subprocesses ≈ 30s.
- With cache: fib(N) = O(N) unique subprocesses. fib(40) becomes feasible.
- Subprocess startup cost (~100ms) dominates. fib(40) with cache ≈ 4 seconds.
- The runtime helper adds a real dependency on q at runtime for `q.NestedComptime` users (vs. q.Comptime which is identity at runtime).

### Path B — Compile-time partial evaluator

**Estimate: 8-12 sessions.** Elegant but expensive.

The rewriter unfolds recursion at compile time. For each call site `Fib(literal)`:

1. Substitute the literal arg into the impl body.
2. Evaluate conditionals with constant-folding (`if 40 < 2 { ... }` becomes the false branch).
3. Identify recursive `Fib(...)` calls with literal args in the surviving branch.
4. Recursively unfold each.
5. Base case: emit the literal.

For fib(40) this would unfold to a 2^40-leaf arithmetic expression, which the Go compiler can constant-fold to a single integer. But the unfolding source size is enormous — fib(20) is already a million leaves.

Memoisation at compile-time-unfold (essentially Path A's cache, but in the rewriter not at runtime) would collapse fib(N) to N unique unfoldings.

**Implementation challenges:**
- The unfolder is essentially a Go interpreter. Need to handle: parameter substitution, conditionals, arithmetic, comparisons, slicing, calls to other functions (transitive impls).
- Stops being practical for non-trivial impls (loops, maps, etc.). Either restrict to a subset of Go (footgun documentation) or implement a real interpreter.
- Generated source size is the headline cost. Even with memoisation the call graph could be enormous.

This path is theoretically clean but practically bounded to small impls. Probably not worth pursuing unless someone has a compelling use case.

### Path C — Manually unrolled (already shipped)

The `atct_fib_recursive_run_ok` / `atct_fib5_recursive_run_ok` fixtures literally invoke the compiler per recursive level via manually nested `q.AtCompileTime` calls. Verbose source, but it's already there.

```go
// Manually unrolled fib(3):
fib3 := q.AtCompileTime[int](func() int {
    f2 := q.AtCompileTime[int](func() int {
        f1a := q.AtCompileTime[int](func() int { return 1 })
        f0  := q.AtCompileTime[int](func() int { return 0 })
        return f1a + f0
    })
    f1b := q.AtCompileTime[int](func() int { return 1 })
    return f2 + f1b
})
```

Best as a demonstration that the architecture composes — not a practical pattern.

## Recommendation

**Pursue Path A (runtime fork helper + cache).** This is the most direct path to:
- Clean source syntax (recursive Go function inside `q.NestedComptime`).
- Compiler-per-recursion (each call is a real subprocess).
- Linear cost via memoisation cache.
- Fib(40) at compile time in a few seconds.

Path B (partial evaluator) is more elegant in a vacuum but practically constrained — the source-size blow-up is fundamental.

Path C (manual unrolling) is already shipped; it's a demo pattern, not for everyday use.

## Implementation plan for Path A

### Step 1: pkg/q runtime helper

```go
// pkg/q/comptime_runtime.go (new file)

package q

import (
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "sync"
)

// comptimeImpls is the per-process map of registered comptime
// function impls. The rewriter generates an init() per
// q.NestedComptime decl that calls RegisterComptimeImpl with the
// impl source.
var comptimeImpls sync.Map // key: implKey, value: implSource string

// RegisterComptimeImpl is called from synthesised init()s.
func RegisterComptimeImpl(key, src string) {
    comptimeImpls.Store(key, src)
}

// ForkComptime is the runtime entry point — invoked per call to
// a NestedComptime function. Looks up cache, spawns subprocess
// on miss, caches result.
func ForkComptime[A any, R any](key string, args A) R {
    // 1. Hash args + impl + Go version.
    cacheKey := computeCacheKey(key, args)
    // 2. Cache check.
    if v, ok := readCache[R](cacheKey); ok {
        return v
    }
    // 3. Cache miss — synthesize tiny program + run subprocess.
    src := comptimeImpls.Load(key)
    // ... build main.go that imports/inlines impl, calls with args ...
    // ... run go run -toolexec=qBin ...
    // ... parse JSON, cache, return
}
```

### Step 2: Scanner detection of q.NestedComptime decls

Add `familyNestedComptimeDecl` family. Same shape as `familyComptimeDecl` but the rewrite is different.

### Step 3: Rewriter generates the wrapper

Original:
```go
var Fib = q.NestedComptime(func(n int) int { ... Fib(n-1) ... })
```

Rewritten:
```go
var Fib = func(n int) int {
    return q.ForkComptime[int, int]("Fib_<hash>", n)
}

func init() {
    q.RegisterComptimeImpl("Fib_<hash>", `func(n int) int { ... Fib(n-1) ... }`)
}
```

The impl source is embedded as a string literal. The synthesis program inside ForkComptime knows how to assemble it (likely as a wrapper that uses Fib recursively, which uses ForkComptime recursively).

### Step 4: Cache

`$GOCACHE/q-comptime/<sha256>.json`. Atomic write via temp + rename.

### Step 5: Fixtures

- `atct_nested_comptime_fib_run_ok` — `var Fib = q.NestedComptime(...)`. Build verifies subprocess-per-call recursion + cache.
- `atct_nested_comptime_cache_hit` — same fib called multiple times in main; cache hit assertions via subprocess invocation count (need to instrument the helper).
- `atct_nested_comptime_cycle_rejected` — mutual recursion that's actually cyclic, not just deep.

### Step 6: Documentation

Update `docs/api/atcompiletime.md` with the `q.NestedComptime` surface + tradeoffs vs `q.Comptime`. Add a "when to choose which" table.

## Open questions

1. **Cache invalidation.** When does the cache go stale? Impl source change → key change → cache miss naturally. Go version change → key includes it. Module dep version change → currently NOT in the key. May need a more aggressive key (whole module hash) for safety.

2. **Subprocess args encoding.** Path A passes args via... what? CLI args? STDIN? Environment? Probably stdin JSON, with stdout JSON for the result. Must handle all q.AtCompileTime-encodable types (anything codec-encodable).

3. **Error propagation.** A panic in the impl at level N should propagate up. Currently the `q.AtCompileTime` synthesis surfaces subprocess stderr on failure. The fork helper should do the same.

4. **Concurrency.** Multiple `go build -toolexec=q` running in parallel might race on the cache. Atomic rename solves this; need to verify on Windows (atomic-rename semantics differ).

5. **Stripping.** When the user does `go clean -cache`, our cache should clear too. Living at `$GOCACHE/q-comptime/` means it's auto-cleared. Good.

## Resume checklist

Picking up Phase 5.2 work cold:

1. Read this document end-to-end.
2. Check git log for any partial 5.2 commits since 2026-04-25.
3. Look at `pkg/q/comptime_runtime.go` — exists yet?
4. Search for `familyNestedComptimeDecl` in scanner.go.
5. Run existing comptime fixtures: `go test ./internal/preprocessor/ -run TestFixtures/atct_comptime` (Phase 5.1) and `TestFixtures/atct_nested` (Phase 5.2).
6. Pick the next step from "Implementation plan for Path A" above.

## What we did NOT pursue

- **The original "Tier 2 = multi-pass rewriting (macros emitting macros)"** — useful for `q.AtCompileTimeCode` returning source containing `q.AtCompileTime` calls. Doesn't enable compiler-per-recursion. Could still be worth shipping standalone if a use case appears.
- **Path B (compile-time partial evaluator)** — too expensive for the value provided.
- **Self-modifying generators** — `q.AtCompileTimeCode` that emits q.AtCompileTime calls. Theoretically interesting but the multi-pass rewriting needed is its own design exercise.

## Cross-references

- [docs/planning/atcompiletime.md](atcompiletime.md) — the q.AtCompileTime planning doc (Phases 1-4).
- [docs/api/atcompiletime.md](../api/atcompiletime.md) — user-facing API doc (Phases 1-4 surface).
- [docs/planning/TODO.md](TODO.md) — the persistent backlog.
- Commit `61044fe` — Phase 5.1 (q.Comptime) implementation.
- Commit `bb47f53` — bug fix for topo-sort using nil Closure.Pos.
