# `q.At` — nested-nil safe traversal

Optional-chaining for Go. `q.At(<expr>)` opens a chain that walks a
selector expression, nil-guards every nilable hop, and falls through
to one or more alternative paths before resorting to a literal
fallback or the zero value of `T`.

```go
theme := q.At(user.Profile.Settings.Theme).
    OrElse(user.Defaults.Settings.Theme).
    Or("light")
```

## Surface (v1)

```go
func At[T any](expr T) PathChain[T]

func (PathChain[T]) OrElse(alt T) PathChain[T] // chain another path / value
func (PathChain[T]) Or(fallback T) T            // terminal: literal/expr fallback
func (PathChain[T]) OrZero() T               // terminal: zero value of T
```

The argument to `q.At` and to each `.OrElse` is any expression. The
common case is a selector chain (`a.b.c.d`); a single identifier or a
function-call result also works.

## What the rewriter does

For each path the preprocessor walks the selector chain at compile
time, asking go/types for the static type at every hop. Pointer-,
interface-, map-, slice-, channel-, and func-typed hops get a nil
guard; value-typed hops are bound but pass through.

The chain rewrites to an IIFE in which every path lives in its own
one-iteration `for { … break … }` block. A nil hop breaks out to the
next path's loop (or the terminal); a non-nil leaf returns from the
IIFE. Example:

```go
v := q.At(user.Profile.Settings.Theme).
    OrElse(user.Defaults.Settings.Theme).
    Or("light")
```

rewrites (approximately) to:

```go
v := func() string {
    for {
        _qAt0_0 := user;        if _qAt0_0 == nil { break }
        _qAt0_1 := _qAt0_0.Profile;  if _qAt0_1 == nil { break }
        _qAt0_2 := _qAt0_1.Settings; if _qAt0_2 == nil { break }
        return _qAt0_2.Theme
    }
    for {
        _qAt1_0 := user;            if _qAt1_0 == nil { break }
        _qAt1_1 := _qAt1_0.Defaults; if _qAt1_1 == nil { break }
        _qAt1_2 := _qAt1_1.Settings; if _qAt1_2 == nil { break }
        return _qAt1_2.Theme
    }
    return "light"
}()
```

## What you get

- **Nil-deref safety.** Any nilable hop along any path is guarded
  before the next selector evaluates. A nil intermediate breaks out of
  the path's loop — no panic.
- **Lazy fallback.** `.Or(fallback)` and each `.OrElse(alt)` argument
  are spliced into their own arms of the IIFE; their expressions only
  evaluate when reached.
- **Single-eval per path.** Every hop is bound to a fresh local
  exactly once, so a method call embedded in the chain runs at most
  once per path.
- **No runtime overhead.** One closure call per IIFE; Go's escape
  analysis usually inlines it.

## Examples

```go
// Simple fallback:
display := q.At(user.Profile.DisplayName).Or("anonymous")

// Multiple fallback paths:
endpoint := q.At(opts.Endpoint).
    OrElse(env.Endpoint).
    OrElse(globalConfig.DefaultEndpoint).
    Or("https://example.com")

// Zero-value terminal — ".OrZero()" returns T's zero value:
name := q.At(user.Profile.DisplayName).OrZero()  // "" if anything is nil

// .OrElse arg can be any expression — selector chain OR plain value:
maxConn := q.At(cfg.DB.MaxConn).OrElse(loadDefault()).Or(10)

// Nested method call as the root — single-eval applies, the call
// runs at most once per path:
v := q.At(getUser().Profile.Settings.Theme).Or("light")
```

## Caveats

- **Selector chains only get per-hop guards.** When `.OrElse(<expr>)`
  is given something other than a selector chain (a literal, a
  function call result, an arbitrary expression), the rewriter
  evaluates it once and uses the value as-is — there's no per-hop
  walking because there are no hops to walk.
- **Interface nil vs typed-nil.** `(*T)(nil)` boxed in an interface
  fails plain `== nil`. The rewriter currently emits `== nil` for
  interface hops, which catches the common bare-nil case but not the
  typed-nil-in-interface case. Use a typed pointer hop when you need
  the latter.
- **Slice / map / chan element access.** Today's MVP recognises only
  selector hops (`a.b`). Map and slice index hops (`m["k"]`, `s[i]`)
  fall back to single-evaluation as ordinary expressions; if you need
  per-hop guards on those, wrap with an explicit comma-ok check or
  use `q.Tern` for the bounds branch.

## See also

- [`q.NotNil`](notnil.md) — single-pointer nil-bubble; `q.At` is the
  nested chain variant. Reach for `q.At` when you have *more than one
  hop* to traverse — it's both safer (nil-guards every intermediate)
  and shorter than chained `q.NotNil` calls.
- [`q.Tern`](tern.md) — value-form binary pick; pairs naturally with
  `q.At` for fallback selection on non-selector predicates.
