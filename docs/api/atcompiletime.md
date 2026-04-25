# Compile-time evaluation: `q.AtCompileTime`, `q.AtCompileTimeCode`, `q.Comptime`

q ships three compile-time evaluation helpers, each with a different shape:

| Helper | Surface | Closure shape | Result | Use when |
|---|---|---|---|---|
| `q.AtCompileTime[R](fn func() R)` | one-shot value | zero params, returns `R` | `R` value spliced at call site | computing a single comptime value (a hash, a table, a parsed config) |
| `q.AtCompileTimeCode[R](fn func() string)` | macro / codegen | zero params, returns Go source | parsed Go expression spliced at call site | generating function values, switch statements, struct literals from build-time data |
| `q.Comptime[F any](impl F)` | reusable fn value | a function with **N args** that recursively calls itself | a Go fn value; every call to it folds at preprocess time | calling the same comptime computation from many sites with different args (recursive math, codegen-per-input) |

Together they're the universal escape hatch every other compile-time helper (`q.F`, `q.Snake`, `q.SQL`, `q.Match` resolution, …) is a special case of: if you can write the computation as a pure Go function, q can run it before your program ever does.

## Signatures

```go
// Pure value: closure runs at preprocess time, R is the spliced value.
func AtCompileTime[R any](fn func() R, codec ...Codec[R]) R

// Macro: closure returns Go source that the rewriter parses + splices.
func AtCompileTimeCode[R any](fn func() string) R

// Comptime function value — calls to the returned fn fold at preprocess time.
func Comptime[F any](impl F) F

// Codec interface — controls how non-primitive values cross the
// preprocessor → runtime boundary.
type Codec[T any] interface {
    Encode(v T) ([]byte, error)
    Decode(data []byte, v *T) error
}

// Built-in codecs:
func JSONCodec[T any]() Codec[T]   // default — encoding/json
func GobCodec[T any]() Codec[T]    // encoding/gob; handles unexported fields
func BinaryCodec[T any]() Codec[T] // encoding/binary; fixed-size types only
```

`q.AtCompileTime` / `q.AtCompileTimeCode` closures must be a function literal (not a named function reference), take zero parameters and return exactly one value matching `R`.

`q.Comptime` accepts any function-typed argument (`F` resolves to that function type via type inference), and the impl may take any number of args. The function value it returns IS the impl at runtime, but every LEXICAL call site of the returned fn (with literal / package-const / cross-comptime args) is rewritten to a value spliced at compile time.

## Where you can use it

Both `q.AtCompileTime` and `q.AtCompileTimeCode` work in **two placements**:

1. **Function bodies** — local variables, return positions, expression context. The result becomes a regular Go expression at the call site.
2. **Package-level `var` initialisers** — a `var X = q.AtCompileTime(...)` at file scope makes the comptime-computed value the initialiser of a package-level variable. For non-primitive `R`, the generated companion file uses a function-call form (`var X = _qCtFn0()`) so the decoded value is available at var-init time, before any `init()` runs.

Both placements support `q.AtCompileTimeCode`, so you can pre-build entire function values as package-level vars too.

What's NOT supported: package-level `const` declarations. Go forbids function calls in `const` initialisers regardless of whether they fold to a literal — `const X = q.AtCompileTime(...)` won't parse. Use a `var` or a function-body local.

## Constant pregeneration — `q.AtCompileTime` examples

### Inside a function — local compile-time constants

```go
func main() {
    // Compile-time hash — md5 runs at build time, the digest hex
    // literal is embedded as a Go literal in your binary.
    hash := q.AtCompileTime[string](func() string {
        sum := md5.Sum([]byte("hello, comptime"))
        return hex.EncodeToString(sum[:])
    })
    fmt.Println(hash)
    // → "6073241895e7dc207ffb924c228e3a09" — folded to a string literal
}
```

### At package level — pregenerated tables, configs, lookups

