# `either.Either` — Scala-flavoured 2-arm sum type

`either.Either[L, R]` is the discriminated union with exactly two
variants — by convention the **left arm** carries the failure /
alternative case and the **right arm** carries the success / primary
case. Right-biased operations (`Map`, `FlatMap`) reflect the convention.

Structurally it's a 2-arm `q.OneOf2` with named arms; the integration
points are identical (`q.Match` + `q.OnType`, `q.Exhaustive` type
switch on `.Value`). `either` lives in its own subpackage —
`pkg/q/either` — because the monadic helpers (`Map`, `FlatMap`, `Fold`)
collide with the slice/map versions in the main `q` package.

```go
import (
    "github.com/GiGurra/q/pkg/q"
    "github.com/GiGurra/q/pkg/q/either"
)

type Result = either.Either[Error, Response]

func process(req Request) Result {
    if !req.Valid() {
        return either.Left[Error, Response](Error{Code: 400, Msg: "bad input"})
    }
    return either.Right[Error, Response](Response{Body: "ok"})
}

// Scala-style fold:
desc := either.Fold(r,
    func(e Error) string    { return e.Msg },
    func(r Response) string { return r.Body },
)

// q.Match + q.OnType integration:
desc = q.Match(r,
    q.OnType(func(e Error) string    { return e.Msg }),
    q.OnType(func(r Response) string { return r.Body }),
)

// Statement-level type switch with q.Exhaustive coverage:
switch v := q.Exhaustive(r.Value).(type) {
case Error:    handleErr(v)
case Response: handleOk(v)
}
```

## Surface

```go
type Either[L, R any] struct {
    Tag   uint8  // 1 = Left, 2 = Right
    Value any
}

// Constructors:
func Left[L, R any](l L) Either[L, R]
func Right[L, R any](r R) Either[L, R]
func AsEither[T any](v any) T            // single-type-arg form, value's type → tag

// Predicates:
func (e Either[L, R]) IsLeft() bool
func (e Either[L, R]) IsRight() bool

// Comma-ok extractors:
func (e Either[L, R]) LeftOk() (L, bool)
func (e Either[L, R]) RightOk() (R, bool)

// Top-level monadic helpers (Go disallows method type-params, so
// these introduce R2 / L2 / T at the function level):
func Fold[L, R, T any](e Either[L, R], onLeft func(L) T, onRight func(R) T) T
func Map[L, R, R2 any](e Either[L, R], f func(R) R2) Either[L, R2]
func FlatMap[L, R, R2 any](e Either[L, R], f func(R) Either[L, R2]) Either[L, R2]
func MapLeft[L, R, L2 any](e Either[L, R], f func(L) L2) Either[L2, R]
func GetOrElse[L, R any](e Either[L, R], fallback R) R
func Swap[L, R any](e Either[L, R]) Either[R, L]
```

## Three ways to construct

```go
type Result = either.Either[Error, Response]

// (1) Named constructors — Scala-flavored, both type params explicit:
r := either.Right[Error, Response](Response{...})
e := either.Left[Error, Response](Error{...})

// (2) AsEither — single type-arg, value's type drives the tag:
r := either.AsEither[Result](Response{...})    // tag = 2 (Right)
e := either.AsEither[Result](Error{...})       // tag = 1 (Left)

// (3) q.AsOneOf — Either is structurally a OneOf2, so this works too:
r := q.AsOneOf[Result](Response{...})
```

`AsEither` is the most concise when a named alias exists. `Left` /
`Right` read more clearly when the surrounding code is in the
"return a result" idiom. `q.AsOneOf` is the unified catch-all if the
caller is already mixing OneOf families.

## Right-biased operations

`Map`, `FlatMap`, and `GetOrElse` operate on the right arm. A left
value passes through unchanged. This is the Scala convention — the
right arm is "right" as in "correct/primary".

