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
//
// The optional codec controls how the result is serialized between
// the preprocessor-time subprocess and the runtime user-package
// init. Default is q.JSONCodec[R](). Other built-ins:
// q.GobCodec[R](), q.BinaryCodec[R]() (for fixed-size types).
// Users can implement Codec[R] for custom encodings.
func AtCompileTime[R any](fn func() R, codec ...Codec[R]) R

// Codec encodes/decodes T to/from bytes. Used by q.AtCompileTime
// to pass values from preprocessor-time to runtime.
type Codec[T any] interface {
    Encode(v T) ([]byte, error)
    Decode(data []byte, v *T) error
}

func JSONCodec[T any]() Codec[T]   // encoding/json — text, lossy on unexported fields
func GobCodec[T any]() Codec[T]    // encoding/gob — handles unexported fields when registered
func BinaryCodec[T any]() Codec[T] // encoding/binary — fixed-size structs only, smallest output
```

Codecs are pure runtime — `Codec[T]` is a real interface, the constructors return real values, no preprocessor magic on the codec path. The preprocessor only needs to know which codec to invoke in the synthesized program (extracted from the call's second arg's source text).

**Restrictions enforced at compile time:**

1. The argument MUST be a `*ast.FuncLit` literal (not a named function reference, not a variable). Diagnostic: `q.AtCompileTime: argument must be a function literal (anonymous function), not a function reference or variable`.
2. The closure MUST have no captures from the enclosing scope, EXCEPT other `q.AtCompileTime` results — these ARE allowed (the result is constant by then; we splice it into the synthesized program). Captures of mutable / runtime values are rejected. Diagnostic: `q.AtCompileTime: closure body references local variable %s that is not itself a q.AtCompileTime result — comptime closures must be self-contained`.
3. R must round-trip through the chosen codec (default JSON). Codec validation at typecheck time uses go/types to walk R recursively and check encodability. Diagnostic: `q.AtCompileTime: return type %s is not encodable by %s codec (fields/elements that the codec can't handle: ...)`.
4. The closure body MUST NOT call `q.*` in Phase 1+2 (preprocessor doesn't run on synthesized comptime programs). Phase 3 lifts this. Diagnostic: `q.AtCompileTime: closure body uses q.%s — q.* calls inside comptime closures are not supported in this version`.
5. Standard library + user's own module imports allowed (Phase 1 stdlib-only; Phase 2+ adds module).
6. Recursive AtCompileTime references (A captures B captures A) are rejected at topo-sort time. Diagnostic: `q.AtCompileTime: cyclic dependency between AtCompileTime values: A → B → A`.

## Core architecture: one synthesis program per package

This is the load-bearing decision — call out at the top of every phase plan.

**Principle:** for each user-package compile that contains `q.AtCompileTime` calls, the preprocessor builds **one** synthesized program that evaluates **all** of those calls together, in topological order. The result is a single subprocess invocation per package compile, not one per call site.

**Why:**
- Cross-call captures: an AtCompileTime closure can reference another AtCompileTime result as a free variable. With one combined program, the dependent call simply uses the producing call's output as a Go-source-level constant.
- Subprocess startup cost: amortised across all calls. One `go run` instead of N.
- Cache locality: hash the combined program; cache hit reuses every value at once.

**Synthesized program shape:**

```go
package main

import (
    "encoding/json"
    "fmt"
    "os"
    qPkg "github.com/GiGurra/q/pkg/q"
    // ... user-module imports needed by any closure body
)

func main() {
    // Topo-ordered: A is computed before B if B captures A.
    _qCt0 := func() <R0> { /* closure body 0 */ }()
    _qCt1 := func() <R1> { /* closure body 1, may reference _qCt0 */ }()
    _qCt2 := func() <R2> { /* closure body 2, may reference _qCt0 or _qCt1 */ }()

    // Single output: a JSON array of per-call results, encoded by
    // each call's chosen codec, then wrapped in JSON for transport.
    out := []json.RawMessage{
        encodeWith(_qCt0, codec0),
        encodeWith(_qCt1, codec1),
        encodeWith(_qCt2, codec2),
    }
    data, err := json.Marshal(out)
    if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
    fmt.Println(string(data))
}

func encodeWith[T any](v T, codec qPkg.Codec[T]) json.RawMessage {
    raw, err := codec.Encode(v)
    if err != nil { panic(err) }
    // Wrap as JSON-quoted string so binary codecs survive transport.
    quoted, _ := json.Marshal(string(raw))
    return quoted
}
```

The rewriter then parses the top-level JSON array, decodes each element with `json.Unmarshal` to recover the inner bytes, and emits the per-call init+var pair (or inline-literal for primitives).

**Topological sort:** standard. Build a graph where edges go from a call to each AtCompileTime variable it captures. Reject cycles. Produce a flat order suitable for sequential execution. The captured-variable detection happens during typecheck — walk each closure body for `*ast.Ident` whose resolved object is the LHS of another AtCompileTime call site.

**Cross-call capture mechanics:** when call B's closure references variable `x` that was bound by call A's `x := q.AtCompileTime(...)`:
- In the synthesized program, A's call binds `_qCt<A> := <closure A>()` first.
- B's synthesized closure body has `x` rewritten to `_qCt<A>` before being spliced in.
- This is a per-package source-text rewrite limited to the synthesized program; the user's source stays untouched.

## Phasing

Each phase is independently shippable. Don't merge phases.

### Phase 1 — single-pass core, primitives + stdlib (2-3 sessions)

**Scope:**
- One synthesized program per user-package compile (the core architecture above).
- Cross-call captures of other AtCompileTime values supported from day one (closures can reference earlier-resolved AtCompileTime variables; topo-sort enforces ordering).
- R limited to primitive types: `string`, `int`, `int8`, `int16`, `int32`, `int64`, `uint`, `uint8`, `uint16`, `uint32`, `uint64`, `uintptr`, `byte`, `rune`, `float32`, `float64`, `bool`.
- Codec interface defined and `JSONCodec[T]` implemented (the only built-in for Phase 1).
- Closure body uses only stdlib imports + Go builtins. No user-module imports yet (Phase 2).
- $TMPDIR holds the synthesized main.go; no go.mod needed for Phase 1 (stdlib resolves via GOROOT).
- Output: each call's primitive result spliced as inline Go literal at the call site. No init()/var synthesis yet — just literal substitution.

**Deliverable:** Working q.AtCompileTime for math / hash / string-precompute use cases, with cross-call composition: `b := q.AtCompileTime(func() string { return strings.Repeat("a", a*2) })` works when `a` is itself an AtCompileTime int.

### Phase 2 — Codec-based encoding + module access (3-4 sessions)

**Scope:**
- R extended to any type the chosen codec accepts (default JSON: structs with exported fields, slices, maps, pointers, time.Time).
- Built-in codecs: `q.GobCodec[T]` and `q.BinaryCodec[T]` added alongside `JSONCodec`.
- Codec parameter resolved at call site; subprocess uses the same codec for encoding.
- Closure body can import the user's module (any package within it).
- $TMPDIR module setup with `replace` directive pointing to user's module root.
- For non-primitive R: synthesize a companion file (`_q_atcomptime.go`) at the package level with one `var _qCtN_value R` + `init()` per call. The init() decodes the embedded bytes via the call's codec. Call site rewrites to `_qCtN_value`.
- For primitive R: still inline-literal splice (Phase 1 path) when the codec is JSONCodec; for other codecs, fall back to var/init() shape.

**Deliverable:** Compile-time loading of arbitrary structured data via the user's module. User can specify gob (for unexported fields) or binary (for fixed structs) instead of JSON.

### Phase 3 — q.* in closures + custom codecs (1-2 sessions)

**Scope:**
- Closure body can use q.* (subprocess `go run` invoked with `-toolexec=q`).
- Custom user-defined codecs (any type implementing `q.Codec[T]`).

**Deliverable:** Full graph of compile-time computations with arbitrary domain-specific encoders.

**Build caching:** Go's build cache handles incremental builds. Source unchanged → Go reuses the previously compiled artifact and the synthesis subprocess doesn't re-run. Source changed → full re-expansion. Closures must be deterministic for this to be sound.

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

This is the bulk of the work. The pass is **per-package, single subprocess invocation, all calls resolved together** (the core architecture above).

```go
// Per-package, after typecheck and before rewrite. Collects every
// q.AtCompileTime call site, topo-sorts by inter-call captures,
// builds one synthesized program, runs it, and returns the
// resolved values keyed by qSubCall.
func resolveAtCompileTimeCalls(
    fset *token.FileSet,
    pkgPath string,    // user's package import path (Phase 2 module import)
    modRoot string,    // user's module root on disk (Phase 2 replace dir)
    shapes []callShape,
) (map[*qSubCall]resolvedValue, []Diagnostic, error)

type resolvedValue struct {
    Literal     string  // Go-source text for inline-splice (primitive + JSONCodec only)
    EncodedRaw  []byte  // raw codec output for var+init() embedding (every other case)
    CodecExpr   string  // user's codec expression source (for the init's decode call)
    UseInline   bool    // pick the route at rewriter time
}
```

**Pipeline per package:**

1. **Collect.** Filter shapes for `Family == familyAtCompileTime`. Build per-call records: closure FuncLit, R's type-text, codec arg's source text, the LHS binding name (if any) for cross-call capture detection.
2. **Build dep graph.** For each call, walk its closure body for `*ast.Ident` whose resolved object is the LHS of another AtCompileTime call. Edge from this call to the producing call.
3. **Topo-sort.** Kahn's algorithm. Cycle → diagnostic.
4. **Extract imports.** Per-call FuncLit body: walk for `*ast.SelectorExpr`; resolve aliases against the file's imports. Union into a single dedup-ed import set.
5. **Synthesize main.go.** Template:

```go
package main

import (
    "encoding/json"
    "fmt"
    "os"
    qPkg "github.com/GiGurra/q/pkg/q"
    {{ range .ExtraImports }}{{ . }}
    {{ end }}
)

func main() {
    {{ range $i, $c := .Calls }}
    _qCt{{ $i }} := func() {{ $c.ResultType }} {
        {{ $c.ClosureBody }}
    }()
    {{ end }}

    out := []json.RawMessage{
        {{ range $i, $c := .Calls }}
        encodeWith(_qCt{{ $i }}, {{ $c.CodecExpr }}),
        {{ end }}
    }
    data, err := json.Marshal(out)
    if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
    fmt.Println(string(data))
}

func encodeWith[T any](v T, codec qPkg.Codec[T]) json.RawMessage {
    raw, err := codec.Encode(v)
    if err != nil { panic(err) }
    quoted, _ := json.Marshal(string(raw))
    return quoted
}
```

Cross-call capture rewrite: when call B's closure body references variable `x` and `x` resolves to call A's LHS binding, rewrite the spliced body to use `_qCt<A-index>` instead of `x`. Done by walking B's FuncLit AST and substituting source spans during template emission.

6. **Phase 1: write to $TMPDIR/q-comptime-<pkgHash>/main.go.** No go.mod (stdlib only).
7. **Phase 2: write go.mod alongside main.go:**

```
module q-comptime
go 1.26
require <userModule> v0.0.0-comptime
replace <userModule> => <userModuleAbsPath>
```

`<userModule>` from `go env GOMOD` parsed (run from the toolexec's compile cwd). `<userModuleAbsPath>` from the dir containing that go.mod.

8. **Run `go run .`** in the synthesized dir. Hard timeout (configurable; default ~60s). Capture stdout + stderr. Strip toolexec-related env so the subprocess doesn't recurse:

```go
cmd.Env = filterEnv(os.Environ(), []string{"GOFLAGS"})
```

Phase 3: invoke with `-toolexec=<argv0>` so q.* in closure bodies works.

9. **Parse stdout.** Top-level JSON array. Each element is a JSON-quoted string carrying the codec's raw bytes. For each call, build a resolvedValue:
   - If R is primitive AND codec is JSONCodec: parse the JSON value (which is the primitive itself), format as a Go literal, set `UseInline=true`.
   - Otherwise: store the raw bytes + codec expression, set `UseInline=false`.
10. **Return map.**

### 5. `internal/preprocessor/rewriter.go`

Add `buildAtCompileTimeReplacement(sub qSubCall, resolved map[*qSubCall]resolvedValue) string`:

- If `resolved[sub].UseInline`: return `resolved[sub].Literal` directly. No companion file needed.
- Otherwise: return `_qCt<N>_value` (the package-level var the synthesis pass arranges). The call site becomes a plain identifier reference.

For non-inline calls, the rewriter coordinates with a synthesis pass (similar to `gen.go`'s `_q_gen.go` shape) that emits a single companion file `_q_atcomptime.go` per package containing all the var + init() pairs:

```go
//go:build !ignore_q_atcompiletime

package <pkg>

import (
    "github.com/GiGurra/q/pkg/q"
    {{ range .ExtraImports }}{{ . }}
    {{ end }}
)

var _qCt0_value SomeType
var _qCt1_value AnotherType

func init() {
    {
        data := []byte("<\\x-escaped-bytes>")
        codec := q.JSONCodec[SomeType]()  // user's codec expr
        if err := codec.Decode(data, &_qCt0_value); err != nil {
            panic("q.AtCompileTime[0] decode failed: " + err.Error())
        }
    }
    {
        data := []byte("<\\x-escaped-bytes>")
        codec := userPkg.MyCodec[AnotherType]()
        if err := codec.Decode(data, &_qCt1_value); err != nil {
            panic("q.AtCompileTime[1] decode failed: " + err.Error())
        }
    }
}
```

One `init()` body holds all decodes (separate `{}` blocks for variable scoping). Reference the synthesized file via the existing rewrittenFiles plumbing (same pattern as `gen.go`).

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

## Codec-based encoding / decoding (Phase 2)

The Codec interface (defined in `pkg/q/atcompiletime.go`):

```go
type Codec[T any] interface {
    Encode(v T) ([]byte, error)
    Decode(data []byte, v *T) error
}
```

Built-in implementations live alongside (in the same file):
- `JSONCodec[T any]() Codec[T]` — encoding/json wrapper. Default. Lossy on unexported fields.
- `GobCodec[T any]() Codec[T]` — encoding/gob wrapper. Handles unexported fields when `gob.Register(T{})` is called once. Larger output than JSON.
- `BinaryCodec[T any]() Codec[T]` — encoding/binary wrapper. Fixed-size types only (no slices, maps, strings). Smallest output.

**Transport.** The synthesized program runs each call's encoder, then wraps the resulting `[]byte` as a JSON-quoted string so binary outputs survive stdout transport:

```go
encoded, err := codec0.Encode(_qCt0)
if err != nil { panic(err) }
encodedQuoted, _ := json.Marshal(string(encoded))  // "\"...\""
// All N calls' quoted strings go into a single top-level []json.RawMessage.
```

The rewriter parses the top-level JSON array, decodes each `json.RawMessage` into a Go string (recovering the raw bytes), and embeds those bytes in the user's package via Go's `\x`-escape string literal syntax (compact, supports arbitrary bytes):

```go
//go:build !ignore_q_atcompiletime

package <pkg>

var _qCt0_value <R>

func init() {
    data := []byte("<\\x-escaped-bytes>")
    codec := q.JSONCodec[<R>]()
    if err := codec.Decode(data, &_qCt0_value); err != nil {
        panic("q.AtCompileTime[0] decode failed: " + err.Error())
    }
}
```

The codec construction in init() is the source text of the user's codec arg (e.g. `q.JSONCodec[Config]()` or `customCodec()`) — same expression as written at the call site. This guarantees encode/decode symmetry.

**Inline literals (primitive R + JSONCodec):** for cheapness, still emit a Go literal at the call site instead of embedding-and-decoding:

```go
// Source:
n := q.AtCompileTime(func() int { return fib(40) })
// Rewritten:
n := 102334155
```

This bypasses the var+init() route. Detection: R is in the primitive set AND codec is JSONCodec (default). For other codecs even on primitive R, fall back to var+init() — keeps the codec-roundtrip symmetric.

## Build caching

q does not maintain its own cache. Go's build cache covers it: when the user's source is unchanged, Go reuses the previously compiled package artifact and the synthesis subprocess does not re-run. Source change triggers full re-expansion.

This requires closures to be deterministic — same source must produce the same output run-to-run. Closures that read `time.Now()`, `os.Getenv()`, randomness, or other non-deterministic I/O break this assumption and should be avoided.

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

## Phase 4 — code generation (macros) and recursive comptime — SHIPPED

Both pieces shipped:

- **Phase 4.2** (recursive comptime): the recursion-rejection diagnostic in `validateAtCompileTime` was lifted; the existing `-toolexec=<qBin>` passthrough on the synthesis subprocess (added in Phase 3) was already enough — q.AtCompileTime calls inside a closure body get processed by a recursive q invocation that synthesizes its own `.q-comptime-<hash>/` directory. Fixture `atct_recursive_run_ok` exercises 2-level deep nesting; `atct_fib_recursive_run_ok` and `atct_fib5_recursive_run_ok` compute fib(4) and fib(5) entirely at compile time across 4-5 levels of compiler-recursive q invocations.

- **Phase 4.1** (code generation): new surface `q.AtCompileTimeCode[R any](fn func() string) R`. Closure returns a Go expression as a string; the rewriter splices the parsed expression in place of the call. Fixture `atct_codegen_run_ok` covers function-literal / string / multi-line switch; `atct_codegen_combined_run_ok` shows code-gen composing with cross-call captures (a code-gen closure references a value-returning AtCompileTime LHS, baking the value into the generated source); `atct_codegen_invalid_rejected` confirms malformed source fails the build.

### Phase 4.1 — `q.AtCompileTime` returning code

Today `q.AtCompileTime[R]` returns a value of type R. A Phase 4 extension would let the closure return Go SOURCE CODE that the rewriter parses + splices into the user's package as actual declarations. This is a real macro system — closer to Zig `comptime` or Lisp macros than to constexpr.

**Surface sketch (working):**

```go
// User declares the type they want to fill in:
var greet func(name string) string = q.AtCompileTimeCode(func() string {
    // Closure builds source code for the function body.
    return `func (name string) string { return "Hello, " + name }`
})
```

The preprocessor:
1. Runs the closure at preprocessor time (same subprocess infrastructure as Phase 1+2).
2. Captures the returned string as a Go expression / declaration source.
3. Parses the returned source via go/parser.
4. Inlines the parsed AST at the call site (replacing the q.AtCompileTimeCode call with the parsed expression / decl).

**Why this is a real macro system:** the closure doesn't return DATA, it returns CODE. The compiler then compiles the generated code as if the user had written it directly. Combined with Phase 3 (q.* allowed inside the closure), users can build templated Go code from data + iteration + branching, producing zero-overhead specialised functions per use site.

**Use cases:**
- Inline-specialised `Map`/`Filter`/etc. without runtime function-call overhead (the closure emits a flat `for { ... }` for the specific T, R).
- Domain-specific languages that compile to Go (SQL builders, regex compilers, parser combinators).
- Per-type "marshallers" generated from a struct's reflection — emit straight-line code per field.
- Build-time configuration that emits typed accessor functions instead of map lookups.

**Complications worth flagging:**

- **Hygiene.** Variable names used by the generated code might clash with the surrounding scope. Either:
  - The macro must use uniquified names (closure inserts `__hyg_<n>`-style prefixes).
  - The rewriter alpha-renames bindings inside the spliced code so they don't collide.
- **Scoping.** Inlined code sees the call site's scope (local vars, imports, etc). Variables in the call site that the generated code references need to be valid at the splice point. Static check + diagnostics at rewrite time.
- **Diagnostic mapping.** Compile errors in the generated code need to map back to the closure's source lines via `//line` directives, or users get unactionable error messages pointing at the synthesised splice.
- **Parsing fragments.** Returning an expression vs a statement vs a declaration changes what the rewriter splices. Either provide multiple flavours (`q.AtCompileTimeExpr`, `q.AtCompileTimeStmt`, `q.AtCompileTimeDecl`) or auto-detect based on the closure's R.
- **Composition with regular q.AtCompileTime.** A code-generating closure might depend on a value-returning closure's output (e.g., the macro generates a switch over enum constants computed by an earlier AtCompileTime). The synthesis pass already does cross-call captures + topo-sort, so this composes — but the dependent macro would need to read the constant value at preprocessor time and stitch its source-text accordingly.

**Variant idea: full-function generation.** Instead of a func variable initialiser, allow `q.GenerateFunc(target, recipe)` at file scope:

```go
var _ = q.GenerateFunc("greet", func() string {
    return `func greet(name string) string { return "Hello, " + name }`
})

// Elsewhere — the function exists, type-checked, callable:
greet("world")
```

Reaches into Zig territory: the user's package gains real declarations created by preprocessor-time computation. Same mechanism as `q.GenStringer` (file-synthesis pass) but the contents come from a closure rather than a fixed template.

**Decision:** park as Phase 4 in this plan. Phase 1-3 is plenty of complexity to ship first; Phase 4 lights up after the value-returning core is solid and the value-vs-code split is well-understood by users.

### Phase 4.2 — toolexec passthrough so q.* works at preprocessor time

Phase 3 plans to invoke the comptime subprocess with `-toolexec=q` so closure bodies can use `q.Try` / `q.Match` / etc. The remaining piece is **passing the parent's compile flags into the comptime subprocess**, not just `-toolexec`. Without this, the comptime program might be compiled differently from how the user's actual build is configured (different build tags, different `-mod` mode, different `GOOS`/`GOARCH` target ABI tweaks).

Concretely, the synthesis pass should construct the subprocess command as:

```go
cmd := exec.Command("go", "run", "-toolexec="+qBinPath, "./.q-comptime-<hash>")
cmd.Dir = modRoot
cmd.Env = inheritedEnv(parent)  // GOOS, GOARCH, GOMODCACHE, GOFLAGS sans toolexec
```

**Why flag-passthrough unlocks recursive comptime:** with `-toolexec=q` set on the comptime subprocess, q.* calls in closure bodies get rewritten before the subprocess compiles them. That includes recursive `q.AtCompileTime` calls inside closure bodies — the inner AtCompileTime's closure runs in *its own* sub-subprocess, which (transitively) inherits the same flag set, all the way down. Each recursion level adds one subprocess invocation; cycle detection prevents infinite recursion (the existing topo-sort doesn't catch cross-package cycles, so we'd need a per-recursion-level visited set).

**Why parked, not Phase 3:** the simpler form of Phase 3 is "allow q.* in closure bodies but no recursive comptime". That's cheaper to ship and covers the 80% case (using q.Match / q.F / q.Snake inside an AtCompileTime closure). Recursive comptime is a 20% case that needs more careful flag handling and cycle detection. Once Phase 3 lands and someone asks for recursion, the Phase 4.2 work is small.

## Why this matters

q.AtCompileTime is the one feature where every other macro is a special case:
- q.F → `q.AtCompileTime(func() string { return fmt.Sprintf(...) })`
- q.Snake → `q.AtCompileTime(func() string { return toSnake(s) })`
- q.SQL → `q.AtCompileTime(func() SQLQuery { return parseSQL(...) })`
- q.Match resolution → `q.AtCompileTime(func() string { return resolveMatch(...) })` (in spirit)

Once we ship it cleanly, future "I want a compile-time helper that does X" requests reduce to "wrap X in `q.AtCompileTime`." That's the unification this aims for.
