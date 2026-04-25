# q.AtCompileTime — comprehensive implementation plan

This document is the resume-point for implementing TODO #82 (`q.AtCompileTime`). It is self-contained — a fresh session with no prior context should be able to read this and start implementing without re-deriving design decisions.

Cross-references:
- Surface sketch in [TODO.md #82](TODO.md) (kept lean; this doc is the authoritative plan)
- Related: TODO.md #84 (q.Assemble) shares some of the topo-sort + subprocess machinery
- Existing patterns to reuse: scanner family pattern (e.g. `familyMatch`), file synthesis (`gen.go`), runtime injection (`runtimestub.go`)

## Goal

Run pure Go code at preprocessor time and splice the result as a value at the call site. Universal escape hatch — every other compile-time helper (q.F, q.Snake, q.Match resolution, etc.) is a special case of this.

```go
hash := q.AtCompileTime(func() string { return md5sum("password") })
table := q.AtCompileTime(func() [256]uint32 { return crcTable() })
config := q.AtCompileTime(func() Config { return parseConfig("./config.json") })
```

Use cases that motivate it:
- Compile-time math + cryptographic precompute (hashes, lookup tables)
- Compile-time loading of static config / schema / templates
- Compile-time codegen of inlinable constants (sin tables, parser tables)
- Compile-time validation (parse-and-fail-build for malformed static data)

## Surface

```go
// Pure runtime stub — body panics if reached. Preprocessor rewrites
// every legitimate call site away.
func AtCompileTime[R any](fn func() R) R
```

**Restrictions enforced at compile time:**

1. The argument MUST be a `*ast.FuncLit` literal (not a named function reference, not a variable). Diagnostic: `q.AtCompileTime: argument must be a function literal (anonymous function), not a function reference or variable`.
2. The closure MUST have no captures from the enclosing scope, EXCEPT other `q.AtCompileTime` results (Phase 3). Phase 1 rejects all captures. Diagnostic: `q.AtCompileTime: closure body references local variable %s — comptime closures must be self-contained`.
3. R MUST be JSON-encodable in Phase 2+. Phase 1 limits R to primitives. Diagnostic: `q.AtCompileTime: return type %s is not JSON-encodable (functions, channels, complex types are unsupported)`.
4. The closure body MUST NOT call `q.*` in Phase 1+2 (preprocessor doesn't run on synthesized comptime programs). Phase 3 lifts this. Diagnostic: `q.AtCompileTime: closure body uses q.%s — q.* calls inside comptime closures are not supported in this version`.
5. Standard library + user's own module imports allowed (Phase 1 stdlib-only; Phase 2+ adds module).

## Phasing

Each phase is independently shippable. Don't merge phases.

### Phase 1 — primitives + stdlib only (1-2 sessions)

**Scope:**
- R limited to primitive types: `string`, `int`, `int8`, `int16`, `int32`, `int64`, `uint`, `uint8`, `uint16`, `uint32`, `uint64`, `uintptr`, `byte`, `rune`, `float32`, `float64`, `bool`.
- Closure body uses only stdlib imports + Go builtins.
- No captures from enclosing scope.
- Synthesize a self-contained Go file in $TMPDIR (no go.mod needed — stdlib only).
- Splice result as an inline Go literal at the call site (no JSON, no init() var).

**Deliverable:** Working q.AtCompileTime for the math / hash / string-precompute use cases. Multi-call-per-file works. Errors in the closure body produce build diagnostics.

### Phase 2 — JSON pass-through + module access (2-3 sessions)

**Scope:**
- R extended to JSON-encodable types: structs (exported fields), slices, maps, pointers, time.Time.
- Closure body can import the user's module (any package within it).
- $TMPDIR module setup with `replace` directive pointing to user's module root.
- For complex R: synthesize package-level `var _qCtN_value R` + `init()` doing `json.Unmarshal(<embedded JSON>, &_qCtN_value)`. Call site becomes the variable reference.
- For primitive R: still inline-literal splice (Phase 1 path).

**Deliverable:** Compile-time loading of arbitrary structured data — config files, parser tables, embedded resources.

### Phase 3 — chained AtCompileTime + q.* in closures (1-2 sessions)

**Scope:**
- AtCompileTime values can be referenced inside another AtCompileTime closure (topo-sort + substitute as literals).
- Closure body can use q.* (subprocess `go run` invoked with `-toolexec=q`).
- Caching: hash the (body source + module version + captured AtCompileTime values) and skip re-running on cache hit.

**Deliverable:** Full graph of compile-time computations. Useful for layered precomputes (load schema → derive lookup table → derive validators).

## Implementation architecture

Files to add / modify, in implementation order:

### 1. `pkg/q/atcompiletime.go` (new file)

Surface stub. Just the function declaration with `panicUnrewritten`.

```go
package q

// AtCompileTime evaluates fn at preprocessor time and splices the
// result as a value at the call site. … (full doc comment per the
// surface section above)
func AtCompileTime[R any](fn func() R) R {
    panicUnrewritten("q.AtCompileTime")
    var zero R
    return zero
}
```

### 2. `internal/preprocessor/scanner.go`

Add:
- `familyAtCompileTime` to the family enum
- Recognition in the `match*Call` chain — single arg, must be `*ast.FuncLit`
- Capture the `*ast.FuncLit` AST node, R's source text (from the index expression `q.AtCompileTime[R]`), and the call's outer span on the qSubCall

Position: alongside `familyExpr` (also takes one arg, also rewrites in-place). Add to `qRuntimeHelpers` carve-out check (to avoid trip on `panicUnrewritten` body call).

The `q.AtCompileTime` call is in-place (the call site's source span gets replaced by the resolved value). Use `isInPlaceFamily` to add it to the in-place family list.

### 3. `internal/preprocessor/typecheck.go`

New function `validateAtCompileTime`:
- Closure arg must be `*ast.FuncLit` (else diagnostic)
- Closure must have no free variables: walk the FuncLit's body for `*ast.Ident` nodes; for each, check `info.Uses[ident]` — if the resolved object's parent scope is OUTSIDE the closure's own scope AND it's not a package-level declaration in a stdlib (Phase 1) / user-module (Phase 2) package, reject.
- Phase 1: validate R is in the primitives list (look up `info.Types[indexExpr]` for the type-arg).
- Phase 2: validate R is JSON-encodable (recursive check via go/types: walk struct fields, slice/map elements, etc.).
- Walk closure body for q.* calls (Phase 1+2 reject; Phase 3 lift).

Add to the existing pass-2 loop in `checkErrorSlots` (alongside `validateExhaustive` / `resolveMatch`).

### 4. `internal/preprocessor/atcompiletime.go` (new file — the synthesis pass)

This is the bulk of the work. Architecture:

```go
// Phase 1: per-package, after typecheck and before rewrite.
// Collects all q.AtCompileTime call sites and resolves each one
// via subprocess. Returns a map[*qSubCall]string of resolved value
// literals (Go-source text), which the rewriter splices in.
func resolveAtCompileTimeCalls(
    fset *token.FileSet,
    pkgPath string,    // user's package import path (for Phase 2 module import)
    modRoot string,    // user's module root on disk (for Phase 2 replace dir)
    shapes []callShape,
) (map[*qSubCall]string, []Diagnostic, error)
```

For each shape with `Family == familyAtCompileTime`:

1. **Extract closure body source.** The FuncLit AST node has a `Body *ast.BlockStmt`. Use `printer.Fprint` to render the body to source text. Also extract:
   - R's source text (from the call's index expression — already captured in scanner)
   - The closure's parameter list (should be empty for `func() R`)
2. **Extract referenced imports.** Walk the FuncLit body for `*ast.SelectorExpr` of form `<pkgAlias>.<name>`; resolve `<pkgAlias>` against the file's `*ast.File.Imports`. Collect the unique set.
3. **Synthesize main.go.** Template:

```
package main

import (
    "encoding/json"
    "fmt"
    {{ range .Imports }}{{ . }}
    {{ end }}
)

func main() {
    result := func() {{ .ResultType }} {
        {{ .ClosureBody }}
    }()
    {{ if .UsePrintf }}
    fmt.Printf("%#v", result)
    {{ else }}
    data, err := json.Marshal(result)
    if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
    fmt.Println(string(data))
    {{ end }}
}
```

4. **Phase 1: write to $TMPDIR/q-comptime-<hash>/main.go.** No go.mod needed — stdlib-only resolves via GOROOT.
5. **Phase 2: write go.mod alongside main.go:**

```
module q-comptime
go 1.26
require <userModule> v0.0.0
replace <userModule> => <userModuleAbsPath>
```

`<userModule>` from the toolexec compile's `-importcfg` lookup (or `go env GOMOD` parsed). `<userModuleAbsPath>` from the dir of that go.mod.

6. **Run `go run main.go` (or `go run .` for Phase 2).** Capture stdout. Set a hard timeout (30s? configurable later).
7. **Parse stdout.** Phase 1: stdout is the `%#v` output, validate it parses as a Go literal via `parser.ParseExpr`. Phase 2 primitive R: same. Phase 2 complex R: stdout is JSON; encode it as a Go byte slice literal embedded in a generated `init()` (see #5 below).
8. **Return map of qSubCall → resolved literal text.**

### 5. `internal/preprocessor/rewriter.go`

Add `buildAtCompileTimeReplacement` similar to `buildExprReplacement`:

```go
func buildAtCompileTimeReplacement(sub qSubCall, resolved map[*qSubCall]string) string {
    return resolved[&sub]  // Phase 1 inline literal
}
```

For Phase 2 complex R, the rewriter generates a NEW file in $TMPDIR (synthesized companion file via `gen.go`-style mechanism) containing:

```go
package <pkg>

import "encoding/json"

var _qCt0_value SomeType
func init() {
    if err := json.Unmarshal([]byte(`{...JSON...}`), &_qCt0_value); err != nil {
        panic("q.AtCompileTime[N] decode failed: " + err.Error())
    }
}
```

And the call site rewrites to `_qCt0_value`. Reference the synthesized file via the existing rewrittenFiles plumbing.

### 6. `internal/preprocessor/userpkg.go`

Wire the resolution into the pipeline:

```go
// After typecheck, before rewrite:
atResolved, atDiags, err := resolveAtCompileTimeCalls(fset, pkgPath, modRoot, allShapes)
if err != nil { /* fatal */ }
diags = append(diags, atDiags...)
if len(diags) > 0 {
    return &Plan{Diags: diags}, nil
}

// Pass atResolved into the rewriter (extend rewriteFile signature).
```

### 7. Test fixtures

Per-phase fixtures under `internal/preprocessor/testdata/cases/`:

**Phase 1:**
- `atcompiletime_primitive_run_ok/` — int, string, bool, float results. Stdlib (strings, strconv, math, crypto/md5).
- `atcompiletime_no_captures_rejected/` — closure references a local variable; build fails with the captures diagnostic.
- `atcompiletime_non_funclit_rejected/` — `q.AtCompileTime(myFn)` with named function; build fails.
- `atcompiletime_q_in_body_rejected/` — closure body uses q.Try; build fails.
- `atcompiletime_runtime_panic_rejected/` — closure body panics at runtime; subprocess fails; build fails with the panic message.
- `atcompiletime_compile_error_rejected/` — closure body has a Go compile error (typo); subprocess `go run` fails; we surface the compiler error.

**Phase 2:**
- `atcompiletime_struct_run_ok/` — closure returns a struct; verifies init() decode + var reference.
- `atcompiletime_module_import_run_ok/` — closure body calls a function from the user's module.
- `atcompiletime_unexported_field_warned/` — struct with unexported fields is a soft warning (NOT a build failure — Go convention).
- `atcompiletime_cycle_rejected/` — closure returns a self-referential struct; json.Marshal fails; build fails.

**Phase 3:**
- `atcompiletime_chained_run_ok/` — one AtCompileTime's output captured by another's closure; topo-sort works.
- `atcompiletime_q_try_in_body_run_ok/` — closure body uses q.Try (subprocess invoked with -toolexec=q).
- `atcompiletime_cache_hit/` — second build of same code uses cached result.

## $TMPDIR module setup details (Phase 2)

Critical detail: the synthesized program needs to import the user's module. Mechanism:

1. **Find user's module root.** Parse the toolexec args; the compile's `-p <importPath>` gives the user's package import path. Also given is `-importcfg <file>` mapping deps to their `.a` archives — but we want the SOURCE not the archive (subprocess will compile from source). Read `go env GOMOD` from a process running in the toolexec's working directory (compile's cwd). `GOMOD` returns the path to the module's go.mod. The directory of that go.mod is the module root.

2. **Parse user's go.mod for the module path.** Extract the `module <path>` line. That's `<userModule>` for the replace directive.

3. **Write synthesized go.mod:**

```
module q-comptime-<hash>
go 1.26
require <userModule> v0.0.0-comptime
replace <userModule> => <abs-path-to-user-module-root>
```

4. **Vendor / dependency resolution.** The subprocess `go run` will need to resolve transitive deps. If the user's module uses `vendor/`, set `GOFLAGS=-mod=vendor` on the subprocess. Otherwise rely on `GOMODCACHE` (subprocess inherits parent env).

5. **Subprocess invocation:**

```go
cmd := exec.Command("go", "run", ".")
cmd.Dir = tmpDir
cmd.Env = append(os.Environ(),
    "GOFLAGS=", // override toolexec's flags so subprocess doesn't recurse
)
cmd.Stdout = &stdoutBuf
cmd.Stderr = &stderrBuf
if err := cmd.Run(); err != nil {
    // Surface stderr as a diagnostic
}
```

6. **Phase 3:** add `-toolexec=<qBin>` to the `go run` to allow q.* in the closure body. The `qBin` path: argv[0] of the current process (we're running as toolexec, so we know our own path).

## JSON encoding / decoding for complex R (Phase 2)

The synthesized main.go encodes via `json.Marshal`. The rewriter parses the output and either:

- **Primitive R:** generate `var _qCtN_value R = <inline-literal>` at the call site. Replace the call with `_qCtN_value` (or just inline the literal).
- **Complex R:** generate a companion file with:

```go
//go:build !ignore_q_atcompiletime

package <pkg>

import "encoding/json"

var _qCtN_value <R>

func init() {
    raw := []byte(`<JSON-text>`)
    if err := json.Unmarshal(raw, &_qCtN_value); err != nil {
        panic("q.AtCompileTime[N] decode failed: " + err.Error())
    }
}
```

The JSON text must be embedded as a Go raw string literal — but JSON containing backticks needs escaping. Use `strconv.Quote` instead and embed as a regular string literal, or use Go's ``` escape for backticks.

Cost analysis: one json.Unmarshal per call site at init() time. For typical config-file-sized data (kilobytes), this is sub-millisecond. Acceptable.

## Caching (Phase 3)

Cache key: hash of `(closure-body-source + sorted-list-of-imports + user-module-version + Go-version)`. Use SHA-256 or similar.

Cache location: `$GOCACHE/q-comptime/` (next to Go's existing cache so it's cleaned when the user runs `go clean -cache`).

Cache miss → subprocess runs, result written to cache.
Cache hit → read cached result, skip subprocess.

User module version: `git rev-parse HEAD` (fast for git repos; fallback to mtime sum of the module's source files). For Phase 3 to work without git, fall back to walking the module dir for source mtimes.

## Edge cases and caveats (across phases)

**1. Multiple call sites in the same package.** Phase 1: each gets its own subprocess. Phase 2: batch into a single synthesized program with N closures, all results JSON-marshalled to a single `[]any` and printed; rewriter parses the array. Faster (one `go run` per package).

**2. Determinism.** The subprocess MUST produce deterministic output for caching to be correct. Document: closures are pure (no time.Now, no random, no I/O of mutable state). Validation is best-effort — we can't fully sandbox.

**3. Build cache collision with parent's `-toolexec=q`.** When the subprocess runs `go run`, if it accidentally invokes the parent toolexec, infinite recursion. Strip `GOFLAGS` and other toolexec-related env from the subprocess env.

**4. Closure with `error` return.** Phase 1: reject closures returning `(R, error)` — for now, AtCompileTime's R must be a single value. If error handling is needed, the closure can panic and we surface it as a build error.

**5. Side effects in closure body.** Theoretically allowed (file reads, etc.) but must be deterministic. Document the rule. Future: sandbox via a restricted `GOENV`.

**6. Generic R.** Phase 1: reject `q.AtCompileTime[T]` where T is a type parameter. Only concrete R supported. Diagnostic.

**7. Closure parameters.** The closure must be `func() R` — zero parameters. Anything else is a diagnostic.

**8. Privacy: `cmd.Stderr` may leak source.** The subprocess's stderr might contain source paths if `go run` errors. Filter / sanitise before surfacing as a build diagnostic.

**9. Cross-platform.** `exec.Command` with relative paths works on all platforms. $TMPDIR is portable. JSON encoding is portable. Should "just work" on darwin / linux / windows.

**10. Concurrency safety.** Multiple toolexec compile invocations may run in parallel. Each must use its own $TMPDIR (unique by hash). The cache is read+write via file locking (or the simplest approach: write to a temp file, atomic rename to final cache path).

## Test strategy

Unit tests for the synthesis machinery (no subprocess):
- `parseClosureBody` extracts the right source text
- `collectImports` finds referenced packages
- `synthesizeMainGo` produces well-formed Go source
- `parseSubprocessOutput` round-trips primitives and JSON

Integration tests via the e2e fixture harness:
- Each phase has positive + negative fixtures (see Test fixtures section above)
- Run with `-race -count=3` to catch any flakiness in the subprocess plumbing

Manual smoke test: a checked-in example using AtCompileTime for something real (e.g. embedding a CRC table) under `example/`.

## Resume-from-cold-state checklist

Picking up this work fresh:

1. Read this plan end-to-end.
2. Check git log for any partial AtCompileTime commits since this plan was written.
3. Look at `pkg/q/atcompiletime.go` — exists yet?
4. Look at `internal/preprocessor/atcompiletime.go` — exists yet?
5. Search for `familyAtCompileTime` in scanner.go to gauge progress.
6. Run existing AtCompileTime fixtures: `go test ./internal/preprocessor/ -run TestFixtures/atcompiletime`
7. Pick the next phase based on what's done. Phase 1 is the entry point; don't start Phase 2 before Phase 1 is solid.

## Open questions to settle during implementation

- **JSON encoding format for the embedded literal.** Use raw string literal (with backtick escaping) or quoted string literal (with backslash escaping)? Quoted is safer for arbitrary JSON; raw is more readable. Lean: quoted.
- **Should AtCompileTime support `(R, error)` shape?** Initially no (panic on error in the closure body). Could add later as `q.AtCompileTimeE`.
- **Variadic / multi-result?** No. Single value out. Parse-and-build approach for that case is q.Assemble (TODO #84).
- **Should the closure see the call-site's package context?** Phase 2: yes, via importing the user's package. Phase 1: no (stdlib only).
- **What about `q.AtCompileTime` inside a generic function?** R is concrete at instantiation, but the preprocessor sees the generic source. Reject in Phase 1 — needs the generic instantiation point's type info, which lives at the call site, not the declaration. Phase 3 could lift this if there's demand.

## Why this matters

q.AtCompileTime is the one feature where every other macro is a special case:
- q.F → `q.AtCompileTime(func() string { return fmt.Sprintf(...) })`
- q.Snake → `q.AtCompileTime(func() string { return toSnake(s) })`
- q.SQL → `q.AtCompileTime(func() SQLQuery { return parseSQL(...) })`
- q.Match resolution → `q.AtCompileTime(func() string { return resolveMatch(...) })` (in spirit)

Once we ship it cleanly, future "I want a compile-time helper that does X" requests reduce to "wrap X in `q.AtCompileTime`." That's the unification this aims for.