```go
package data

import "github.com/GiGurra/q/pkg/q"

// First 10 primes — sieve runs once at build time, the slice is decoded
// into Lookup before package init() runs.
var Primes = q.AtCompileTime[[]int](func() []int {
    out := []int{}
    for n := 2; len(out) < 10; n++ {
        isPrime := true
        for _, p := range out {
            if p*p > n { break }
            if n%p == 0 { isPrime = false; break }
        }
        if isPrime { out = append(out, n) }
    }
    return out
})

// Pregenerated configuration struct.
var DefaultConfig = q.AtCompileTime[Config](func() Config {
    return Config{Name: "service-A", Port: 8080, Tags: []string{"prod", "edge"}}
})
```

Other packages just `import "<modpath>/data"` and read `data.Primes` / `data.DefaultConfig` like any other package var. There's no runtime computation cost.

### CRC table built once, baked in

```go
package crc

// 256-entry CRC8 lookup table — every entry computed at build time.
var Table = q.AtCompileTime[[256]uint8](func() [256]uint8 {
    var t [256]uint8
    for i := range t {
        crc := uint8(i)
        for b := 0; b < 8; b++ {
            if crc&0x80 != 0 {
                crc = (crc << 1) ^ 0x07 // CRC-8/CDMA2000 polynomial
            } else {
                crc <<= 1
            }
        }
        t[i] = crc
    }
    return t
})
```

### Cross-call captures

A later closure may reference an earlier `q.AtCompileTime` LHS — the synthesis pass topo-sorts so dependencies resolve in the right order:

```go
var (
    Greeting = q.AtCompileTime[string](func() string { return "Hello" })
    Farewell = q.AtCompileTime[string](func() string { return "Goodbye" })
    Banner   = q.AtCompileTime[string](func() string {
        return Greeting + " / " + Farewell // both captured at build time
    })
)
```

## Comptime function values — `q.Comptime`

`q.Comptime` lets you declare a recursive Go function whose every call site is folded to a literal at preprocess time. The shape is borrowed from Zig's `comptime` keyword: write the function as ordinary Go, mark it once with `q.Comptime`, then call it freely from anywhere in the module — each call resolves before runtime.

### The clean version of recursive comptime

The classic motivating example is recursive Fibonacci:

```go
package fib

import "github.com/GiGurra/q/pkg/q"

var Fib = q.Comptime(func(n int) int {
    if n < 2 { return n }
    return Fib(n-1) + Fib(n-2)
})
```

Then anywhere in the module:

```go
import "yourmod/fib"

func main() {
    fmt.Println(fib.Fib(10))  // → 55  (folded to the literal 55 at build time)
    fmt.Println(fib.Fib(15))  // → 610 (folded to the literal 610)
}
```

At runtime the binary holds two `fmt.Println` calls with int literals 55 and 610 — `fib.Fib` is never called at runtime; the recursion happened during the build.

### How it differs from `q.AtCompileTime`

`q.AtCompileTime` evaluates ONE closure ONCE per call site. To compute fib(5) recursively with `q.AtCompileTime` alone, you'd have to manually unroll every level (see [Recursive comptime via manual unrolling](#recursive-comptime--fibonacci-as-a-build-time-recursion) below).

`q.Comptime` takes args. Every distinct call site (`fib.Fib(10)`, `fib.Fib(15)`) is treated as its own preprocess-time invocation, but they all share the same impl source — written ONCE, at the declaration site.

The synthesis pass handles recursion by spawning ONE subprocess per call site that runs the impl as ordinary recursive Go code. So `fib.Fib(40)` becomes one `go run -toolexec=q` subprocess executing 2^40 Go recursive calls in-process. Build cost is ~3 seconds for fib(40) — dominated by Go recursion, not preprocess overhead.

