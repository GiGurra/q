package q

// comptime_runtime.go — runtime helpers for q.NestedComptime.
//
// q.NestedComptime is the "compiler-per-recursive-level" variant of
// q.Comptime: each recursive call inside the impl spawns its own
// `go run -toolexec=q` subprocess, gated by a hash-keyed on-disk
// cache. For a recursive impl with overlapping subproblems (e.g.
// Fibonacci) the cache makes the build cost linear instead of
// exponential.
//
// Surface:
//
//   var Fib = q.NestedComptime(func(n int) int {
//       if n < 2 { return n }
//       return Fib(n-1) + Fib(n-2)
//   })
//
// Runtime path: q.NestedComptime is the identity function (returns
// its arg unchanged). At runtime in the user binary, calls to Fib
// run as ordinary Go recursion in-process — no subprocess spawn.
//
// Build-time path: each LEXICAL call site `Fib(literal)` in the user
// source is folded to a literal value computed by a synthesis
// subprocess. The synthesis subprocess uses ForkComptime, which
// spawns a sub-subprocess per recursive call (cached on disk).
//
// ForkComptime + RegisterComptimeImpl are exported so the synthesis
// programs the preprocessor generates can call them. Direct user
// code shouldn't call them.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// NestedComptime marks a function value as "compiler-per-recursion
// comptime". Every lexical call site of the returned fn (with
// preprocess-time-resolvable args) is folded to a literal at user
// package compile time, computed by a chain of nested compiler
// subprocesses with hash-keyed disk caching.
//
// At runtime the returned fn is the impl itself — invocations with
// runtime args run as ordinary Go recursion in-process.
//
// Restrictions match q.Comptime:
//
//   - Impl must be a function literal (not a named function reference).
//   - Recursion is allowed; cross-comptime calls (impl A calls impl B) are
//     allowed; capturing local vars is not.
//   - Args at every call site must be preprocess-time resolvable.
//
// Pick NestedComptime over Comptime when:
//
//   - The recursion has heavy overlap (memoisation pays off).
//   - You want the build cache to outlive a single build (the on-disk
//     cache lives at $GOCACHE/q-comptime/, cleared by `go clean -cache`).
//
// Otherwise prefer q.Comptime — it runs the whole recursion in one
// subprocess with negligible per-call overhead.
func NestedComptime[F any](impl F) F { return impl }

// comptimeImpls is the per-process registry of NestedComptime impl
// sources. Populated by init() functions the preprocessor generates
// (one per q.NestedComptime decl), keyed by an opaque key the
// preprocessor invents (typically `<userIdent>_<implHash>`).
//
// ForkComptime reads from this map when building a synthesis
// subprocess — the impl source is spliced into the subprocess's
// main.go template.
var comptimeImpls sync.Map // map[string]string

// RegisterComptimeImpl records an impl source under the given key.
// Called from init() functions emitted by the preprocessor; user
// code should not call it directly.
func RegisterComptimeImpl(key, src string) {
	comptimeImpls.Store(key, src)
}

// LookupComptimeImpl returns the source registered under key, or "".
// Used by ForkComptime when assembling a synthesis subprocess.
func LookupComptimeImpl(key string) string {
	if v, ok := comptimeImpls.Load(key); ok {
		return v.(string)
	}
	return ""
}

// ForkComptime spawns a fresh `go run -toolexec=q` subprocess to
// compute fn(args) for a NestedComptime-marked function and returns
// the result, caching by (key + impl source + args + Go version).
//
// On cache hit, returns immediately (~1ms file read). On miss,
// spawns a subprocess (~100-200ms startup + impl runtime), caches
// the result, returns.
//
// Used by the synthesis programs the preprocessor generates. Direct
// user calls work but are unusual.
//
// Termination: when the impl reaches a base case that doesn't
// recursively call back into ForkComptime, recursion bottoms out.
// True cycles (mutual recursion that never base-cases) are detected
// via Q_FORK_CHAIN env var and produce a panic.
func ForkComptime[A any, R any](key string, args A) R {
	src := LookupComptimeImpl(key)
	if src == "" {
		panic(fmt.Sprintf("q.ForkComptime: no impl registered for key %q (did the preprocessor's init() run?)", key))
	}

	cacheKey := computeForkCacheKey(key, src, args)

	// Cache check.
	if r, ok := readForkCache[R](cacheKey); ok {
		return r
	}

	// Cycle detection via env-var chain.
	chainKey := key + "|" + cacheKey[:16]
	if existing := os.Getenv("Q_FORK_CHAIN"); existing != "" {
		if strings.Contains(existing, chainKey) {
			panic(fmt.Sprintf("q.ForkComptime: cycle detected — chain=%q would re-enter %s with the same args", existing, chainKey))
		}
	}

	// Spawn subprocess.
	r := runForkSubprocess[A, R](key, src, args, chainKey)

	// Cache store.
	writeForkCache(cacheKey, r)
	return r
}

