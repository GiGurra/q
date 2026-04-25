# Compile-time evaluation: `q.AtCompileTime`, `q.AtCompileTimeCode`

Two preprocessor primitives. Both make compile-time evaluation **explicit at the call site**:

- `q.AtCompileTime[R]` — run a closure at build time, splice the *value* it returns.
- `q.AtCompileTimeCode[R]` — run a closure at build time, splice the *Go source* it returns.

## Signatures

```go
// Splice a value computed at build time.
func AtCompileTime[R any](fn func() R, codec ...Codec[R]) R

// Splice Go source generated at build time. The string must parse as a Go expression.
func AtCompileTimeCode[R any](fn func() string) R
```

The closure must be a function literal (`func() R { ... }`) taking zero parameters. `R` must be concrete.

## `q.AtCompileTime` — splice a value

### A constant

```go
days := q.AtCompileTime[int](func() int { return 7 * 24 * 60 * 60 })
// days := 604800
```

### A hash digest

```go
import "crypto/md5"
import "encoding/hex"

hash := q.AtCompileTime[string](func() string {
    sum := md5.Sum([]byte("hello"))
    return hex.EncodeToString(sum[:])
})
// hash := "5d41402abc4b2a76b9719d911017c592" — no runtime md5 cost.
```

### A list

```go
primes := q.AtCompileTime[[]int](func() []int {
    out := []int{}
    for n := 2; len(out) < 10; n++ {
        prime := true
        for _, p := range out {
            if p*p > n { break }
            if n%p == 0 { prime = false; break }
        }
        if prime { out = append(out, n) }
    }
    return out
})
// primes := []int{2, 3, 5, 7, 11, 13, 17, 19, 23, 29}
```

### A lookup table

```go
crc8 := q.AtCompileTime[[256]uint8](func() [256]uint8 {
    var t [256]uint8
    for i := range t {
        c := uint8(i)
        for b := 0; b < 8; b++ {
            if c&0x80 != 0 {
                c = (c << 1) ^ 0x07
            } else {
                c <<= 1
            }
        }
        t[i] = c
    }
    return t
})
// 256 bytes baked into the binary.
```

### A struct from a config file

```go
type Config struct {
    Name string
    Port int
}

cfg := q.AtCompileTime[Config](func() Config {
    data, _ := os.ReadFile("./config.json")
    var c Config
    json.Unmarshal(data, &c)
    return c
})
// Parsing happens during build; runtime sees the populated struct.
```

### Reusing helpers from another package

```go
// utils/fib.go
package utils

func Fib(n int) int {
    if n < 2 { return n }
    return Fib(n-1) + Fib(n-2)
}
```

```go
// main.go
import "yourmod/utils"

fib10 := q.AtCompileTime[int](func() int { return utils.Fib(10) })
// fib10 := 55
```

The synthesis subprocess imports your module, so any non-`main` subpackage is callable.

### Package-level var

```go
package config

import "github.com/GiGurra/q/pkg/q"

var Default = q.AtCompileTime[Config](func() Config {
    return parseFile("./defaults.json")
})
```

Importing packages just read `config.Default`. No `init()` cost — the value is the var's initialiser.

### Capturing earlier `q.AtCompileTime` results

```go
var (
    Greeting = q.AtCompileTime[string](func() string { return "Hello" })
    Farewell = q.AtCompileTime[string](func() string { return "Goodbye" })
    Banner   = q.AtCompileTime[string](func() string {
        return Greeting + " / " + Farewell
    })
)
// Banner := "Hello / Goodbye"
```

The synthesis pass topo-sorts so each captured value is in scope when its dependent runs.

## `q.AtCompileTimeCode` — splice generated Go

The closure returns Go source as a string. The preprocessor parses the string as a single Go expression and splices it at the call site.

### A function value

```go
greet := q.AtCompileTimeCode[func(string) string](func() string {
    return `func(name string) string { return "Hi, " + name }`
})
greet("alice") // "Hi, alice"
```

### A switch built from a list

```go
isAllowed := q.AtCompileTimeCode[func(string) bool](func() string {
    var b strings.Builder
    b.WriteString("func(s string) bool { switch s {\n")
    for _, n := range []string{"alice", "bob", "carol"} {
        fmt.Fprintf(&b, "case %q: return true\n", n)
    }
    b.WriteString("}; return false }")
    return b.String()
})
isAllowed("alice") // true
isAllowed("dave")  // false
```

The switch lives in the binary; no runtime list traversal.

### A constant string