> See [`q.NestedComptime`](#nestedcomptime--compiler-per-recursive-level-phase-52) below for the variant that spawns one subprocess PER recursive level (with caching, so it stays linear).

### Cross-comptime composition

A `q.Comptime` impl can call another `q.Comptime` decl:

```go
var Fact = q.Comptime(func(n int) int {
    if n < 2 { return 1 }
    return n * Fact(n-1)
})

var Power = q.Comptime(func(base, exp int) int {
    if exp == 0 { return 1 }
    return base * Power(base, exp-1)
})

var Combinatoric = q.Comptime(func(n, k int) int {
    return Fact(n) / (Fact(k) * Fact(n-k))   // calls another Comptime
})
```

The synthesis pass topo-sorts impls so each is in scope when a dependent impl runs.

### Composes with `q.AtCompileTime`

A `q.Comptime` call inside a `q.AtCompileTime` closure body is treated like any other captured comptime value:

```go
doubled := q.AtCompileTime[int](func() int {
    return Fact(5) + Fact(5)   // both Fact calls fold at preprocess time
})
// doubled folds to the literal 240
```

The synthesis pass evaluates `Fact(5)` first, substitutes the literal into the closure body, then evaluates the closure. One subprocess per `q.AtCompileTime` site.

### Restrictions

- The impl MUST be a function literal — `var Fib = q.Comptime(SomeNamedFn)` is rejected.
- The impl can recurse (call itself directly) and call other `q.Comptime` decls. It CANNOT capture other free variables from the enclosing scope.
- All args at every call site must be resolvable at preprocess time: literals, package-level constants, other `q.Comptime` / `q.AtCompileTime` values, or arithmetic combinations of those. A call with a runtime-only arg (`fib.Fib(userInput)`) cannot be folded — and so is rejected with a clear diagnostic.
- The impl runs in a stdlib + user-module subprocess like `q.AtCompileTime`. Same purity rules apply: no `time.Now()`, `os.Getenv`, or other non-deterministic I/O if you want the build to be reproducible.

### How it works

The preprocessor:

1. **Pre-pass** scans every source file in the package for `var X = q.Comptime(funcLit)` decls. Records each into a package-wide map keyed by name.
2. Per-file scan: any call expression whose `Fun` is an ident matching a comptime binding becomes a `familyComptimeCall` shape.
3. Synthesis pass emits the impl declaration once per unique binding (with self-references rewritten to a synthesised name to avoid Go's init-cycle detector — see below) and per-call lines that invoke the impl with the (resolved) args.
4. Per call site, the result is JSON-encoded by the subprocess and either folded to a literal (primitives) or extracted via a `_qCtFn<N>()` companion file (non-primitives).

**Init-cycle workaround.** A naïve rewrite of `var Fib = q.Comptime(<impl>)` into `var Fib = <impl>` would trip Go's compiler ("initialization cycle: Fib refers to itself"), because the impl body contains `Fib(n-1)` which the parser sees as a forward reference to the same package var. q dodges this with an IIFE that defers the self-binding through a local var:

```go
// Generated form:
var Fib = func() func(int) int {
    var _qfn func(int) int
    _qfn = func(n int) int {
        if n < 2 { return n }
        return _qfn(n-1) + _qfn(n-2)   // refers to the local, not the package var
    }
    return _qfn
}()
```

The same shape is also what runs inside the synthesis subprocess when a comptime impl recurses — but because the subprocess never imports the user package, there's no init cycle there either way; the IIFE wrap is purely for the user-package compile.



`q.AtCompileTimeCode` runs the closure at preprocess time and takes the returned string as Go SOURCE that the rewriter parses + splices.

### Inside a function — local generated function values

```go
func main() {
    // Generate a function literal from a string at build time.
    greet := q.AtCompileTimeCode[func(string) string](func() string {
        return `func(name string) string { return "Hello, " + name }`
    })
    fmt.Println(greet("alice")) // "Hello, alice"
}
```

### At package level — generated function values as package vars

```go
package handlers

import "github.com/GiGurra/q/pkg/q"

// Switch-based classifier built at build time; the spliced function
// literal lives at package scope, ready to call.
var Classify = q.AtCompileTimeCode[func(int) string](func() string {
    var b strings.Builder
    b.WriteString("func(n int) string {\n")
    b.WriteString("\tswitch {\n")
    b.WriteString("\tcase n < 0:    return \"negative\"\n")
    b.WriteString("\tcase n == 0:   return \"zero\"\n")
    b.WriteString("\tcase n < 10:   return \"small\"\n")
    b.WriteString("\tdefault:       return \"large\"\n")
    b.WriteString("\t}\n}")
    return b.String()
})

// Anywhere else: handlers.Classify(50) → "large"
```

### Generated code that captures a comptime value

A `q.AtCompileTimeCode` closure can reference an earlier `q.AtCompileTime` value to bake it into the generated source as literals — this is where macros and constant pregeneration compose:

```go
// First, gather the names at build time.
var Names = q.AtCompileTime[[]string](func() []string {
    return []string{"alice", "bob", "carol"}
})

// Then synthesize a switch that maps indices to greetings.
// `Names` is captured by the synthesis pass; the closure runs at
// build time AFTER Names has been resolved, so `range Names` walks
// the comptime-computed slice and emits a case per element.
var Greet = q.AtCompileTimeCode[func(int) string](func() string {
    var b strings.Builder
    b.WriteString("func(i int) string {\n\tswitch i {\n")
    for i, n := range Names {
        b.WriteString(fmt.Sprintf("\tcase %d: return %q\n", i, "hi "+n))
    }
    b.WriteString("\tdefault: return \"unknown\"\n\t}\n}")
    return b.String()
})

// Greet(1) → "hi bob" — the switch arms were built at compile time.
```

### Generated string constants

`q.AtCompileTimeCode` doesn't have to produce a function value — any Go expression works:

```go
var BuildTag = q.AtCompileTimeCode[string](func() string {
    parts := []string{"prod", "edge", "v2"}
    return fmt.Sprintf("%q", strings.Join(parts, "-"))
})
// BuildTag == "prod-edge-v2" — spliced as the string literal "prod-edge-v2"
```

## How it works

For each user-package compile that contains `q.AtCompileTime` calls, the preprocessor:

1. **Collects** every `q.AtCompileTime` / `q.AtCompileTimeCode` call site in the package.
2. **Topo-sorts** them by inter-call captures (a closure may reference another `q.AtCompileTime` LHS — those come first).
3. **Synthesizes** ONE Go program containing all closures, written to `<userModRoot>/.q-comptime-<hash>/main.go`. The leading `.` makes the Go toolchain skip the directory for `./...` walks; running inside the user's module means the subprocess inherits the user's `go.mod`, replace directives, and module dependencies for free — no separate `go.mod` synthesis.
4. **Runs** `go run -toolexec=<qBin> ./.q-comptime-<hash>`. The `-toolexec` flag means q.* calls inside closure bodies get rewritten before the subprocess compiles them. Recursive `q.AtCompileTime` (a closure containing another `q.AtCompileTime`) just works — the inner q invocation creates its OWN `.q-comptime-<hash>/`.
5. **Reads** the subprocess's stdout: a JSON array of per-call codec-encoded results.
6. **Splices**:
   - For primitive `R` + default `JSONCodec`: emit the JSON value directly as a Go literal at the call site (JSON `42` is `42` in Go, JSON `"hi"` is `"hi"`, etc).
   - For struct / slice / map `R`: emit a companion file `_q_atcomptime.go` with `func _qCtFn<N>() R { /* decode */ return v }`; the call site rewrites to `_qCtFn<N>()`. Function-call form is required so package-level user vars (`var X = q.AtCompileTime(...)`) see the decoded value at var-init time — Go runs all package-level initializers BEFORE any `init()` function, so a `var _qCtValueN R` initialised from `init()` would arrive too late.
   - For `q.AtCompileTimeCode`: take the JSON-encoded string, unquote to recover the raw Go source, splice it in parens.

After the subprocess finishes, the temp directory is deleted.

## Cross-call captures

A closure may reference another `q.AtCompileTime` LHS variable. The synthesis pass topo-sorts and rewrites the captured identifier in the synthesized program:

```go
a := q.AtCompileTime[int](func() int { return 21 })
b := q.AtCompileTime[int](func() int { return a * 2 })   // captures a
c := q.AtCompileTime[int](func() int { return a + b })   // captures both
fmt.Println(a, b, c) // 21 42 63
```

In the synthesized program, `a` becomes `_qCt0`, then `b`'s body sees `_qCt0 * 2` (rewritten), and `c` sees `_qCt0 + _qCt1`. The synthesis runs in topo order so each captured value is in scope when the dependent closure executes.

## Recursive comptime — Fibonacci as a build-time recursion

> **Modern path:** for recursive impls, reach for [`q.Comptime`](#comptime-function-values--qcomptime) instead — it gives you ordinary `var Fib = q.Comptime(func(n int) int { ... Fib(n-1) ... })` syntax, with the recursion running inside a single synthesis subprocess. The subsection below shows how to express the same thing with raw `q.AtCompileTime` for educational purposes (and as proof that the architecture composes recursively).

Because the synthesis subprocess inherits `-toolexec=q`, a `q.AtCompileTime` call INSIDE a closure body gets processed by a recursive q invocation. Each level of nesting becomes one deeper compiler-process:

```
level 0 — your `go build` invokes q on the user package.
level 1 — q's synthesis subprocess runs `go run -toolexec=q ./.q-comptime-<hash>`.
          Inside, q processes the AtCompileTime calls in that synthesized main.go.
level 2 — those calls' synthesis subprocesses recurse the same way.
…
level K — the leaves return literal values with no further nesting.
```

You can compute Fibonacci ENTIRELY at compile time by manually unrolling each level into a nested `q.AtCompileTime`:

```go
// fib(5) = fib(4) + fib(3) = 3 + 2 = 5 — every term resolved at build time.
fib5 := q.AtCompileTime[int](func() int {
    f4 := q.AtCompileTime[int](func() int {
        f3a := q.AtCompileTime[int](func() int {
            f2a := q.AtCompileTime[int](func() int {
                one  := q.AtCompileTime[int](func() int { return 1 })
                zero := q.AtCompileTime[int](func() int { return 0 })
                return one + zero
            })
            one := q.AtCompileTime[int](func() int { return 1 })
            return f2a + one
        })
        f2b := q.AtCompileTime[int](func() int {
            one  := q.AtCompileTime[int](func() int { return 1 })
            zero := q.AtCompileTime[int](func() int { return 0 })
            return one + zero
        })
        return f3a + f2b
    })
    f3 := q.AtCompileTime[int](func() int {
        f2 := q.AtCompileTime[int](func() int {
            one  := q.AtCompileTime[int](func() int { return 1 })
            zero := q.AtCompileTime[int](func() int { return 0 })
            return one + zero
        })
        one := q.AtCompileTime[int](func() int { return 1 })
        return f2 + one
    })
    return f4 + f3
})
fmt.Println(fib5) // 5
```

The shape is verbose because `q.AtCompileTime` requires a function-literal argument (no recursion via a named Go function that references itself), so each fib level has to be unrolled by hand. But what's happening at build time is wild: 5 levels of compiler-recursive `q` invocations, each one creating its own `.q-comptime-<hash>/` directory inside your module, computing its piece, and returning. Total wall-clock time on the e2e harness: ~5s for fib(5) — each level is one Go subprocess startup + a fast compile of a tiny program.

For practical purposes, when you want compile-time iteration over a value, **use cross-call captures instead** — they all run in one subprocess at level 1 and topo-sort handles ordering:

```go
fib0 := q.AtCompileTime[int](func() int { return 0 })
fib1 := q.AtCompileTime[int](func() int { return 1 })
fib2 := q.AtCompileTime[int](func() int { return fib1 + fib0 })
fib3 := q.AtCompileTime[int](func() int { return fib2 + fib1 })
fib4 := q.AtCompileTime[int](func() int { return fib3 + fib2 })
fib5 := q.AtCompileTime[int](func() int { return fib4 + fib3 })
// All six computed in ONE subprocess; topo-sort runs them in dependency order.
```

The recursive flavour is mostly a demonstration that the architecture composes; cross-call captures are usually what you reach for.

## Code generation — `q.AtCompileTimeCode`

Sometimes you don't want a value, you want CODE. `q.AtCompileTimeCode` runs the closure at preprocess time, takes the returned string as Go source, parses it, and splices the parsed expression at the call site:

```go
// Generate a switch-based function from a comptime-computed list.
classify := q.AtCompileTimeCode[func(int) string](func() string {
    var b strings.Builder
    b.WriteString("func(n int) string {\n")
    b.WriteString("\tswitch {\n")
    b.WriteString("\tcase n < 0:    return \"negative\"\n")
    b.WriteString("\tcase n == 0:   return \"zero\"\n")
    b.WriteString("\tcase n < 10:   return \"small\"\n")
    b.WriteString("\tdefault:       return \"large\"\n")
    b.WriteString("\t}\n}")
    return b.String()
})
classify(0)   // "zero"
classify(50)  // "large"
```

Composes with value-returning `q.AtCompileTime` via cross-call captures — a code-gen closure can reference values that earlier `q.AtCompileTime` calls produced, baking them into the generated source as literals:

```go
names := q.AtCompileTime[[]string](func() []string {
    return []string{"alice", "bob", "carol"}
})
greet := q.AtCompileTimeCode[func(int) string](func() string {
    var b strings.Builder
    b.WriteString("func(i int) string {\n\tswitch i {\n")
    for i, n := range names {
        b.WriteString(fmt.Sprintf("\tcase %d: return %q\n", i, "hi "+n))
    }
    b.WriteString("\tdefault: return \"unknown\"\n\t}\n}")
    return b.String()
})
greet(1)  // "hi bob" — switch arms baked at build time
```

The generated source can only reference symbols / types / packages already in scope at the call site; imports the macro needs but the user file lacks must be added explicitly by the user.

## Codecs — non-JSON and non-trivial round-trips

The default codec is `JSONCodec[R]`. Pass an alternative as the second argument when you need it:

```go
// gob handles unexported fields if you `gob.Register` them once.
type secret struct {
    Public string
    hidden int  // unexported — JSON would lose this
}

s := q.AtCompileTime[secret](func() secret {
    return secret{Public: "ok", hidden: 42}
}, q.GobCodec[secret]())
```

For fixed-size types (no slices, maps, or strings), `BinaryCodec[T]` produces the smallest output. You can also implement `Codec[T]` yourself for custom encodings.

<a id="nestedcomptime--compiler-per-recursive-level-phase-52"></a>
## Forward-look — `q.NestedComptime`, partial evaluator, multipass

Three follow-on capabilities are designed and tracked in [`docs/planning/comptime-inception.md`](../planning/comptime-inception.md):

**`q.NestedComptime[F any](impl F) F` — compiler-per-recursive-level + cache.** Same surface as `q.Comptime`, but each recursive call inside the impl spawns its OWN `go run -toolexec=q` subprocess (gated by an on-disk hash-keyed cache living at `$GOCACHE/q-comptime/`). For `Fib(40)` that means 40 unique subprocess invocations instead of 2^40, kept tractable by the cache. The motivation is "true inception comptime" — the compiler literally calls itself per recursive level, each level seeing only its own subproblem. Useful when impls do real work (heavy parsing, code synthesis), so the cache savings pay for the subprocess startup cost.

**Compile-time partial evaluator (Path B).** A complementary mode where `q.Comptime` calls with literal args get **unfolded inline at preprocess time**: substitute the literal, constant-fold conditionals, recurse on surviving call sites, emit the constant. No subprocess at all — all the work happens in the rewriter. Bounded to a Go subset (parameter substitution, if/else, arithmetic, comparisons, recursive calls); rich impls fall back to the subprocess path. The win is build speed for tight numeric impls.

**Multipass code generation (Path D).** `q.AtCompileTimeCode` already returns source that the rewriter parses. The next step is letting that returned source itself contain more `q.*` comptime calls — the rewriter detects them, runs another scan+rewrite pass on the spliced fragment, and repeats until a fixed point. Macros that emit macros that emit values; useful for code-gen pipelines where one stage produces sources for the next.

For now (Phase 5.1) you have `q.Comptime` for clean recursive impls in one subprocess, plus the underlying `q.AtCompileTime` / `q.AtCompileTimeCode` primitives for one-shot value / code-gen splicing.

## Restrictions

Enforced at compile time:

- **Placement: function bodies and package-level `var` initialisers only.** Both work — `x := q.AtCompileTime(...)` inside `func` and `var X = q.AtCompileTime(...)` at file scope are equally supported. `const X = q.AtCompileTime(...)` is rejected by the Go parser itself (Go forbids function calls in const initialisers); use `var` or a function-body local.
- **Closure must be a function literal.** `q.AtCompileTime[T](myNamedFn)` is rejected — the synthesis pass needs the AST of the body, not a value of function type.
- **No captures from the enclosing scope, EXCEPT other `q.AtCompileTime` LHS bindings.** A closure that references a local variable not bound by another `q.AtCompileTime` is rejected with a clear diagnostic.
- **R must be concrete.** Generic type parameters as `R` are rejected — the synthesis program needs a fully resolved type to emit.
- **Non-bubbling q.* only inside closure bodies.** `q.Try` / `q.Check` / `q.Recv` / `q.Open` need an error return slot, but the closure has signature `func() R` (no error). Use `q.Match`, `q.Upper`/`Snake`/`Camel`, `q.F`, `q.SQL`, `q.EnumValues`/`Names`, `q.Fields`/`AllFields`/`TypeName`/`Tag`, etc. — anything that doesn't bubble.
- **Closures inside `package main` cannot reference same-package types/decls.** `package main` isn't importable, so the synthesis program can't qualify references to it. Move the types into a non-main subpackage.
- **Pure closures only.** `time.Now()`, `os.Getenv`, random number generators, anything that produces a different result on each run — the cache key in a future caching layer assumes determinism. Document the closure as pure and don't reach for mutable I/O.

For `q.Comptime` impls specifically:

- **Args at every call site must be preprocess-time-resolvable.** Literals, package-level constants, other `q.AtCompileTime` / `q.Comptime` outputs, or arithmetic combinations of those. A call with a runtime-only arg (e.g. `Fib(userInput)`) is rejected — the rewriter can't fold what isn't known at build time.
- **Impl is a function literal, not a named function reference.** `var Fib = q.Comptime(SomeNamedFn)` is rejected — the synthesis pass needs the AST of the body.
- **Same-package call sites only need the impl name in scope.** Cross-package call sites (`other.Fib(10)`) work the same way as local ones — they resolve through the importing package's view of the comptime binding.

## Common patterns

**Compile-time hash / digest:**
```go
const password = "secret"
hash := q.AtCompileTime[string](func() string {
    sum := sha256.Sum256([]byte(password))
    return hex.EncodeToString(sum[:])
})
```

**Compile-time lookup tables:**
```go
crc8Table := q.AtCompileTime[[256]uint8](func() [256]uint8 {
    var t [256]uint8
    for i := range t { /* ... */ }
    return t
})
```

**Compile-time config / schema loading:** read a JSON / YAML file at preprocess time, parse it, embed the parsed struct.

**Code generation per type:** compose `q.AtCompileTimeCode` with `q.Fields[T]()` to emit a per-struct marshaller without runtime reflection.

## What gets generated

For a primitive case, the call site folds to a Go literal:

```go
hash := q.AtCompileTime[string](func() string { return md5sum("x") })
// Rewritten:
hash := "6073241895e7dc207ffb924c228e3a09"
```

For a non-primitive case, the call site rewrites to a function call, and the rewriter adds a companion file at the package level:

```go
// User code:
cfg := q.AtCompileTime[Config](func() Config { return Config{Port: 8080} })

// Rewritten user code:
cfg := _qCtFn0()

// Companion file (_q_atcomptime.go) added to the compile:
func _qCtFn0() Config {
    var v Config
    data := []byte("{\"Port\":8080}")
    if err := q.JSONCodec[Config]().Decode(data, &v); err != nil {
        panic("q.AtCompileTime decode failed: " + err.Error())
    }
    return v
}
```

For `q.AtCompileTimeCode`, the call site folds to the parsed expression in parens:

```go
greet := q.AtCompileTimeCode[func(string) string](func() string {
    return `func(name string) string { return "Hello, " + name }`
})
// Rewritten:
greet := (func(name string) string { return "Hello, " + name })
```

## Related

- [Phase plan + design (1-4)](../planning/atcompiletime.md) — original implementation architecture for `q.AtCompileTime` / `q.AtCompileTimeCode`.
- [Comptime inception design (5+)](../planning/comptime-inception.md) — `q.Comptime`, `q.NestedComptime`, partial evaluator, multipass macros.
- [Codec interface](../planning/atcompiletime.md#codec-based-encoding-decoding-phase-2) — when to pick JSON vs gob vs binary vs a custom codec.
- [`q.GenStringer` / `q.GenEnumJSON`](gen.md) — package-level directives that synthesize methods. q.AtCompileTime is the more general escape hatch; the Gen* directives are specialised cases.
- [`q.F`](format.md), [`q.Snake`](string_case.md), [`q.SQL`](sql.md), [`q.EnumName`](enums.md), [`q.Match`](match.md) — every other compile-time helper. Each one is, in spirit, a fixed special case of what `q.AtCompileTime` lets you write yourself.