```go
result := process(req)

// Length of the response body, or pass through the error:
mapped := either.Map(result, func(r Response) int { return len(r.Body) })

// Chain another fallible step:
chained := either.FlatMap(result, func(r Response) either.Either[Error, Decoded] {
    return decode(r.Body)
})

// Default value when there's no Response:
body := either.GetOrElse(result, Response{Body: "default"})
```

For symmetry, `MapLeft` operates on the left arm. `Swap` flips the
arms (useful when reusing a right-biased pipeline on the left).

## Coverage with `q.Exhaustive`

```go
switch v := q.Exhaustive(r.Value).(type) {
case Error:    log.Error(v)
case Response: send(v)
}
```

The build fails if either case is missing. `default:` opts out of
the missing-case rule but doesn't substitute for the declared
variants — same semantics as the const-enum form. See
[`q.Exhaustive`](exhaustive.md) for the rules.

## Direct construction is unsafe

`Tag` and `Value` are exported so the q preprocessor can construct
instances at user call sites. Direct construction skips arm
validation:

```go
e := either.Either[Error, Response]{Tag: 9, Value: 42}  // well-typed but malformed
```

Always go through `Left` / `Right` / `AsEither`.

## Nested-sum dispatch

Either values nest cleanly with `q.OneOfN`. When `L` and `R` are
themselves sums, `q.Match` arms can target the LEAF variants
directly — see [`q.OneOfN` nested-sum dispatch](oneof.md#nested-sum-dispatch-leaf-flattening)
for the semantics. Example:

```go
type ErrSet q.OneOf2[NotFound, Forbidden]
type OkSet  q.OneOf2[Created, Updated]
type Result = either.Either[ErrSet, OkSet]

desc := q.Match(result,
    q.OnType(func(NotFound) string  { return "404" }),
    q.OnType(func(Forbidden) string { return "403" }),
    q.OnType(func(Created) string   { return "201" }),
    q.OnType(func(Updated) string   { return "200" }),
)
```

Coverage is enforced over the flat leaf set; `q.Default` opts out.

## Use it for actor-style message returns

The shape that motivates Either:

```go
// Producer (could be a goroutine handling a request):
func handle(req Request) either.Either[Error, Response] {
    if err := validate(req); err != nil {
        return either.Left[Error, Response](Error{Code: 400, Msg: err.Error()})
    }
    return either.Right[Error, Response](Response{...})
}

// Consumer:
result := handle(req)
either.Fold(result,
    func(e Error) {
        metrics.IncErrors(e.Code)
        respond(e.Code, e.Msg)
    },
    func(r Response) {
        metrics.IncOK()
        respond(200, r.Body)
    },
)
```

Compared to a plain `(Response, error)` return:

- The result is **a single value** that flows through channels,
  collections, and other monadic helpers without splitting / re-pairing.
- `Map`/`FlatMap` chain side-effect-free transformations on the
  right arm without per-step `if err != nil`.
- `q.Exhaustive` enforces both arms are handled at compile time —
  Go's `(T, error)` shape doesn't track that.

## Caveats

- **L and R must be type-distinct.** `either.Either[int, int]` is
  rejected at build time — variant dispatch couldn't disambiguate.
- **The runtime cost per construction is one `any` interface box**
  (the wrapped variant value) plus the `uint8` tag. Comparable to
  `q.OneOf2`.
- **Methods cannot introduce new type parameters in Go**, so the
  monadic operations live as top-level functions rather than methods.
  `e.Map(f)` would have been nicer; `either.Map(e, f)` is the
  Go-idiomatic shape.

## See also

- [`q.OneOfN`](oneof.md) — N-arm sum types. Either is structurally
  a 2-arm OneOf with named arms and Scala-flavored helpers.
- [`q.Match`](match.md) — `q.OnType` arms work on Either values
  (Either is recognised as a 2-arm sum by the typecheck pass).
- [`q.Exhaustive`](exhaustive.md) — coverage on the type switch
  over `.Value`.