```go
tag := q.AtCompileTimeCode[string](func() string {
    parts := []string{"prod", "edge", "v2"}
    return fmt.Sprintf("%q", strings.Join(parts, "-"))
})
// tag := "prod-edge-v2"
```

### Composing with a precomputed value

A codegen closure can capture a `q.AtCompileTime` value and bake it into the emitted source as literals:

```go
var Names = q.AtCompileTime[[]string](func() []string {
    return []string{"alice", "bob"}
})

var Greet = q.AtCompileTimeCode[func(int) string](func() string {
    var b strings.Builder
    b.WriteString("func(i int) string { switch i {\n")
    for i, n := range Names {
        fmt.Fprintf(&b, "case %d: return %q\n", i, "hi "+n)
    }
    b.WriteString("default: return \"\"\n} }")
    return b.String()
})
// Greet(0) → "hi alice", Greet(1) → "hi bob"
```

Topo-sort runs `Names` first; `Greet`'s closure sees `Names` as a normal Go slice.

## Codecs

`q.AtCompileTime` serialises the result to cross the build/runtime boundary. Default is `q.JSONCodec[R]`. Override with a second arg when JSON doesn't fit:

```go
// gob round-trips unexported fields (after gob.Register).
v := q.AtCompileTime[secret](func() secret { ... }, q.GobCodec[secret]())

// binary is smaller for fixed-size types.
t := q.AtCompileTime[[16]byte](func() [16]byte { ... }, q.BinaryCodec[[16]byte]())
```

You can implement `q.Codec[T]` yourself for custom encodings.

## Restrictions

- **Closure must be a function literal.** `q.AtCompileTime[R](myNamedFn)` is rejected — the synthesis pass needs the AST of the body.
- **No captures from the enclosing scope** — except other `q.AtCompileTime` LHS bindings (the cross-call capture pattern above). Any other captured local is a build error.
- **`R` must be concrete.** Generic type parameters as `R` are rejected.
- **Closures inside `package main` cannot reference same-package types/decls.** `package main` isn't importable, so the synthesis program can't qualify references back to it. Move types into a non-`main` subpackage.
- **`const X = q.AtCompileTime(...)` is rejected by Go itself.** The Go parser forbids function calls in `const` initialisers. Use a `var` or a function-body local.
- **Bubble-shape `q.*` (`q.Try` / `q.Check` / `q.Recv` / `q.Open`) is not usable inside the closure body.** The closure signature is `func() R` — no error slot to bubble to. Non-bubbling helpers (`q.Match`, `q.F`, `q.SQL`, `q.EnumValues`, `q.Fields`, …) are fine.
- **Closures should be pure.** No `time.Now()`, `os.Getenv()`, randomness, or other non-deterministic I/O. Determinism keeps Go's build cache effective and your builds reproducible.

## How it works

For each user-package compile containing `q.AtCompileTime` / `q.AtCompileTimeCode` calls, the preprocessor:

1. Collects every call site and topo-sorts by inter-call captures.
2. Synthesises one Go program containing every closure, written to `<modRoot>/.q-comptime-<hash>/main.go` (the leading `.` makes the dir invisible to `./...` walks).
3. Runs `go run -toolexec=q ./.q-comptime-<hash>`. Running inside the user's module means `go.mod`, replace directives, and module deps just work.
4. Reads the subprocess stdout — a JSON array, one entry per closure.
5. Splices each result back into the original source:
   - Primitive `R` with `JSONCodec`: emit the JSON value directly as a Go literal.
   - Anything else: emit a companion file with `func _qCtFn<N>() R { /* decode bytes */ }`, rewrite the call site to `_qCtFn<N>()`.
   - `q.AtCompileTimeCode`: take the returned string, parse it as a Go expression, splice in parens.

The synthesis subprocess inherits `-toolexec=q`, so q.* helpers (non-bubble) inside closure bodies get processed too. Recursive `q.AtCompileTime` works — the inner call creates its own `.q-comptime-<hash>/`.

**Build caching.** Go's build cache handles incremental builds — source unchanged means the synthesis subprocess doesn't re-run. Source changed means full re-expansion. Closures must be deterministic for caching to be sound; non-deterministic I/O like `time.Now()` defeats it.

## Related

- [`q.GenStringer` / `q.GenEnumJSON`](gen.md) — specialised method generators. `q.AtCompileTime` is the general escape hatch; `Gen*` are pre-baked common cases.
- [Implementation plan](../planning/atcompiletime.md) — synthesis pass, codec architecture, phase notes.
