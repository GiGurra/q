# Compile-time evaluation: `q.AtCompileTime`, `q.AtCompileTimeCode`

`q.AtCompileTime` evaluates a Go closure at preprocessor time and splices the result as a value at the call site. `q.AtCompileTimeCode` is the macro flavour — the closure returns Go SOURCE CODE that the rewriter parses + splices in place. Together they're the universal escape hatch every other compile-time helper (`q.F`, `q.Snake`, `q.SQL`, `q.Match` resolution, …) is a special case of: if you can write the computation as a pure Go function, q can run it before your program ever does.

## Signatures

```go
// Pure value: closure runs at preprocess time, R is the spliced value.
func AtCompileTime[R any](fn func() R, codec ...Codec[R]) R

// Macro: closure returns Go source that the rewriter parses + splices.
func AtCompileTimeCode[R any](fn func() string) R

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

The closure MUST be a function literal (not a named function reference). It must take zero parameters and return exactly one value. The returned value's type must match `R`.

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

## Macros — `q.AtCompileTimeCode` examples

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

## Restrictions

Enforced at compile time:

- **Placement: function bodies and package-level `var` initialisers only.** Both work — `x := q.AtCompileTime(...)` inside `func` and `var X = q.AtCompileTime(...)` at file scope are equally supported. `const X = q.AtCompileTime(...)` is rejected by the Go parser itself (Go forbids function calls in const initialisers); use `var` or a function-body local.
- **Closure must be a function literal.** `q.AtCompileTime[T](myNamedFn)` is rejected — the synthesis pass needs the AST of the body, not a value of function type.
- **No captures from the enclosing scope, EXCEPT other `q.AtCompileTime` LHS bindings.** A closure that references a local variable not bound by another `q.AtCompileTime` is rejected with a clear diagnostic.
- **R must be concrete.** Generic type parameters as `R` are rejected — the synthesis program needs a fully resolved type to emit.
- **Non-bubbling q.* only inside closure bodies.** `q.Try` / `q.Check` / `q.Recv` / `q.Open` need an error return slot, but the closure has signature `func() R` (no error). Use `q.Match`, `q.Upper`/`Snake`/`Camel`, `q.F`, `q.SQL`, `q.EnumValues`/`Names`, `q.Fields`/`AllFields`/`TypeName`/`Tag`, etc. — anything that doesn't bubble.
- **Closures inside `package main` cannot reference same-package types/decls.** `package main` isn't importable, so the synthesis program can't qualify references to it. Move the types into a non-main subpackage.
- **Pure closures only.** `time.Now()`, `os.Getenv`, random number generators, anything that produces a different result on each run — the cache key in a future caching layer assumes determinism. Document the closure as pure and don't reach for mutable I/O.

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

- [Phase plan + design](../planning/atcompiletime.md) — full implementation architecture.
- [Codec interface](../planning/atcompiletime.md#codec-based-encoding-decoding-phase-2) — when to pick JSON vs gob vs binary vs a custom codec.
- [`q.GenStringer` / `q.GenEnumJSON`](gen.md) — package-level directives that synthesize methods. q.AtCompileTime is the more general escape hatch; the Gen* directives are specialised cases.
- [`q.F`](format.md), [`q.Snake`](string_case.md), [`q.SQL`](sql.md), [`q.EnumName`](enums.md), [`q.Match`](match.md) — every other compile-time helper. Each one is, in spirit, a fixed special case of what `q.AtCompileTime` lets you write yourself.