// computeForkCacheKey returns the cache key for a (key, src, args)
// triple. Stable across builds for the same Go version.
func computeForkCacheKey[A any](key, src string, args A) string {
	h := sha256.New()
	h.Write([]byte(runtime.Version()))
	h.Write([]byte{0})
	h.Write([]byte(key))
	h.Write([]byte{0})
	h.Write([]byte(src))
	h.Write([]byte{0})
	if argsJSON, err := json.Marshal(args); err == nil {
		h.Write(argsJSON)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// forkCacheDir returns the on-disk cache directory. Lives under
// $GOCACHE so `go clean -cache` clears it; falls back to user cache
// dir + "go-build" if GOCACHE is unset.
func forkCacheDir() string {
	if v := os.Getenv("GOCACHE"); v != "" {
		return filepath.Join(v, "q-comptime")
	}
	if v, err := os.UserCacheDir(); err == nil {
		return filepath.Join(v, "go-build", "q-comptime")
	}
	return filepath.Join(os.TempDir(), "q-comptime")
}

// readForkCache returns the cached value for cacheKey, or zero+false.
func readForkCache[R any](cacheKey string) (R, bool) {
	var zero R
	path := filepath.Join(forkCacheDir(), cacheKey+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return zero, false
	}
	var r R
	if err := json.Unmarshal(data, &r); err != nil {
		return zero, false
	}
	return r, true
}

// writeForkCache atomically writes the encoded value at cacheKey via
// tmp+rename so concurrent toolexec invocations don't corrupt the
// cache.
func writeForkCache[R any](cacheKey string, v R) {
	dir := forkCacheDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return // best-effort
	}
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	final := filepath.Join(dir, cacheKey+".json")
	tmp, err := os.CreateTemp(dir, cacheKey+".*.tmp")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return
	}
	_ = os.Rename(tmpPath, final)
}

// runForkSubprocess generates a tiny synthesis program for one
// (key, src, args) call, runs it under `go run -toolexec=q`, and
// parses the JSON result.
func runForkSubprocess[A any, R any](key, src string, args A, chainKey string) R {
	dir, err := os.MkdirTemp(forkWorkRoot(), "q-fork-")
	if err != nil {
		panic(fmt.Sprintf("q.ForkComptime: tempdir: %v", err))
	}
	defer func() { _ = os.RemoveAll(dir) }()

	argsJSON, err := json.Marshal(args)
	if err != nil {
		panic(fmt.Sprintf("q.ForkComptime: marshal args: %v", err))
	}

	progSrc := buildForkProgram(key, src, argsJSON)
	progPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(progPath, []byte(progSrc), 0o644); err != nil {
		panic(fmt.Sprintf("q.ForkComptime: write prog: %v", err))
	}

	qBin := os.Getenv("Q_BIN")
	if qBin == "" {
		// Fall back to argv[0] of this process — the q binary is
		// also the toolexec for the subprocess (since we run in
		// preprocess context).
		if exe, err := os.Executable(); err == nil {
			qBin = exe
		}
	}

	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "go", "run", "-toolexec="+qBin, progPath)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "Q_FORK_CHAIN="+chainKey+":"+os.Getenv("Q_FORK_CHAIN"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		panic(fmt.Sprintf("q.ForkComptime[%s]: subprocess failed: %v\nstderr:\n%s", key, err, stderr.String()))
	}

	var r R
	out := bytes.TrimSpace(stdout.Bytes())
	if err := json.Unmarshal(out, &r); err != nil {
		panic(fmt.Sprintf("q.ForkComptime[%s]: decode stdout %q: %v", key, string(out), err))
	}
	return r
}

