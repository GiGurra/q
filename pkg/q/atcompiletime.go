package q

// atcompiletime.go — surface for q.AtCompileTime.
//
// q.AtCompileTime evaluates a closure at preprocessor time and splices
// the result as a value at the call site. The runtime body panics —
// every legitimate call site is rewritten away by the preprocessor.
//
// The optional codec controls how the value crosses the boundary
// between the preprocessor-time subprocess (which runs the closure)
// and the runtime user-package (which sees the spliced value). For
// primitive types under the default JSONCodec the rewriter folds
// straight to a Go literal; for non-primitives a companion file is
// synthesized with var + init() that decodes the embedded bytes via
// the requested codec.

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
)

// AtCompileTimeCode evaluates fn at preprocessor time, takes the
// returned string as Go source code, parses it, and splices the
// parsed expression in place of the call site. This is the macro
// flavour of q.AtCompileTime — the closure returns CODE, not a value.
//
// The closure's return value MUST be a Go expression source (not a
// statement, not a declaration). The expression's type must match R.
// Same restrictions as q.AtCompileTime apply (no captures except
// other AtCompileTime LHS bindings, no recursion if it would cycle,
// closure must be a *ast.FuncLit literal).
//
// Example:
//
//	greet := q.AtCompileTimeCode[func(string) string](func() string {
//	    return `func(name string) string { return "Hello, " + name }`
//	})
//	// Rewriter splices:
//	// greet := func(name string) string { return "Hello, " + name }
//
// The generated source can only reference symbols / types / packages
// already in scope at the call site. Imports the macro needs but
// the user file lacks must be added explicitly by the user.
func AtCompileTimeCode[R any](fn func() string) R {
	panicUnrewritten("q.AtCompileTimeCode")
	var zero R
	return zero
}

// AtCompileTime evaluates fn at preprocessor time and splices the
// result as a value at the call site.
//
// Restrictions enforced at compile time:
//
//   - The argument MUST be a `*ast.FuncLit` (an inline anonymous
//     function literal). A function reference or variable holding a
//     func value is rejected.
//   - The closure must have no captures from the enclosing scope,
//     EXCEPT other q.AtCompileTime results — those are allowed and
//     resolved in dependency order via topological sort.
//   - The closure must NOT call q.* (rewriter doesn't recurse into
//     synthesized comptime programs).
//   - R must round-trip through the chosen codec.
//
// The optional codec controls value transport. Default is
// JSONCodec[R](). Other built-ins: GobCodec[R](), BinaryCodec[R]().
// Users can pass any value implementing Codec[R].
func AtCompileTime[R any](fn func() R, codec ...Codec[R]) R {
	panicUnrewritten("q.AtCompileTime")
	var zero R
	return zero
}

// Codec encodes and decodes values of type T to/from bytes. Used by
// q.AtCompileTime to transport values from the preprocessor-time
// subprocess to the runtime user package.
type Codec[T any] interface {
	Encode(v T) ([]byte, error)
	Decode(data []byte, v *T) error
}

// JSONCodec returns the JSON codec for T (the default for
// q.AtCompileTime). Lossy on unexported fields — use GobCodec for
// those.
func JSONCodec[T any]() Codec[T] { return jsonCodec[T]{} }

// GobCodec returns the gob codec for T. Handles unexported fields when
// the type is registered with gob.Register; output is larger than JSON
// but more flexible.
func GobCodec[T any]() Codec[T] { return gobCodec[T]{} }

// BinaryCodec returns the encoding/binary codec for T. Fixed-size
// types only (no slices, maps, strings) — produces the smallest
// possible output.
func BinaryCodec[T any]() Codec[T] { return binaryCodec[T]{} }

type jsonCodec[T any] struct{}

func (jsonCodec[T]) Encode(v T) ([]byte, error)         { return json.Marshal(v) }
func (jsonCodec[T]) Decode(data []byte, v *T) error     { return json.Unmarshal(data, v) }

type gobCodec[T any] struct{}

func (gobCodec[T]) Encode(v T) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
func (gobCodec[T]) Decode(data []byte, v *T) error {
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}

type binaryCodec[T any] struct{}

func (binaryCodec[T]) Encode(v T) ([]byte, error) {
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
func (binaryCodec[T]) Decode(data []byte, v *T) error {
	return binary.Read(bytes.NewReader(data), binary.LittleEndian, v)
}
