# Goroutine ID: `q.GoroutineID`

Returns the runtime-internal goroutine ID — the integer shown in panic
stack traces (`goroutine 17 [running]:`) and in the `goroutine` pprof
profile. Go deliberately hides this from the `runtime` public API; q
exposes it via toolexec injection.

## Signature

```go
func GoroutineID() uint64
```

Type matches the runtime's own `g.goid` field. Stable for the lifetime
of the goroutine.

## How it works

q's preprocessor handles three kinds of compile invocations:

1. `pkg/q` — append the existing `_q_atCompileTime` link-gate companion file.
2. `runtime` — append a synthesized companion file that exports a
   tiny accessor for `g.goid`:

   ```go
   package runtime
   import _ "unsafe"
   //go:linkname GoroutineID
   func GoroutineID() uint64 { return getg().goid }
   ```

   The single-arg `//go:linkname` directive marks the symbol as
   intentionally externally-linkable, satisfying Go 1.23+'s rule that
   third-party `linkname` pulls into the runtime package can only
   target symbols runtime has explicitly declared as such.
3. Every other (user) package — the standard scan + rewrite pass.

`pkg/q.GoroutineID` is then a one-liner that `//go:linkname`-pulls
the injected runtime symbol:

```go
//go:linkname runtimeGoroutineID runtime.GoroutineID
func runtimeGoroutineID() uint64

func GoroutineID() uint64 { return runtimeGoroutineID() }
```

Call cost: one inlined function call returning a struct field — about
**1 ns**. No stack walk, no pprof labels dependency, no assembly.

## Usage

```go
id := q.GoroutineID()
slog.Info("processing", q.SlogAttr(id))
```

Pairs naturally with `q.SlogAttr` for log correlation when ctx-aware
logging isn't an option.

## Goroutine inheritance

`q.GoroutineID` returns the *current* goroutine's ID. Child goroutines
get their own IDs — there's no inheritance. If you need an identity
that flows across `go fn()` boundaries, use ctx propagation
(`q.SlogCtx`) or the pprof labels mechanism directly.

## Caveats

- **Hack territory.** This works by injecting an exported function
  into the standard library's `runtime` package compile and using
  `//go:linkname` to reach it. It's exactly the kind of thing Go's
  team has been tightening down on (the Go 1.23+ linkname restrictions
  are why the single-arg directive is needed). A future Go release
  could close this loophole. The fallback would be parsing
  `runtime.Stack()` output (~1 μs/call, pure public API).
- **Cache discipline matters.** A `runtime.a` archive built without
  `-toolexec=q` doesn't have `runtime.GoroutineID`. If that archive
  ends up in a `-toolexec=q` build's cache, the link fails with
  "relocation target runtime.GoroutineID not defined". Use a separate
  `GOCACHE` for toolexec builds, same discipline as for `pkg/q` itself.
- **Cross-compilation.** Works automatically — the injected file has
  no build tags, so every `GOOS/GOARCH` matrix entry rebuilds runtime
  with it.

## See also

- [`q.SlogAttr` / `q.SlogCtx`](slog.md) — ctx-aware log correlation;
  preferred over goroutine-ID-as-correlation when you have a context.