// forkWorkRoot returns a parent dir for synthesis tempdirs. Uses
// $TMPDIR; the actual `go run` happens in a subdir created via
// MkdirTemp so concurrent forks don't collide.
func forkWorkRoot() string {
	if v := os.Getenv("Q_FORK_WORKDIR"); v != "" {
		_ = os.MkdirAll(v, 0o755)
		return v
	}
	return os.TempDir()
}

// buildForkProgram returns the Go source for a fork synthesis
// program: it imports pkg/q, registers all known impls (transitive
// dependencies of `key`), and runs the impl directly with the
// decoded args. Recursive calls inside the impl go through
// ForkComptime via package-level wrapper vars.
//
// `key` is currently unused inside the body — emitWrappers iterates
// all registered impls (the current key included). Reserved for the
// future call-graph-aware emit step.
func buildForkProgram(key, src string, argsJSON []byte) string {
	_ = key
	var b strings.Builder
	b.WriteString("package main\n\n")
	b.WriteString("import (\n")
	b.WriteString("\t\"encoding/json\"\n")
	b.WriteString("\t\"fmt\"\n")
	b.WriteString("\t\"os\"\n")
	b.WriteString("\tq \"github.com/GiGurra/q/pkg/q\"\n")
	b.WriteString(")\n\n")

	// Walk the impl source for references to other registered impls
	// and emit wrappers for each one. For now we emit wrappers for
	// every registered impl (overshoot is harmless — Go DCE drops
	// unreferenced ones).
	wrapperEmitted := map[string]bool{}
	emitWrappers(&b, &wrapperEmitted)

	// Splice the impl source as a top-level var.
	b.WriteString("var _qImpl = ")
	b.WriteString(src)
	b.WriteString("\n\n")

	// Args type is inferred from JSON encoding. We decode into the
	// argument type by re-encoding through json — but we need the
	// argument list shape. Given the MVP single-arg constraint,
	// embed argsJSON as a literal and call _qImpl with it splatted.
	// For now emit a generic decoder that only handles single int args.
	// TODO(multi-arg): extend to tuple structs.
	b.WriteString("func main() {\n")
	b.WriteString("\t// Decode the args literal.\n")
	b.WriteString("\tvar argsRaw json.RawMessage = []byte(`")
	b.Write(argsJSON)
	b.WriteString("`)\n")
	b.WriteString("\t_ = argsRaw\n")
	// q is already used via the wrapper var assignments emitted above
	// (q.ForkComptime + q.RegisterComptimeImpl), so no keep-alive needed.

	// MVP: assume single int arg, single int return.
	b.WriteString("\tvar n int\n")
	b.WriteString("\tif err := json.Unmarshal(argsRaw, &n); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }\n")
	b.WriteString("\tresult := _qImpl(n)\n")
	b.WriteString("\tdata, err := json.Marshal(result)\n")
	b.WriteString("\tif err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }\n")
	b.WriteString("\tos.Stdout.Write(data)\n")
	b.WriteString("}\n")
	return b.String()
}

// emitWrappers iterates the registered impls and emits a package-level
// var wrapper for each one. Each wrapper calls q.ForkComptime so
// recursive references inside any impl resolve to per-recursion
// subprocess spawns + cache.
//
// Wrappers are emitted as the impl-source-pattern detection has not
// been wired yet (the impl source is a string, not an AST). For MVP
// we emit a single wrapper for the current key.
func emitWrappers(b *strings.Builder, emitted *map[string]bool) {
	comptimeImpls.Range(func(k, v any) bool {
		ks, _ := k.(string)
		vs, _ := v.(string)
		if ks == "" || vs == "" {
			return true
		}
		if (*emitted)[ks] {
			return true
		}
		(*emitted)[ks] = true
		// Identify the user-facing name: it's encoded as the prefix
		// before the underscore in the key (we use `<userName>_<hash>`).
		// MVP assumption.
		idx := strings.LastIndex(ks, "_")
		userName := ks
		if idx > 0 {
			userName = ks[:idx]
		}
		fmt.Fprintf(b, "var %s = func(n int) int { return q.ForkComptime[int, int](%q, n) }\n", userName, ks)
		fmt.Fprintf(b, "func init() { q.RegisterComptimeImpl(%q, `%s`) }\n\n", ks, vs)
		return true
	})
}

