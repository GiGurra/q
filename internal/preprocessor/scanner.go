package preprocessor

// scanner.go — recognise q.* call expressions in user-package source
// files.
//
// Recognised shapes (all in statement-position inside a function body):
//
//	v := q.Try(<inner-call>)                              [Family=Try, no Method]
//	v := q.TryE(<inner-call>).<Method>(<args>...)         [Family=TryE]
//
// where <Method> is one of Err, ErrF, Catch, Wrap, Wrapf. The LHS is
// always a single identifier and the operator is the short-var-decl
// `:=`. Discard form, plain `=` assignment, multi-LHS, and the whole
// q.NotNil / q.NotNilE family are out of scope for now — each emits a
// diagnostic when encountered, so half-rewritten builds never happen
// silently.
//
// The scanner only resolves the local import alias of pkg/q per file.
// It does not consult go/types — call expressions are matched purely
// on AST shape and the local alias.

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
)

// qPkgImportPath is the import path of pkg/q, the surface the
// preprocessor recognises.
const qPkgImportPath = "github.com/GiGurra/q/pkg/q"

// family enumerates the source-monad entries the rewriter knows how
// to handle. Bare and chain entries within the same family share zero-
// value emission and rewrite-shape skeletons; the method (when present)
// picks the right way to spell the bubbled error.
type family int

const (
	familyTry family = iota
	familyTryE
	familyNotNil
	familyNotNilE
	familyCheck   // q.Check(err) — void, always formDiscard
	familyCheckE  // q.CheckE(err).<method> — void chain, always formDiscard
	familyOpen    // q.Open(v, err).Release(cleanup) — value chain, always Release-terminated
	familyOpenE   // q.OpenE(v, err).<shape?>.Release(cleanup) — value chain with optional shape
	familyOk      // q.Ok(v, ok) — comma-ok bubble using ErrNotOk sentinel
	familyOkE     // q.OkE(v, ok).<method> — comma-ok chain
	familyTrace   // q.Trace(v, err) — bubble prefixed with call-site file:line
	familyTraceE  // q.TraceE(v, err).<method> — trace-prefixed chain
	familyLock        // q.Lock(l) — Lock + defer Unlock
	familyTODO        // q.TODO([msg]) — panic with file:line prefix
	familyUnreachable // q.Unreachable([msg]) — panic with file:line prefix
	familyRequire     // q.Require(cond, [msg]) — bubble an error when cond is false
	familyRecv        // q.Recv(ch) — channel receive with close bubble
	familyRecvE       // q.RecvE(ch).<method> — chain variant
	familyAs          // q.As[T](x) — type assertion with failure bubble
	familyAsE         // q.AsE[T](x).<method> — chain variant
	familyDebugPrintln  // q.DebugPrintln(v) — in-place rewrite to q.DebugPrintlnAt("label", v)
	familyDebugSlogAttr // q.DebugSlogAttr(v) — in-place rewrite to slog.Any("label", v)
	familySlogAttr      // q.SlogAttr(v) — in-place rewrite to slog.Any("<src>", v)
	familySlogFile      // q.SlogFile() — in-place rewrite to slog.Any("file", "<basename>")
	familySlogLine      // q.SlogLine() — in-place rewrite to slog.Any("line", <line-int>)
	familySlogFileLine  // q.SlogFileLine() — in-place rewrite to slog.Any("file", "<basename>:<line>")
	familyFile          // q.File() — in-place rewrite to "<basename>" string literal
	familyLine          // q.Line() — in-place rewrite to <line-int> integer literal
	familyFileLine      // q.FileLine() — in-place rewrite to "<basename>:<line>" string literal
	familyExpr          // q.Expr(v) — in-place rewrite to "<src-text-of-v>" string literal
	familyAwait       // q.Await(f) — Try-like bubble using q.AwaitRaw as the source
	familyAwaitE      // q.AwaitE(f).<method> — TryE-like chain over q.AwaitRaw
	familyRecoverAuto  // defer q.Recover()       — inject &err from enclosing sig
	familyRecoverEAuto // defer q.RecoverE().M(x) — same, for the chain variant
	familyCheckCtx       // q.CheckCtx(ctx) — ctx.Err() checkpoint
	familyCheckCtxE      // q.CheckCtxE(ctx).<method> — chain variant
	familyRecvCtx      // q.RecvCtx(ctx, ch) — ctx-aware channel receive
	familyRecvCtxE     // q.RecvCtxE(ctx, ch).<method> — chain variant
	familyAwaitCtx     // q.AwaitCtx(ctx, f) — ctx-aware future await
	familyAwaitCtxE    // q.AwaitCtxE(ctx, f).<method> — chain variant
	familyTimeout      // ctx = q.Timeout(ctx, dur) — WithTimeout + defer cancel
	familyDeadline     // ctx = q.Deadline(ctx, t)  — WithDeadline + defer cancel
	familyAwaitAll     // q.AwaitAll(futures...) — fan-in, bubble first err
	familyAwaitAllE    // chain variant
	familyAwaitAllCtx  // q.AwaitAllCtx(ctx, futures...) — same with ctx cancel
	familyAwaitAllCtxE // chain variant
	familyAwaitAny     // q.AwaitAny(futures...) — first success wins
	familyAwaitAnyE    // chain variant
	familyAwaitAnyCtx  // q.AwaitAnyCtx(ctx, futures...) — same with ctx cancel
	familyAwaitAnyCtxE // chain variant
	familyRecvAny      // q.RecvAny(chans...) — first-value-wins multi-channel select
	familyRecvAnyE     // chain variant
	familyRecvAnyCtx   // q.RecvAnyCtx(ctx, chans...)
	familyRecvAnyCtxE  // chain variant
	familyDrainCtx     // q.DrainCtx(ctx, ch) — drain until close or cancel
	familyDrainCtxE    // chain variant
	familyDrainAllCtx  // q.DrainAllCtx(ctx, chans...)
	familyDrainAllCtxE // chain variant
	familyEnumValues  // q.EnumValues[T]() — literal []T of all constants of T
	familyEnumNames   // q.EnumNames[T]() — literal []string of constant names
	familyEnumName    // q.EnumName[T](v) — switch on value, return name
	familyEnumParse   // q.EnumParse[T](s) (T, error) — switch on string, return value
	familyEnumValid   // q.EnumValid[T](v) bool — membership check
	familyEnumOrdinal // q.EnumOrdinal[T](v) int — declaration-order index
	familyF    // q.F("hi {name}") — compile-time string interpolation
	familyFerr // q.Ferr("err: {x}") — interpolation + errors.New / fmt.Errorf
	familyFln  // q.Fln("dbg: {v}") — interpolation + fmt.Fprintln to DebugWriter
	familySQL      // q.SQL("...{x}...") — placeholder-style parameterised SQL (?, ?...)
	familyPgSQL    // q.PgSQL("...{x}...") — PostgreSQL-style ($1, $2, ...)
	familyNamedSQL // q.NamedSQL("...{x}...") — named-param style (:name1, :name2, ...)
	familyExhaustive // switch q.Exhaustive(v) { ... } — compile-time enforced exhaustiveness
	familyUpper  // q.Upper("...") — compile-time string-case transforms
	familyLower
	familySnake
	familyKebab
	familyCamel
	familyPascal
	familyTitle
	familyGenStringer       // var _ = q.GenStringer[T]() — synthesize String() method
	familyGenEnumJSONStrict // var _ = q.GenEnumJSONStrict[T]() — name-based JSON, errors on unknown
	familyGenEnumJSONLax    // var _ = q.GenEnumJSONLax[T]() — passthrough JSON, preserves unknown
	familyFields    // q.Fields[T]() — exported field names of struct T
	familyAllFields // q.AllFields[T]() — every field name of struct T
	familyTypeName  // q.TypeName[T]() — defined type name as string
	familyTag       // q.Tag[T](field, key) — struct tag value
	familyMatch     // q.Match(v, q.Case(...), q.Default(...)) — value-returning switch
	familyAtCompileTime     // q.AtCompileTime(func() R { ... }, codec...) — comptime evaluation
	familyAtCompileTimeCode // q.AtCompileTimeCode[R](func() string { ... }) — comptime code generation
	familyGenerator         // q.Generator[T](func() { ... q.Yield(v) ... }) — iter.Seq[T] sugar
	familyAssemble       // q.Assemble[T](recipes...) (T, error) — auto-derived DI
	familyAssembleAll    // q.AssembleAll[T](recipes...) ([]T, error) — multi-provider aggregation
	familyAssembleStruct // q.AssembleStruct[T](recipes...) (T, error) — field-decomposed multi-output
	familyTern           // q.Tern[T](cond, ifTrue, ifFalse) T — conditional expression
)

// form is the syntactic position of a recognised q.* call:
//
//	formDefine  -> v   := q.Try(call())     (declares LHS via :=)
//	formAssign  -> v    = q.Try(call())     (assigns to existing LHS)
//	formDiscard ->        q.Try(call())     (ExprStmt; bubbles, drops T/p)
//	formReturn  -> return …, q.Try(…), …    (q.* anywhere in a result)
//	formHoist   -> v := f(q.Try(…), …)      (q.* nested inside a non-
//	                                         return statement's expr)
//
// formHoist is the general case: the rewriter binds each q.* call to
// its own `_qTmpN`, emits per-call bubble checks, then re-emits the
// original statement with each q.* span replaced by its temp. The
// direct-bind forms (formDefine/formAssign/formDiscard) are kept for
// the common simple shapes so the rewrite stays tight (one line
// instead of two for `v := q.Try(call())`).
type form int

const (
	formDefine form = iota
	formAssign
	formDiscard
	formReturn
	formHoist
)

// qSubCall captures the per-call-site pieces of a recognised q.*
// expression: which family/method, the inner (T, error) call or *T
// expression, the outer call span, and any chain-method arguments.
// One callShape holds one (non-return forms) or many (formReturn with
// multiple q.*s in the same expression) of these.
type qSubCall struct {
	// Family identifies the source-monad entry — Try, TryE, etc.
	Family family

	// Method is the chain method name on a TryE / NotNilE shape — Err,
	// ErrF, Catch, Wrap, Wrapf — or "" for a bare Try / NotNil.
	Method string

	// MethodArgs are the args passed to the chain method. nil when
	// Method is "".
	MethodArgs []ast.Expr

	// InnerExpr is the source expression handed to the q.* entry: a
	// (T, error)-returning call for the Try family, or any pointer-
	// returning expression for the NotNil family. The rewriter copies
	// its source span verbatim into the bind line.
	InnerExpr ast.Expr

	// OuterCall is the q.* call expression (bare `q.Try(...)` or the
	// outer chain call `q.TryE(...).Method(...)`). For formReturn,
	// the rewriter splices `_qTmpN` into the reconstructed final
	// return in place of this call's source span.
	OuterCall ast.Expr

	// ReleaseArg is the cleanup function passed to .Release in the
	// Open family (familyOpen / familyOpenE). nil for every other
	// family AND for the .NoRelease() variant. When non-nil, the
	// rewriter emits a `defer (<cleanup>)(<resultVar>)` line on the
	// success path so the cleanup fires when the enclosing function
	// returns.
	ReleaseArg ast.Expr

	// NoRelease is true when the Open chain terminates with the
	// zero-arg .NoRelease() instead of .Release(cleanup). Bubble
	// path is identical; the rewriter skips the defer-cleanup line.
	NoRelease bool

	// AutoRelease is true when the Open chain terminates with the
	// zero-arg .Release() form. The preprocessor infers the cleanup
	// from the resource's type at compile time (channel close, or
	// a Close() method). The typecheck pass populates AutoCleanup
	// with the inferred kind; the rewriter consults AutoCleanup
	// when emitting the defer line. Mutually exclusive with
	// NoRelease and with a non-nil ReleaseArg.
	AutoRelease bool

	// AutoCleanup is the cleanup form the typecheck pass inferred
	// for an AutoRelease=true call. Zero (cleanupUnknown) until
	// the typecheck pass has run; if still zero by rewriter time
	// the typecheck pass either skipped (no importcfg) or emitted
	// a diagnostic that aborted the build.
	AutoCleanup cleanupKind

	// RecoverSteps carries any leading .RecoverIs / .RecoverAs chain
	// methods that sit between the entry call and the terminal
	// method (in source order). Currently only the TryE chain
	// exposes these. Empty for every other chain shape.
	RecoverSteps []recoverStep

	// OkArgs is the raw argument list of the q.Ok / q.OkE entry
	// call. nil for every other family. Ok accepts either a single
	// (T, bool)-returning CallExpr or two separate expressions
	// (value, ok); the rewriter reads the source span from the
	// first arg's Pos to the last arg's End to produce an inner-text
	// that drops straight into a tuple bind (`v, _qOkN := <span>`).
	OkArgs []ast.Expr

	// AsType is the explicit type argument in a q.As[T] / q.AsE[T]
	// call; nil for every other family. The rewriter splices its
	// source text into the generated type-assertion `<x>.(<T>)`.
	AsType ast.Expr

	// EntryEllipsis is the position of the variadic spread `...` on
	// the entry call's last argument, or token.NoPos if the call is
	// not variadic-spread. Only meaningful for the AwaitAll / AwaitAny
	// families whose entry signatures accept `...Future[T]`. When
	// valid, the rewriter appends `...` to the raw-helper call it
	// emits so the variadic spread survives the rewrite.
	EntryEllipsis token.Pos

	// EnumConsts is the list of constant identifier names declared
	// with the enum type T (carried in AsType) in T's declaring
	// package, in source declaration order. Populated by the
	// typecheck pass for the q.Enum* families; absent or nil for
	// every other family. The rewriter splices these into the
	// generated literal slice / switch.
	EnumConsts []string

	// EnumTypeText is the text of T as it should appear in the
	// generated code — usually the AsType expression printed via the
	// FileSet (e.g. "Color"). Populated by the typecheck pass. The
	// rewriter uses it to spell the IIFE param type and the slice
	// element type. For same-package T this is just the type name;
	// for cross-package T the typecheck pass refuses with a
	// diagnostic (the rewriter doesn't yet emit qualified
	// identifiers for enum lookups).
	EnumTypeText string

	// EnumConstValues is a parallel slice to EnumConsts carrying
	// each constant's runtime value, formatted as Go-syntax source
	// text (e.g. `"pending"` for a string-backed const, `0` for an
	// int-backed iota constant). Populated by the typecheck pass
	// for the Gen* directives that need the value to drive JSON
	// marshaller code generation.
	EnumConstValues []string

	// EnumUnderlyingKind is the basic-type kind of T's underlying
	// type — "string" / "int" / "uint" / "int64" / etc. Populated
	// by the typecheck pass. Empty when T's underlying isn't a
	// basic type (which the Gen* directives reject with a
	// diagnostic).
	EnumUnderlyingKind string

	// StructFields is the resolved field-name list for the q.Fields
	// / q.AllFields families. Populated by the typecheck pass
	// (resolveStructReflection). The rewriter splices these into a
	// `[]string{...}` literal.
	StructFields []string

	// ResolvedString carries pre-computed string output for any
	// reflection family that resolves to a single string at
	// compile time (q.TypeName, q.Tag). Populated by the typecheck
	// pass; the rewriter quotes it as a Go string literal at the
	// call site.
	ResolvedString string

	// MatchCases is the list of arms for q.Match. Populated at
	// scan time by walking the q.Match call's variadic arguments.
	// Each arm carries the value expression (nil for default arms),
	// the result expression, and an IsDefault marker.
	MatchCases []matchCase

	// AtCTClosure is the *ast.FuncLit handed to q.AtCompileTime.
	// Captured at scan time; the synthesis pass reads its Body and
	// signature. nil for every other family.
	AtCTClosure *ast.FuncLit

	// AtCTCodecExpr is the codec argument to q.AtCompileTime, when
	// supplied. nil for the default-codec form (synthesis pass
	// substitutes JSONCodec[R]). When set, it carries the user's
	// codec expression — its source text is spliced into the
	// synthesized program's Encode call AND, for non-primitive R,
	// into the companion file's init() Decode call.
	AtCTCodecExpr ast.Expr

	// AtCTResultText is the spelling of R as written in the type
	// argument of q.AtCompileTime[R]. Captured at scan time from
	// IndexExpr.Index. Used by the synthesis pass for the
	// `func() R { ... }()` invocation in the synthesized main.go.
	AtCTResultText string

	// AtCTResolved is the rewritten replacement text for this call
	// site, populated by the synthesis pass after the subprocess
	// has run. Either a Go literal (primitive R + JSONCodec) or an
	// identifier reference like `_qCt0_value` (non-primitive).
	AtCTResolved string

	// AtCTIndex is the topo-sorted index of this call within the
	// package's AtCompileTime call set. Populated by the synthesis
	// pass; used by the rewriter when emitting `_qCt<N>_value`
	// references for the non-inline path.
	AtCTIndex int

	// AssembleRecipes is the raw recipe argument list captured from the
	// q.Assemble / q.AssembleErr / q.AssembleE call site. Each entry is
	// either a function reference (top-level func / method value /
	// function-typed expr) or an inline value expression. The typecheck
	// pass classifies each entry against go/types and populates
	// AssembleSteps in topo order. Unused for every other family.
	//
	// `q.PermitNil(x)` wrappers are stripped by the scanner: the
	// stored expression is the inner `x`, and the parallel
	// AssemblePermitNil[i] flag is set true so the resolver can mark
	// the resulting step as opting out of the runtime nil-check.
	AssembleRecipes []ast.Expr

	// AssemblePermitNil is parallel to AssembleRecipes — true at
	// index i when the user's i-th recipe was wrapped in
	// q.PermitNil(...). The resolver propagates the flag onto the
	// emitted assembleStep so the rewriter skips the nil-check on
	// that step's bound _qDep<N>.
	AssemblePermitNil []bool

	// AssembleSteps is the topo-sorted recipe sequence the rewriter
	// emits, populated by resolveAssemble. Each step carries the recipe
	// expression's source-text-ready form, the input dep type keys (in
	// signature order), the output type key, and an errored flag.
	// Empty until typecheck has run.
	AssembleSteps []assembleStep

	// AssembleTargetTypeText is the spelling of T as resolved via
	// go/types' qualifier (same form as EnumTypeText). Used by the
	// rewriter to spell the IIFE's return type and the zero-value
	// expression. When empty, the rewriter falls back to `any`.
	AssembleTargetTypeText string

	// AssembleTargetKey is the canonical typeKey of T (path-qualified
	// form), used by the rewriter to look up T's _qDep<N> in the body
	// emitter. Stashed at resolveAssemble time so emit doesn't have to
	// re-derive it from the type-text spelling (which qualifies
	// same-package types differently).
	AssembleTargetKey string

	// AssembleCtxDepKey is the canonical typeKey of the recipe that
	// provides context.Context, when one exists. Set by
	// resolveAssemble. The rewriter uses it to bind _qDbg from that
	// dep variable for the optional debug-trace prelude. Empty when
	// no recipe provides context.Context — debug is silently
	// disabled in that case.
	AssembleCtxDepKey string

	// AssembleAllProviderRidxs is the recipe-index list of every
	// recipe whose output is assignable to T, in recipe declaration
	// order. Populated by resolveAssemble for familyAssembleAll only;
	// nil for familyAssemble. Multiple recipes producing the same
	// type all contribute (each becomes a distinct slice element).
	// The rewriter looks each ridx up in the per-step
	// RecipeIdx -> _qDep<N> map to emit the final
	// `[]T{_qDep<i>, _qDep<j>, ...}` literal.
	AssembleAllProviderRidxs []int

	// AssembleStructFieldNames and AssembleStructFieldKeys are
	// parallel slices: each pair is one field of the struct target T.
	// FieldNames carries the field's exported (or in-package) Go
	// identifier; FieldKeys carries the field type's canonical
	// typeKey for provider lookup. Populated by resolveAssemble for
	// familyAssembleStruct only; nil for other families. The
	// rewriter emits a `T{Field1: _qDepX, Field2: _qDepY, ...}`
	// literal, looking up each FieldKey in the per-step
	// OutputKey -> _qDep<N> map.
	AssembleStructFieldNames []string
	AssembleStructFieldKeys  []string

	// AssembleChain identifies the chain terminator on a q.Assemble /
	// q.AssembleAll / q.AssembleStruct call:
	//   - assembleChainRelease — `.Release()` — defer-injected cleanup
	//     in the enclosing function, returns (T, error).
	//   - assembleChainNoRelease — `.NoRelease()` — caller-managed
	//     shutdown closure, returns (T, func(), error).
	// Populated by the scanner when the chain is detected. Zero
	// (assembleChainNone) for non-chained calls — bare q.Assemble[T](...)
	// returns AssemblyResult[T] which doesn't match (T, error), so
	// such calls fail the build naturally; the scanner additionally
	// emits a guiding diagnostic.
	AssembleChain assembleChain

	// TernCond / TernT are the two q.Tern args captured at scan time.
	// TernResultTypeText is T's spelling under the q.Tern call's
	// package qualifier (populated by resolveTern). All zero for
	// non-Tern families.
	//
	// Lazy semantics for TernT come from source-splicing rather than
	// a runtime func() T — the rewriter places TernT's source span
	// inside the IIFE's true-branch only, so its expression is only
	// evaluated when cond is true.
	TernCond           ast.Expr
	TernT              ast.Expr
	TernResultTypeText string
}

// assembleChain enumerates the chain terminator on an Assemble*
// family call. None means the user wrote a bare `q.Assemble[T](...)`
// with no chain — invalid; the rewriter surfaces a diagnostic
// pointing the user at .Release() or .NoRelease().
type assembleChain int

const (
	assembleChainNone assembleChain = iota
	assembleChainRelease
	assembleChainNoRelease
)

// matchCase is one arm of a q.Match expression — either a
// `q.Case(cond, result)` or a `q.Default(result)`. The scanner only
// captures the source-level shape; the typecheck pass classifies
// cond's role (value match vs predicate, eager vs lazy fn) by
// inspecting its type via go/types, populating CondLazy / IsPredicate
// / ResultLazy accordingly.
type matchCase struct {
	CondExpr   ast.Expr // nil for default; cond expression for q.Case
	ResultExpr ast.Expr
	IsDefault  bool

	// Populated by the typecheck pass once go/types resolves the
	// expressions — see resolveMatch.
	IsPredicate bool // cond is bool / func()bool — emit `if cond` instead of equality compare
	CondLazy    bool // cond is func()V or func()bool — call before use
	ResultLazy  bool // result is func()R — call before return
}

// assembleStep is one entry in the topo-sorted recipe sequence the
// q.Assemble family emits. Populated by resolveAssemble; consumed by
// buildAssembleReplacement / renderAssembleE.
//
// For a function-reference recipe the rewriter emits
//
//	_qDep<N> := <CallText>(<input refs by InputKeys>)
//
// or, when Errored is true,
//
//	_qDep<N>, _qErr<N> := <CallText>(<input refs>)
//	if _qErr<N> != nil { return *new(T), _qErr<N> }
//
// For an inline-value recipe (no inputs) the rewriter emits the
// CallText verbatim:
//
//	_qDep<N> := <CallText>
type assembleStep struct {
	// RecipeIdx is the original variadic-arg index this step came
	// from. The rewriter looks up sub.AssembleRecipes[RecipeIdx] to
	// recover the source expression and feeds it through
	// exprTextSubst; "unused recipe" diagnostics also reference this
	// 1-based index.
	RecipeIdx int

	// IsValue is true when this step is an inline value (no inputs,
	// no call). The rewriter omits the `()` and the input list.
	IsValue bool

	// Errored is true when the recipe returns (T, error). The rewriter
	// emits the bind-and-bubble shape; otherwise a single-value bind.
	// Always false for inline values (their type is the value's type).
	Errored bool

	// InputKeys are the *resolved* provider keys for this recipe's
	// inputs in signature order — populated by resolveAssemble after
	// the assignability resolution pass. For an exact-type input
	// these equal the input's type-key; for an interface input
	// satisfied by a concrete provider, these equal the concrete
	// provider's outputKey. The rewriter looks each up in the per-
	// step OutputKey map to recover the producing _qDep<N> name.
	InputKeys []string

	// OutputKey is the type key the recipe provides; used by the
	// rewriter to derive the final-step's "is this T?" decision and
	// by other steps' InputKeys lookups.
	OutputKey string

	// OutputIsNilable is true when the recipe's output type can hold
	// a nil value at runtime — pointer, interface, slice, map, chan,
	// or func. The rewriter emits a runtime nil-check on the bound
	// _qDep<N> immediately after the recipe call (before any implicit
	// interface conversion at downstream consumer call sites). Pure
	// q.Assemble panics on nil; q.AssembleErr / q.AssembleE bubble a
	// fmt.Errorf("...: %%w", q.ErrNil) so callers can errors.Is the
	// failure against the q.ErrNil sentinel.
	OutputIsNilable bool

	// PermitNil is true when the user wrapped the recipe in
	// q.PermitNil(...) at the call site. The rewriter then skips the
	// runtime nil-check on this step's bound _qDep<N>, allowing nil
	// to flow through as a legitimate value.
	PermitNil bool

	// Label is the diagnostic-friendly recipe identifier
	// ("#N (snippet)") spliced into the runtime nil-check message.
	// Captured at resolve time because the rewriter doesn't have the
	// AST snippet helper available at emit time.
	Label string

	// IsResource is true when the recipe's signature is
	// (T, func(), error). The rewriter binds an extra
	// _qCleanup<N> from the second return and pushes it onto the
	// cleanups chain on success. False for (T) and (T, error)
	// recipes whose T isn't auto-cleanup-able. (Auto-detect of
	// Close()-able T is layered on top: a (T, error) recipe whose T
	// has Close() / Close() error / is a channel also gets
	// IsResource=true, with the cleanup synthesised from the type
	// shape rather than the recipe's signature.)
	IsResource bool

	// AutoCleanup is the inferred cleanup form when IsResource is
	// true but the recipe's signature is NOT (T, func(), error) —
	// instead T's type itself is auto-cleanup-able. The rewriter
	// reads this to emit the right defer line shape:
	//   - cleanupChannelClose → `close(_qDep<N>)`
	//   - cleanupCloseError   → `_ = _qDep<N>.Close()`
	//   - cleanupCloseNoError → `_qDep<N>.Close()`
	// Zero (cleanupUnknown) when IsResource came from the explicit
	// 3-return recipe shape.
	AutoCleanup cleanupKind
}

// cleanupKind is the inferred cleanup form for q.Open(...).Release()
// (zero-arg, auto-inferred). The typecheck pass populates it on
// each AutoRelease qSubCall; the rewriter dispatches on it when
// emitting the defer line.
type cleanupKind int

const (
	cleanupUnknown   cleanupKind = iota // not inferred yet (or typecheck skipped)
	cleanupChanClose                    // channel type → defer close(v)
	cleanupCloseVoid                    // T has Close() → defer v.Close()
	cleanupCloseErr                     // T has Close() error → defer func() { _ = v.Close() }()
)

// recoverKind selects between the errors.Is and errors.As variants
// of the chain-continuing recovery methods.
type recoverKind int

const (
	recoverKindIs recoverKind = iota // .RecoverIs(sentinel, value)
	recoverKindAs                    // .RecoverAs(typedNil, value)
)

// recoverStep encodes one .RecoverIs(sentinel, value) or
// .RecoverAs(typedNil, value) chain step. Stored on qSubCall and
// rendered before the terminal bubble check.
type recoverStep struct {
	// Kind selects between errors.Is (sentinel) and errors.As (type).
	Kind recoverKind
	// MatchArg is the first arg to RecoverIs / RecoverAs:
	// - For Is: the sentinel error expression.
	// - For As: a typed-nil literal whose type the rewriter extracts
	//   at compile time (e.g. `(*MyErr)(nil)`).
	MatchArg ast.Expr
	// ValueArg is the second arg — the recovery value of type T.
	ValueArg ast.Expr
}

// callShape describes one recognised q.* call site, captured at scan
// time so the rewriter can emit the inlined replacement without
// re-walking the AST.
type callShape struct {
	// Stmt is the enclosing statement (or top-level declaration) for
	// this q.* call. Its source span is the unit the rewriter
	// replaces; everything else inside the function stays intact.
	// Type is ast.Node rather than ast.Stmt so package-level shapes
	// (`var X = q.AtCompileTime(...)`) can use *ast.GenDecl.
	Stmt ast.Node

	// Form is the syntactic position — define, assign, discard, return.
	Form form

	// LHSExpr is the AST node for the LHS in formDefine / formAssign;
	// nil for formDiscard and formReturn. Resolved to source text by
	// the rewriter using its source-byte buffer. For formDefine the
	// AST node is always *ast.Ident; for formAssign it can be any
	// addressable expression (ident, selector, index).
	LHSExpr ast.Expr

	// Calls holds the recognised q.* sub-calls inside this statement.
	// Always length 1 for non-return forms. formReturn can have
	// length >= 1 (e.g. `return q.Try(a()) * q.Try(b()), nil`).
	Calls []qSubCall

	// EnclosingFuncType is the signature of the nearest-enclosing
	// function — either the outer FuncDecl or an inner FuncLit. Its
	// Results give the rewriter the result types from which to
	// synthesize the zero-value tuple in the early return. Using the
	// nearest enclosing scope is critical for q.* inside closures:
	// an inner FuncLit can have a different result arity/types than
	// its outer FuncDecl.
	EnclosingFuncType *ast.FuncType
}

// chainMethods is the set of recognised TryE chain method names.
// NotNilE shares the same vocabulary; the receiver type is what
// distinguishes them.
var chainMethods = map[string]bool{
	"Err":   true,
	"ErrF":  true,
	"Catch": true,
	"Wrap":  true,
	"Wrapf": true,
}

// qRuntimeHelpers is the set of q.* function names whose call sites
// are left untouched by the preprocessor — they have real bodies
// and execute at runtime. Scanner's "unsupported q.* shape"
// diagnostic path (findQReference / qCallRootPos) ignores these
// so a standalone `q.ToErr(...)` call doesn't trip the fallback
// flag.
var qRuntimeHelpers = map[string]bool{
	"ToErr":              true,
	"Const":              true,
	"Unwrap":                  true,
	"UnwrapE":                 true,
	"WithAssemblyDebug":       true,
	"WithAssemblyDebugWriter": true,
	"AssemblyDebugWriter":     true,
	"DebugPrintlnAt":     true,
	"SlogCtx":            true,
	"SlogContextHandler": true,
	"InstallSlog":        true,
	"InstallSlogJSON":    true,
	"InstallSlogText":    true,
	"Async":           true,
	"AwaitRaw":        true,
	"AwaitRawCtx":     true,
	"AwaitAllRaw":     true,
	"AwaitAllRawCtx":  true,
	"AwaitAnyRaw":     true,
	"AwaitAnyRawCtx":  true,
	"RecvRawCtx":      true,
	"RecvAnyRaw":      true,
	"RecvAnyRawCtx":   true,
	"Drain":           true,
	"DrainAll":        true,
	"DrainRawCtx":     true,
	"DrainAllRawCtx":  true,
	"Recover":         true,
	"RecoverE":        true,
	"GoroutineID":     true,
	"Map":             true,
	"MapErr":          true,
	"FlatMap":         true,
	"FlatMapErr":      true,
	"Filter":          true,
	"FilterErr":       true,
	"GroupBy":         true,
	"MapValues":       true,
	"MapValuesErr":    true,
	"MapKeys":         true,
	"MapKeysErr":      true,
	"MapEntries":      true,
	"MapEntriesErr":   true,
	"Keys":            true,
	"Values":          true,
	"Zip":             true,
	"Unzip":           true,
	"ZipMap":          true,
	"Coro":            true,
	"Sort":            true,
	"SortBy":          true,
	"SortFunc":        true,
	"Min":             true,
	"Max":             true,
	"MinBy":           true,
	"MaxBy":           true,
	"Sum":             true,
	"Exists":          true,
	"ExistsErr":       true,
	"ForAll":          true,
	"ForAllErr":       true,
	"Find":            true,
	"Reduce":          true,
	"Fold":            true,
	"FoldErr":         true,
	"Distinct":        true,
	"DistinctBy":      true,
	"Partition":       true,
	"Chunk":           true,
	"Count":           true,
	"Take":            true,
	"Drop":            true,
	"ForEach":          true,
	"ForEachErr":       true,
	"ParMap":           true,
	"ParMapErr":        true,
	"ParFlatMap":       true,
	"ParFlatMapErr":    true,
	"ParFilter":        true,
	"ParFilterErr":     true,
	"ParForEach":       true,
	"ParForEachErr":    true,
	"ParGroupBy":       true,
	"ParGroupByErr":    true,
	"ParExists":        true,
	"ParExistsErr":     true,
	"ParForAll":        true,
	"ParForAllErr":     true,
	"WithPar":          true,
	"WithParUnbounded": true,
	"GetPar":           true,
}

// scanFile walks one parsed source file and returns the list of
// recognised q.* call sites it contains, plus diagnostics for any
// q.* calls that did not match a recognised shape.
//
// If the file does not import pkg/q, scanFile returns (nil, nil, nil)
// — no work to do.
func scanFile(fset *token.FileSet, path string, file *ast.File) ([]callShape, []Diagnostic, error) {
	alias := qImportAlias(file)
	if alias == "" {
		return nil, nil, nil
	}

	var shapes []callShape
	var diags []Diagnostic

	// Pre-collect closure FuncLit nodes whose bodies the scanner
	// should NOT descend into:
	//   - q.AtCompileTime / q.AtCompileTimeCode — synthesis pass owns
	//     the body.
	//   - q.Generator — the renderer rewrites q.Yield calls inside the
	//     body itself; outer scanning would mis-classify q.Yield (it
	//     has no rendering when not inside a Generator) or descend
	//     into a func() body where q.Try / etc. have nothing to bubble
	//     to.
	skip := map[*ast.FuncLit]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn := call.Fun
		if ix, ok := fn.(*ast.IndexExpr); ok {
			fn = ix.X
		}
		if sel, ok := fn.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == alias &&
				(sel.Sel.Name == "AtCompileTime" || sel.Sel.Name == "AtCompileTimeCode" ||
					sel.Sel.Name == "Generator") {
				if len(call.Args) >= 1 {
					if lit, ok := call.Args[0].(*ast.FuncLit); ok {
						skip[lit] = true
					}
				}
			}
		}
		return true
	})

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Body == nil {
				continue
			}
			walkBlock(fset, path, d.Body, alias, d.Type, &shapes, &diags, skip)
		case *ast.GenDecl:
			if d.Tok != token.VAR {
				continue
			}
			scanTopLevelVarSpec(fset, path, d, alias, &shapes, &diags)
		}
	}

	return shapes, diags, nil
}

// scanTopLevelVarSpec recognises the package-level directive shape
//
//	var _ = q.GenX[T]()
//
// where GenX is one of GenStringer, GenEnumJSONStrict,
// GenEnumJSONLax. Each is captured as a callShape with a synthetic
// ExprStmt so the rewriter substitutes the call's span with
// `struct{}{}` (no-op runtime initializer). The file-synthesis pass
// (see synthesizeGenFile) reads these shapes back out to generate
// the companion methods file.
//
// Other top-level var declarations (regular package vars) are
// ignored — q.* references inside their initializers would be a
// scoping shape we don't currently support, and a future feature
// can revisit if needed.
func scanTopLevelVarSpec(fset *token.FileSet, path string, gd *ast.GenDecl, alias string, shapes *[]callShape, diags *[]Diagnostic) {
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		// Try Gen* directives first — `var _ = q.GenX[T]()` shape.
		if len(vs.Names) == 1 && vs.Names[0].Name == "_" && len(vs.Values) == 1 {
			if call, ok := vs.Values[0].(*ast.CallExpr); ok {
				if fam, typeArg, ok := classifyGenDirective(call, alias); ok {
					if len(call.Args) != 0 {
						*diags = append(*diags, diagAt(fset, path, call.Pos(),
							fmt.Sprintf("q.%s takes no arguments; got %d", genDirectiveName(fam), len(call.Args))))
						continue
					}
					*shapes = append(*shapes, callShape{
						Stmt:  &ast.ExprStmt{X: call},
						Form:  formDiscard,
						Calls: []qSubCall{{Family: fam, AsType: typeArg, OuterCall: call}},
					})
					continue
				}
			}
		}
		// q.AtCompileTime / q.AtCompileTimeCode at package level:
		//   var X = q.AtCompileTime[T](...)
		//   var X = q.AtCompileTimeCode[T](...)
		// Each ValueSpec can declare multiple names, but for this
		// shape we require single-name, single-value pairs.
		if len(vs.Names) == 1 && len(vs.Values) == 1 {
			if call, ok := vs.Values[0].(*ast.CallExpr); ok {
				sub, matched, classifyErr := classifyQCall(call, alias)
				if classifyErr != nil {
					*diags = append(*diags, diagAt(fset, path, call.Pos(), classifyErr.Error()))
					continue
				}
				if matched && (sub.Family == familyAtCompileTime || sub.Family == familyAtCompileTimeCode) {
					*shapes = append(*shapes, callShape{
						Stmt:    gd,
						Form:    formDefine,
						LHSExpr: vs.Names[0],
						Calls:   []qSubCall{sub},
					})
					continue
				}
			}
		}
	}
}

// classifyGenDirective recognises q.GenX[T]() at top-level and
// returns the family + type-arg expression. ok=false otherwise.
func classifyGenDirective(call *ast.CallExpr, alias string) (family, ast.Expr, bool) {
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "GenStringer"); ok {
		return familyGenStringer, typeArg, true
	}
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "GenEnumJSONStrict"); ok {
		return familyGenEnumJSONStrict, typeArg, true
	}
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "GenEnumJSONLax"); ok {
		return familyGenEnumJSONLax, typeArg, true
	}
	return 0, nil, false
}

// genDirectiveName is the user-facing spelling of a Gen directive
// family for diagnostic messages.
func genDirectiveName(f family) string {
	switch f {
	case familyGenStringer:
		return "GenStringer"
	case familyGenEnumJSONStrict:
		return "GenEnumJSONStrict"
	case familyGenEnumJSONLax:
		return "GenEnumJSONLax"
	}
	return "GenX"
}

// walkBlock recursively scans every statement in the block (and every
// block nested inside it — if-bodies, else-bodies, for-bodies, switch
// case clauses, type-switch cases, select-comm-clauses, range bodies,
// and plain blocks). Each leaf statement is fed to matchStatement; any
// q.* reference in an unsupported position produces a diagnostic.
//
// fnType is the signature of the nearest-enclosing function — the
// outer FuncDecl at the top level, or an inner FuncLit after we cross
// into a closure body via walkFuncLits. Each shape matched here
// records fnType as its EnclosingFuncType so the rewriter uses the
// correct result list for zero-value synthesis.
//
// Nested-scope rewrites are correct because each shape's replacement
// is a self-contained block (zero or one bind line, an if check, a
// return). The new statements live where the original q.* statement
// lived — same scope, same in-flow position — so visibility of the
// LHS variable to surrounding code is preserved.
func walkBlock(fset *token.FileSet, path string, block *ast.BlockStmt, alias string, fnType *ast.FuncType, shapes *[]callShape, diags *[]Diagnostic, skip map[*ast.FuncLit]bool) {
	if block == nil {
		return
	}
	for _, stmt := range block.List {
		shape, ok, err := matchStatement(stmt, alias, fnType)
		if err != nil {
			*diags = append(*diags, diagAt(fset, path, stmt.Pos(), err.Error()))
		} else if ok {
			*shapes = append(*shapes, shape)
		} else if !isContainerStmt(stmt) {
			if pos := findQReference(stmt, alias); pos.IsValid() {
				*diags = append(*diags, diagAt(fset, path, pos,
					fmt.Sprintf("unsupported q.* call shape; supported: `v := %s.Try/NotNil(...)`, `v = %s.Try/NotNil(...)`, `%s.Try/NotNil(...)` (discard), `return %s.Try/NotNil(...), …` (q.* as one top-level return result), with optional .Err / .ErrF / .Catch / .Wrap / .Wrapf chain methods on the *E entries", alias, alias, alias, alias)))
			}
		}
		walkChildBlocks(fset, path, stmt, alias, fnType, shapes, diags, skip)
		walkFuncLits(fset, path, stmt, alias, shapes, diags, skip)
	}
}

// isContainerStmt reports whether stmt's role is to hold further
// statements rather than to be one itself. Such statements should
// not trigger the "unsupported q.* shape" fallback — walkChildBlocks
// descends into them and matches their contents properly. Missing
// this check for CaseClause / CommClause causes findQReference to
// false-positive on every q.* call inside a switch default.
func isContainerStmt(stmt ast.Stmt) bool {
	switch stmt.(type) {
	case *ast.BlockStmt, *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt,
		*ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt,
		*ast.CaseClause, *ast.CommClause, *ast.LabeledStmt:
		return true
	}
	return false
}

// walkFuncLits finds *ast.FuncLit expressions reachable from stmt
// without crossing a nested *ast.BlockStmt boundary. For each FuncLit
// found, walkBlock recurses into the FuncLit's body with the
// FuncLit's own Type as the enclosing function scope — so q.* inside
// a closure bubbles according to the *closure's* result list, not the
// outer FuncDecl's.
//
// The BlockStmt-boundary guard prevents double-walking: FuncLits
// inside nested statements (e.g. an if-body) are reached via the
// recursive walkBlock call that walkChildBlocks triggers on that
// body. Stopping descent at FuncLits themselves after processing
// avoids walking a closure's body twice when it contains further
// closures; the recursive walkBlock on the outer closure will
// discover the inner one.
func walkFuncLits(fset *token.FileSet, path string, stmt ast.Stmt, alias string, shapes *[]callShape, diags *[]Diagnostic, skip map[*ast.FuncLit]bool) {
	ast.Inspect(stmt, func(n ast.Node) bool {
		if blk, ok := n.(*ast.BlockStmt); ok && ast.Node(blk) != ast.Node(stmt) {
			return false
		}
		lit, ok := n.(*ast.FuncLit)
		if !ok {
			return true
		}
		if skip[lit] {
			// Body is owned by another pass (synthesis for
			// q.AtCompileTime, the Generator renderer for q.Generator).
			return false
		}
		walkBlock(fset, path, lit.Body, alias, lit.Type, shapes, diags, skip)
		return false
	})
}

// walkChildBlocks dispatches into every child *ast.BlockStmt that the
// given statement holds. Mirrors the shape of go/ast's nodes that
// carry blocks; new statement kinds added by future Go versions would
// need a case here.
func walkChildBlocks(fset *token.FileSet, path string, stmt ast.Stmt, alias string, fnType *ast.FuncType, shapes *[]callShape, diags *[]Diagnostic, skip map[*ast.FuncLit]bool) {
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		walkBlock(fset, path, s, alias, fnType, shapes, diags, skip)
	case *ast.IfStmt:
		if s.Init != nil {
			scanContainerInit(fset, path, s.Init, alias, fnType, shapes, diags, "if")
		}
		if s.Cond != nil {
			scanContainerExpr(fset, path, s, []ast.Expr{s.Cond}, alias, fnType, shapes, diags, "if")
		}
		walkBlock(fset, path, s.Body, alias, fnType, shapes, diags, skip)
		if s.Else != nil {
			switch elseStmt := s.Else.(type) {
			case *ast.BlockStmt:
				walkBlock(fset, path, elseStmt, alias, fnType, shapes, diags, skip)
			case *ast.IfStmt:
				walkChildBlocks(fset, path, elseStmt, alias, fnType, shapes, diags, skip)
			}
		}
	case *ast.ForStmt:
		if s.Init != nil {
			scanContainerInit(fset, path, s.Init, alias, fnType, shapes, diags, "for")
		}
		if s.Post != nil {
			scanContainerInit(fset, path, s.Post, alias, fnType, shapes, diags, "for")
		}
		if s.Cond != nil {
			scanContainerExpr(fset, path, s, []ast.Expr{s.Cond}, alias, fnType, shapes, diags, "for")
		}
		walkBlock(fset, path, s.Body, alias, fnType, shapes, diags, skip)
	case *ast.RangeStmt:
		if s.X != nil {
			scanContainerExpr(fset, path, s, []ast.Expr{s.X}, alias, fnType, shapes, diags, "range")
		}
		walkBlock(fset, path, s.Body, alias, fnType, shapes, diags, skip)
	case *ast.SwitchStmt:
		if shape, ok, err := matchExhaustiveSwitch(s, alias, fnType); err != nil {
			*diags = append(*diags, diagAt(fset, path, s.Pos(), err.Error()))
		} else if ok {
			*shapes = append(*shapes, shape)
		} else if s.Tag != nil {
			scanContainerExpr(fset, path, s, []ast.Expr{s.Tag}, alias, fnType, shapes, diags, "switch")
		}
		if s.Init != nil {
			scanContainerInit(fset, path, s.Init, alias, fnType, shapes, diags, "switch")
		}
		walkBlock(fset, path, s.Body, alias, fnType, shapes, diags, skip)
	case *ast.TypeSwitchStmt:
		walkBlock(fset, path, s.Body, alias, fnType, shapes, diags, skip)
	case *ast.SelectStmt:
		walkBlock(fset, path, s.Body, alias, fnType, shapes, diags, skip)
	case *ast.CaseClause:
		// case clause Body is a []ast.Stmt without its own BlockStmt
		// wrapper, so we walk it inline.
		for _, child := range s.Body {
			subShape, ok, err := matchStatement(child, alias, fnType)
			if err != nil {
				*diags = append(*diags, diagAt(fset, path, child.Pos(), err.Error()))
			} else if ok {
				*shapes = append(*shapes, subShape)
			} else if !isContainerStmt(child) {
				if pos := findQReference(child, alias); pos.IsValid() {
					*diags = append(*diags, diagAt(fset, path, pos,
						fmt.Sprintf("unsupported q.* call shape; supported: `v := %s.Try/NotNil(...)`, `v = %s.Try/NotNil(...)`, `%s.Try/NotNil(...)` (discard), `return %s.Try/NotNil(...), …` (q.* as one top-level return result), with optional .Err / .ErrF / .Catch / .Wrap / .Wrapf chain methods on the *E entries", alias, alias, alias, alias)))
				}
			}
			walkChildBlocks(fset, path, child, alias, fnType, shapes, diags, skip)
			walkFuncLits(fset, path, child, alias, shapes, diags, skip)
		}
	case *ast.CommClause:
		for _, child := range s.Body {
			subShape, ok, err := matchStatement(child, alias, fnType)
			if err != nil {
				*diags = append(*diags, diagAt(fset, path, child.Pos(), err.Error()))
			} else if ok {
				*shapes = append(*shapes, subShape)
			} else if !isContainerStmt(child) {
				if pos := findQReference(child, alias); pos.IsValid() {
					*diags = append(*diags, diagAt(fset, path, pos,
						fmt.Sprintf("unsupported q.* call shape; supported: `v := %s.Try/NotNil(...)`, `v = %s.Try/NotNil(...)`, `%s.Try/NotNil(...)` (discard), `return %s.Try/NotNil(...), …` (q.* as one top-level return result), with optional .Err / .ErrF / .Catch / .Wrap / .Wrapf chain methods on the *E entries", alias, alias, alias, alias)))
				}
			}
			walkChildBlocks(fset, path, child, alias, fnType, shapes, diags, skip)
			walkFuncLits(fset, path, child, alias, shapes, diags, skip)
		}
	case *ast.LabeledStmt:
		walkChildBlocks(fset, path, s.Stmt, alias, fnType, shapes, diags, skip)
	}
}

// qImportAlias returns the local name under which pkg/q is imported in
// the file, "q" by default, "" if pkg/q is not imported.
func qImportAlias(file *ast.File) string {
	for _, imp := range file.Imports {
		path, err := unquote(imp.Path.Value)
		if err != nil || path != qPkgImportPath {
			continue
		}
		if imp.Name != nil {
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				// Blank or dot imports do not yield a usable selector
				// alias for the rewriter.
				return ""
			}
			return imp.Name.Name
		}
		return "q"
	}
	return ""
}

// matchStatement dispatches on the statement form (assign vs expr stmt)
// and looks for one of the recognised q.* call shapes. Recognised
// shapes:
//
//	<ident>      := <alias>.Try(<call>)                       formDefine
//	<ident>       = <alias>.Try(<call>)                       formAssign
//	             	<alias>.Try(<call>)                        formDiscard
//
//	<ident>      := <alias>.TryE(<call>).<Method>(<args>...)  formDefine
//	<ident>       = <alias>.TryE(<call>).<Method>(<args>...)  formAssign
//	             	<alias>.TryE(<call>).<Method>(<args>...)   formDiscard
//
// (Same set mirrored for q.NotNil / q.NotNilE with a *T expression in
// place of the inner call.)
//
// Returns the shape on a match, (zero, false, nil) on a no-match, and
// (zero, false, err) when the statement is *almost* a match but
// malformed — the caller turns these into diagnostics.
func matchStatement(stmt ast.Stmt, alias string, fnType *ast.FuncType) (callShape, bool, error) {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		if s.Tok != token.DEFINE && s.Tok != token.ASSIGN {
			return callShape{}, false, nil
		}
		// Direct-bind eligibility: single LHS, single RHS, RHS IS a
		// q.* call, LHS has no nested q.*. That's the tight one-line
		// shape: `v, _qErrN := inner`. Anything else falls through to
		// hoist.
		if len(s.Lhs) == 1 && len(s.Rhs) == 1 && !hasQRef(s.Lhs[0], alias) {
			lhsOK := true
			if s.Tok == token.DEFINE {
				if _, isIdent := s.Lhs[0].(*ast.Ident); !isIdent {
					lhsOK = false
				}
			}
			if lhsOK {
				sub, ok, err := classifyQCall(s.Rhs[0], alias)
				if err != nil {
					return callShape{}, false, err
				}
				// Direct-bind also requires the matched q.*'s own
				// InnerExpr / MethodArgs to be free of nested q.*s.
				// Otherwise the bind line would embed an unrewritten
				// q.* call — fall through to hoist, which handles
				// nesting by rendering innermost first and feeding
				// their `_qTmpN` into the outer's bind.
				if ok && !hasQRefInSub(sub, alias) {
					f := formDefine
					if s.Tok == token.ASSIGN {
						f = formAssign
					}
					return callShape{
						Stmt:              stmt,
						Form:              f,
						LHSExpr:           s.Lhs[0],
						Calls:             []qSubCall{sub},
						EnclosingFuncType: fnType,
					}, true, nil
				}
			}
		}
		// Hoist path: bind every q.* call inside LHS or RHS to a temp,
		// check each, then re-emit the statement with the q.* spans
		// substituted.
		return matchHoist(stmt, fnType, alias, append(append([]ast.Expr(nil), s.Lhs...), s.Rhs...))

	case *ast.ExprStmt:
		sub, ok, err := classifyQCall(s.X, alias)
		if err != nil {
			return callShape{}, false, err
		}
		if ok {
			return callShape{
				Stmt:              stmt,
				Form:              formDiscard,
				Calls:             []qSubCall{sub},
				EnclosingFuncType: fnType,
			}, true, nil
		}
		return matchHoist(stmt, fnType, alias, []ast.Expr{s.X})

	case *ast.DeferStmt:
		sub, ok, err := classifyDeferredRecover(s.Call, alias)
		if err != nil {
			return callShape{}, false, err
		}
		if ok {
			return callShape{
				Stmt:              stmt,
				Form:              formDiscard,
				Calls:             []qSubCall{sub},
				EnclosingFuncType: fnType,
			}, true, nil
		}
		return callShape{}, false, nil

	case *ast.ReturnStmt:
		// Find every q.* call anywhere inside the return's result
		// expressions — not just top-level. This makes shapes like
		// `return q.Try(a()) * q.Try(b()), nil` work: each q.* call
		// binds to its own `_qTmpN` with its own bubble check, and
		// the final return keeps the rest of the expression verbatim
		// with each `_qTmpN` spliced into its call's source span.
		subs, err := collectQCalls(s.Results, alias)
		if err != nil {
			return callShape{}, false, err
		}
		if len(subs) == 0 {
			return callShape{}, false, nil
		}
		return callShape{
			Stmt:              stmt,
			Form:              formReturn,
			Calls:             subs,
			EnclosingFuncType: fnType,
		}, true, nil
	}
	return callShape{}, false, nil
}

// matchHoist builds a formHoist callShape by collecting every q.*
// call reachable from exprs. Returns (zero, false, nil) when no q.*
// is present — the caller falls through to the findQReference
// diagnostic path.
func matchHoist(stmt ast.Stmt, fnType *ast.FuncType, alias string, exprs []ast.Expr) (callShape, bool, error) {
	subs, err := collectQCalls(exprs, alias)
	if err != nil {
		return callShape{}, false, err
	}
	if len(subs) == 0 {
		return callShape{}, false, nil
	}
	return callShape{
		Stmt:              stmt,
		Form:              formHoist,
		Calls:             subs,
		EnclosingFuncType: fnType,
	}, true, nil
}

// collectQCalls walks each expr's AST sub-tree with ast.Inspect and
// returns every recognised q.* call in source order, including those
// nested inside another matched q.*'s InnerExpr or MethodArgs. The
// rewriter handles nesting by rendering the innermost first and
// substituting each inner's `_qTmpN` into the outer's bind line.
//
// Descent stops at *ast.FuncLit boundaries — those belong to a
// nested function scope and are handled by walkFuncLits with the
// inner FuncType as their enclosing signature.
func collectQCalls(exprs []ast.Expr, alias string) ([]qSubCall, error) {
	var subs []qSubCall
	var walkErr error
	for _, e := range exprs {
		ast.Inspect(e, func(n ast.Node) bool {
			if walkErr != nil {
				return false
			}
			if _, isLit := n.(*ast.FuncLit); isLit {
				return false
			}
			expr, ok := n.(ast.Expr)
			if !ok {
				return true
			}
			sub, matched, err := classifyQCall(expr, alias)
			if err != nil {
				walkErr = err
				return false
			}
			if matched {
				subs = append(subs, sub)
			}
			return true
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}
	return subs, nil
}

// hasQRefInSub reports whether the sub's user-provided expression
// fields contain any nested q.* reference. Used by the direct-bind
// eligibility check: if the matched q.* wraps another q.* anywhere
// in its arguments, hoist instead so each nested call gets its own
// bind / temp / check, and parents pull from those temps via
// substituteSpans.
//
// Every field that holds a user-supplied expression must be
// inspected here. Missing one means a direct-bind path leaves nested
// q.* calls unrewritten — they survive as panic stubs and fail at
// runtime.
func hasQRefInSub(sub qSubCall, alias string) bool {
	if hasQRef(sub.InnerExpr, alias) {
		return true
	}
	for _, a := range sub.MethodArgs {
		if hasQRef(a, alias) {
			return true
		}
	}
	for _, a := range sub.OkArgs {
		if hasQRef(a, alias) {
			return true
		}
	}
	if hasQRef(sub.ReleaseArg, alias) {
		return true
	}
	if hasQRef(sub.AsType, alias) {
		return true
	}
	for _, st := range sub.RecoverSteps {
		if hasQRef(st.MatchArg, alias) {
			return true
		}
		if hasQRef(st.ValueArg, alias) {
			return true
		}
	}
	for _, r := range sub.AssembleRecipes {
		if hasQRef(r, alias) {
			return true
		}
	}
	return false
}

// hasQRef reports whether the expression's AST contains any selector
// rooted at the local q-alias identifier. Descent stops at FuncLits
// — those belong to a nested scope and are scanned separately.
func hasQRef(e ast.Expr, alias string) bool {
	if e == nil {
		return false
	}
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if found {
			return false
		}
		if _, isLit := n.(*ast.FuncLit); isLit {
			return false
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if x.Name == alias {
			found = true
			return false
		}
		return true
	})
	return found
}

// classifyQCall examines a single expression and reports whether it is
// one of the recognised q.* call shapes (bare or chain). The per-call
// fields are returned as a qSubCall; per-statement fields (Stmt,
// Form, LHSExpr, EnclosingFuncType) are the caller's responsibility.
func classifyQCall(expr ast.Expr, alias string) (qSubCall, bool, error) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return qSubCall{}, false, nil
	}

	// Bare q.Try / q.NotNil.
	if isSelector(call.Fun, alias, "Try") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.Try must take exactly one argument (a (T, error)-returning call); got %d", len(call.Args))
		}
		if _, ok := call.Args[0].(*ast.CallExpr); !ok {
			return qSubCall{}, false, fmt.Errorf("q.Try's argument must itself be a call expression returning (T, error)")
		}
		return qSubCall{Family: familyTry, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	if isSelector(call.Fun, alias, "NotNil") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.NotNil must take exactly one argument (a *T expression); got %d", len(call.Args))
		}
		return qSubCall{Family: familyNotNil, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Bare q.Await — blocks on Future, bubbles err.
	if isSelector(call.Fun, alias, "Await") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.Await must take exactly one argument (a Future); got %d", len(call.Args))
		}
		return qSubCall{Family: familyAwait, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Bare q.DebugPrintln — in-place rewrite to q.DebugPrintlnAt
	// with an auto-generated label carrying call-site file:line
	// and the source text of the argument expression.
	if isSelector(call.Fun, alias, "DebugPrintln") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.DebugPrintln must take exactly one argument (the value to print); got %d", len(call.Args))
		}
		return qSubCall{Family: familyDebugPrintln, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Bare q.DebugSlogAttr — in-place rewrite to slog.Any with a
	// label carrying call-site file:line and the source text of
	// the argument expression. Returns slog.Attr (no pass-through).
	if isSelector(call.Fun, alias, "DebugSlogAttr") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.DebugSlogAttr must take exactly one argument (the value to wrap as a slog.Attr); got %d", len(call.Args))
		}
		return qSubCall{Family: familyDebugSlogAttr, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Bare q.SlogAttr — in-place rewrite to slog.Any keyed by the
	// argument's source text only (no file:line prefix). The
	// production-grade slog helper.
	if isSelector(call.Fun, alias, "SlogAttr") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.SlogAttr must take exactly one argument (the value to wrap as a slog.Attr); got %d", len(call.Args))
		}
		return qSubCall{Family: familySlogAttr, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Bare q.SlogFile — zero-arg in-place rewrite to
	// slog.Any("file", "<basename>"). The file basename is captured
	// at compile time from OuterCall's position.
	if isSelector(call.Fun, alias, "SlogFile") {
		if len(call.Args) != 0 {
			return qSubCall{}, false, fmt.Errorf("q.SlogFile takes no arguments; got %d", len(call.Args))
		}
		return qSubCall{Family: familySlogFile, OuterCall: expr}, true, nil
	}
	// Bare q.SlogLine — zero-arg in-place rewrite to
	// slog.Any("line", <line-int>).
	if isSelector(call.Fun, alias, "SlogLine") {
		if len(call.Args) != 0 {
			return qSubCall{}, false, fmt.Errorf("q.SlogLine takes no arguments; got %d", len(call.Args))
		}
		return qSubCall{Family: familySlogLine, OuterCall: expr}, true, nil
	}
	// Bare q.SlogFileLine — zero-arg in-place rewrite to
	// slog.Any("file", "<basename>:<line>").
	if isSelector(call.Fun, alias, "SlogFileLine") {
		if len(call.Args) != 0 {
			return qSubCall{}, false, fmt.Errorf("q.SlogFileLine takes no arguments; got %d", len(call.Args))
		}
		return qSubCall{Family: familySlogFileLine, OuterCall: expr}, true, nil
	}
	// Bare q.File — zero-arg in-place rewrite to "<basename>"
	// string literal.
	if isSelector(call.Fun, alias, "File") {
		if len(call.Args) != 0 {
			return qSubCall{}, false, fmt.Errorf("q.File takes no arguments; got %d", len(call.Args))
		}
		return qSubCall{Family: familyFile, OuterCall: expr}, true, nil
	}
	// Bare q.Line — zero-arg in-place rewrite to <line-int>.
	if isSelector(call.Fun, alias, "Line") {
		if len(call.Args) != 0 {
			return qSubCall{}, false, fmt.Errorf("q.Line takes no arguments; got %d", len(call.Args))
		}
		return qSubCall{Family: familyLine, OuterCall: expr}, true, nil
	}
	// Bare q.FileLine — zero-arg in-place rewrite to
	// "<basename>:<line>" string literal.
	if isSelector(call.Fun, alias, "FileLine") {
		if len(call.Args) != 0 {
			return qSubCall{}, false, fmt.Errorf("q.FileLine takes no arguments; got %d", len(call.Args))
		}
		return qSubCall{Family: familyFileLine, OuterCall: expr}, true, nil
	}
	// Bare q.Expr — in-place rewrite to "<src-text>" string
	// literal. The argument's value is discarded; only its source
	// text is captured.
	if isSelector(call.Fun, alias, "Expr") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.Expr must take exactly one argument (the expression to source-quote); got %d", len(call.Args))
		}
		return qSubCall{Family: familyExpr, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Bare q.Recv — channel receive with close bubble.
	if isSelector(call.Fun, alias, "Recv") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.Recv must take exactly one argument (a channel); got %d", len(call.Args))
		}
		return qSubCall{Family: familyRecv, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Bare q.As[T](x) — type assertion with failure bubble.
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "As"); ok {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.As[T] must take exactly one argument (the value to assert); got %d", len(call.Args))
		}
		return qSubCall{Family: familyAs, InnerExpr: call.Args[0], AsType: typeArg, OuterCall: expr}, true, nil
	}
	// q.EnumValues[T]() / q.EnumNames[T]() — zero-arg, constant-folded.
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "EnumValues"); ok {
		if len(call.Args) != 0 {
			return qSubCall{}, false, fmt.Errorf("q.EnumValues[T] takes no arguments; got %d", len(call.Args))
		}
		return qSubCall{Family: familyEnumValues, AsType: typeArg, OuterCall: expr}, true, nil
	}
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "EnumNames"); ok {
		if len(call.Args) != 0 {
			return qSubCall{}, false, fmt.Errorf("q.EnumNames[T] takes no arguments; got %d", len(call.Args))
		}
		return qSubCall{Family: familyEnumNames, AsType: typeArg, OuterCall: expr}, true, nil
	}
	// q.EnumName[T](v) / q.EnumValid[T](v) / q.EnumOrdinal[T](v) /
	// q.EnumParse[T](s) — single arg (value or name), in-place
	// rewrite to a switch expression.
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "EnumName"); ok {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.EnumName[T] must take exactly one argument (a value of type T); got %d", len(call.Args))
		}
		return qSubCall{Family: familyEnumName, InnerExpr: call.Args[0], AsType: typeArg, OuterCall: expr}, true, nil
	}
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "EnumParse"); ok {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.EnumParse[T] must take exactly one argument (the name string); got %d", len(call.Args))
		}
		return qSubCall{Family: familyEnumParse, InnerExpr: call.Args[0], AsType: typeArg, OuterCall: expr}, true, nil
	}
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "EnumValid"); ok {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.EnumValid[T] must take exactly one argument (a value of type T); got %d", len(call.Args))
		}
		return qSubCall{Family: familyEnumValid, InnerExpr: call.Args[0], AsType: typeArg, OuterCall: expr}, true, nil
	}
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "EnumOrdinal"); ok {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.EnumOrdinal[T] must take exactly one argument (a value of type T); got %d", len(call.Args))
		}
		return qSubCall{Family: familyEnumOrdinal, InnerExpr: call.Args[0], AsType: typeArg, OuterCall: expr}, true, nil
	}
	// q.Fields[T]() / q.AllFields[T]() — zero-arg, struct-only.
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "Fields"); ok {
		if len(call.Args) != 0 {
			return qSubCall{}, false, fmt.Errorf("q.Fields[T] takes no arguments; got %d", len(call.Args))
		}
		return qSubCall{Family: familyFields, AsType: typeArg, OuterCall: expr}, true, nil
	}
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "AllFields"); ok {
		if len(call.Args) != 0 {
			return qSubCall{}, false, fmt.Errorf("q.AllFields[T] takes no arguments; got %d", len(call.Args))
		}
		return qSubCall{Family: familyAllFields, AsType: typeArg, OuterCall: expr}, true, nil
	}
	// q.TypeName[T]() — zero-arg.
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "TypeName"); ok {
		if len(call.Args) != 0 {
			return qSubCall{}, false, fmt.Errorf("q.TypeName[T] takes no arguments; got %d", len(call.Args))
		}
		return qSubCall{Family: familyTypeName, AsType: typeArg, OuterCall: expr}, true, nil
	}
	// q.AtCompileTimeCode[R](func() string { ... }) — code generation.
	// Closure returns Go source code (a string); the rewriter parses
	// the result and splices the parsed expression at the call site.
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "AtCompileTimeCode"); ok {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.AtCompileTimeCode takes exactly one argument (func() string); got %d", len(call.Args))
		}
		fnLit, ok := call.Args[0].(*ast.FuncLit)
		if !ok {
			return qSubCall{}, false, fmt.Errorf("q.AtCompileTimeCode: argument must be a function literal (anonymous function), not a function reference or variable")
		}
		if fnLit.Type == nil || fnLit.Type.Params == nil {
			return qSubCall{}, false, fmt.Errorf("q.AtCompileTimeCode: closure must have signature func() string (no parameters)")
		}
		if fnLit.Type.Params.NumFields() != 0 {
			return qSubCall{}, false, fmt.Errorf("q.AtCompileTimeCode: closure must take zero parameters")
		}
		if fnLit.Type.Results == nil || fnLit.Type.Results.NumFields() != 1 {
			return qSubCall{}, false, fmt.Errorf("q.AtCompileTimeCode: closure must return exactly one string value")
		}
		return qSubCall{
			Family:      familyAtCompileTimeCode,
			AsType:      typeArg,
			InnerExpr:   fnLit,
			AtCTClosure: fnLit,
			OuterCall:   expr,
		}, true, nil
	}
	// q.AtCompileTime(func() R { ... }, codec...) — comptime evaluation.
	// First arg MUST be a *ast.FuncLit; the synthesis pass runs it in
	// a subprocess and splices the result here. Optional second arg is
	// the codec (default JSONCodec[R]). The type argument R is
	// captured for the synthesized program's typed bind.
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "AtCompileTime"); ok {
		if len(call.Args) < 1 || len(call.Args) > 2 {
			return qSubCall{}, false, fmt.Errorf("q.AtCompileTime takes one or two arguments (func() R, optional codec); got %d", len(call.Args))
		}
		fnLit, ok := call.Args[0].(*ast.FuncLit)
		if !ok {
			return qSubCall{}, false, fmt.Errorf("q.AtCompileTime: argument must be a function literal (anonymous function), not a function reference or variable")
		}
		if fnLit.Type == nil || fnLit.Type.Params == nil {
			return qSubCall{}, false, fmt.Errorf("q.AtCompileTime: closure must have signature func() R (no parameters)")
		}
		if fnLit.Type.Params.NumFields() != 0 {
			return qSubCall{}, false, fmt.Errorf("q.AtCompileTime: closure must take zero parameters (got %d)", fnLit.Type.Params.NumFields())
		}
		if fnLit.Type.Results == nil || fnLit.Type.Results.NumFields() != 1 {
			return qSubCall{}, false, fmt.Errorf("q.AtCompileTime: closure must return exactly one value")
		}
		// Non-FuncLit second arg also rejected (a func variable would
		// hide the codec from the synthesis pass).
		var codecExpr ast.Expr
		if len(call.Args) == 2 {
			codecExpr = call.Args[1]
		}
		return qSubCall{
			Family:        familyAtCompileTime,
			AsType:        typeArg,
			InnerExpr:     fnLit, // for source-text extraction by the rewriter
			AtCTClosure:   fnLit,
			AtCTCodecExpr: codecExpr,
			OuterCall:     expr,
		}, true, nil
	}
	// q.Generator[T](func() { ... q.Yield(v) ... }) — sugar over
	// iter.Seq[T]. The closure body is rewritten in place by the
	// Generator renderer, which substitutes each q.Yield(v) inside
	// it with `if !yield(v) { return }` and wraps the whole
	// expression as `iter.Seq[T](func(yield func(T) bool) { ... })`.
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "Generator"); ok {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.Generator takes exactly one argument (a func() closure); got %d", len(call.Args))
		}
		fnLit, ok := call.Args[0].(*ast.FuncLit)
		if !ok {
			return qSubCall{}, false, fmt.Errorf("q.Generator: argument must be a function literal (anonymous function), not a function reference or variable")
		}
		if fnLit.Type == nil || fnLit.Type.Params == nil || fnLit.Type.Params.NumFields() != 0 {
			return qSubCall{}, false, fmt.Errorf("q.Generator: closure must take zero parameters")
		}
		if fnLit.Type.Results != nil && fnLit.Type.Results.NumFields() != 0 {
			return qSubCall{}, false, fmt.Errorf("q.Generator: closure must have no return values")
		}
		return qSubCall{
			Family:    familyGenerator,
			AsType:    typeArg,
			InnerExpr: fnLit,
			OuterCall: expr,
		}, true, nil
	}
	// q.Match(value, q.Case(...), q.Default(...)) — value-returning
	// switch. The first arg is the match value; remaining args are
	// q.Case / q.Default calls. The scanner extracts each arm's
	// value/result expressions onto MatchCases; the rewriter emits
	// the IIFE-wrapped switch.
	if isSelector(call.Fun, alias, "Match") {
		if len(call.Args) < 2 {
			return qSubCall{}, false, fmt.Errorf("q.Match takes a value plus at least one case (q.Case or q.Default); got %d args", len(call.Args))
		}
		cases, cerr := parseMatchArms(call.Args[1:], alias)
		if cerr != nil {
			return qSubCall{}, false, cerr
		}
		return qSubCall{Family: familyMatch, InnerExpr: call.Args[0], MatchCases: cases, OuterCall: expr}, true, nil
	}
	// q.Case / q.Default at the regular classifier path: silently
	// no-match. They're only meaningful as q.Match's argument, where
	// the q.Match scanner extracts them. Anywhere else the runtime
	// panic stub fires.
	if isSelector(call.Fun, alias, "Case") || isSelector(call.Fun, alias, "Default") {
		return qSubCall{}, false, nil
	}
	// q.Assemble / q.AssembleAll / q.AssembleStruct are matched only
	// in chain form (`q.Assemble[T](...).Release()` or `.NoRelease()`).
	// The chain is detected at the outer `.Release` / `.NoRelease`
	// call site below. Bare entries (without a chain terminator) fall
	// through to the unsupported-shape diagnostic; Go's typechecker
	// will also reject the call because q.Assemble returns
	// AssemblyResult[T] rather than (T, error).
	// q.Tern[T](cond, t) — conditional expression sugar. cond is a
	// plain bool, t is a T value; the rewriter splices their source
	// text into an IIFE so t is only evaluated when cond is true.
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "Tern"); ok {
		if len(call.Args) != 2 {
			return qSubCall{}, false, fmt.Errorf("q.Tern[T] takes exactly 2 arguments (cond, t); got %d", len(call.Args))
		}
		return qSubCall{
			Family:    familyTern,
			AsType:    typeArg,
			TernCond:  call.Args[0],
			TernT:     call.Args[1],
			OuterCall: expr,
		}, true, nil
	}
	// q.Tag[T](field, key) — both args MUST be string literals so
	// the rewriter can resolve the tag at compile time.
	if typeArg, ok := isIndexedSelector(call.Fun, alias, "Tag"); ok {
		if len(call.Args) != 2 {
			return qSubCall{}, false, fmt.Errorf("q.Tag[T] takes exactly two arguments (field, key string literals); got %d", len(call.Args))
		}
		for i, label := range []string{"field", "key"} {
			lit, ok := call.Args[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return qSubCall{}, false, fmt.Errorf("q.Tag[T]'s %s argument must be a Go string literal", label)
			}
		}
		return qSubCall{Family: familyTag, AsType: typeArg, OkArgs: call.Args, OuterCall: expr}, true, nil
	}
	// q.F / q.Ferr / q.Fln — compile-time string interpolation. Each
	// takes a single string-literal format with `{expr}` placeholders.
	if isSelector(call.Fun, alias, "F") {
		if err := validateFLiteral("q.F", call.Args); err != nil {
			return qSubCall{}, false, err
		}
		return qSubCall{Family: familyF, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	if isSelector(call.Fun, alias, "Ferr") {
		if err := validateFLiteral("q.Ferr", call.Args); err != nil {
			return qSubCall{}, false, err
		}
		return qSubCall{Family: familyFerr, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	if isSelector(call.Fun, alias, "Fln") {
		if err := validateFLiteral("q.Fln", call.Args); err != nil {
			return qSubCall{}, false, err
		}
		return qSubCall{Family: familyFln, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// q.Exhaustive(v) — only legal as the tag of a switch statement.
	// Reaching this path means the call was found in any other
	// expression position; the dedicated SwitchStmt walker captures
	// the legitimate placement separately.
	if isSelector(call.Fun, alias, "Exhaustive") {
		return qSubCall{}, false, fmt.Errorf("q.Exhaustive can only be used as the tag of a switch statement, e.g. `switch q.Exhaustive(v) { case A: …; case B: … }`")
	}
	// q.SQL / q.PgSQL / q.NamedSQL — parameterised SQL builders.
	// Same placeholder syntax as q.F, different rewrite output.
	if isSelector(call.Fun, alias, "SQL") {
		if err := validateFLiteral("q.SQL", call.Args); err != nil {
			return qSubCall{}, false, err
		}
		return qSubCall{Family: familySQL, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	if isSelector(call.Fun, alias, "PgSQL") {
		if err := validateFLiteral("q.PgSQL", call.Args); err != nil {
			return qSubCall{}, false, err
		}
		return qSubCall{Family: familyPgSQL, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	if isSelector(call.Fun, alias, "NamedSQL") {
		if err := validateFLiteral("q.NamedSQL", call.Args); err != nil {
			return qSubCall{}, false, err
		}
		return qSubCall{Family: familyNamedSQL, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Compile-time string-case ops. Each takes a single string-literal
	// arg and folds to a string literal at compile time.
	for _, sf := range stringCaseFamilies {
		if isSelector(call.Fun, alias, sf.name) {
			if err := validateStringLiteralArg("q."+sf.name, call.Args); err != nil {
				return qSubCall{}, false, err
			}
			return qSubCall{Family: sf.fam, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
		}
	}
	// Bare q.CheckCtx — ctx.Err() checkpoint. Statement-only (discard).
	if isSelector(call.Fun, alias, "CheckCtx") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.CheckCtx must take exactly one argument (a context.Context); got %d", len(call.Args))
		}
		return qSubCall{Family: familyCheckCtx, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Bare q.RecvCtx(ctx, ch) — ctx-aware receive.
	if isSelector(call.Fun, alias, "RecvCtx") {
		if len(call.Args) != 2 {
			return qSubCall{}, false, fmt.Errorf("q.RecvCtx must take exactly two arguments (ctx, ch); got %d", len(call.Args))
		}
		return qSubCall{Family: familyRecvCtx, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr}, true, nil
	}
	// Bare q.AwaitCtx(ctx, future) — ctx-aware await.
	if isSelector(call.Fun, alias, "AwaitCtx") {
		if len(call.Args) != 2 {
			return qSubCall{}, false, fmt.Errorf("q.AwaitCtx must take exactly two arguments (ctx, future); got %d", len(call.Args))
		}
		return qSubCall{Family: familyAwaitCtx, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr}, true, nil
	}
	// Bare q.AwaitAll(futures...) — fan-in, bubble first err.
	if isSelector(call.Fun, alias, "AwaitAll") {
		// InnerExpr is unused by the Try-like-with-inner renderers
		// (the inner text is built from OkArgs directly), but
		// commonRenderInputs still calls exprTextSubst on it, so we
		// must hand it a non-nil expression. Use call.Fun as a
		// syntactically-valid placeholder; the returned text is
		// discarded.
		return qSubCall{Family: familyAwaitAll, InnerExpr: call.Fun, OkArgs: call.Args, OuterCall: expr, EntryEllipsis: call.Ellipsis}, true, nil
	}
	// Bare q.AwaitAllCtx(ctx, futures...) — same with ctx cancel.
	if isSelector(call.Fun, alias, "AwaitAllCtx") {
		if len(call.Args) < 1 {
			return qSubCall{}, false, fmt.Errorf("q.AwaitAllCtx must take at least one argument (ctx); got %d", len(call.Args))
		}
		return qSubCall{Family: familyAwaitAllCtx, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr, EntryEllipsis: call.Ellipsis}, true, nil
	}
	// Bare q.AwaitAny(futures...) — first success wins.
	if isSelector(call.Fun, alias, "AwaitAny") {
		return qSubCall{Family: familyAwaitAny, InnerExpr: call.Fun, OkArgs: call.Args, OuterCall: expr, EntryEllipsis: call.Ellipsis}, true, nil
	}
	// Bare q.AwaitAnyCtx(ctx, futures...) — same with ctx cancel.
	if isSelector(call.Fun, alias, "AwaitAnyCtx") {
		if len(call.Args) < 1 {
			return qSubCall{}, false, fmt.Errorf("q.AwaitAnyCtx must take at least one argument (ctx); got %d", len(call.Args))
		}
		return qSubCall{Family: familyAwaitAnyCtx, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr, EntryEllipsis: call.Ellipsis}, true, nil
	}
	// Bare q.RecvAny(chans...) — multi-channel first-value-wins select.
	if isSelector(call.Fun, alias, "RecvAny") {
		return qSubCall{Family: familyRecvAny, InnerExpr: call.Fun, OkArgs: call.Args, OuterCall: expr, EntryEllipsis: call.Ellipsis}, true, nil
	}
	// Bare q.RecvAnyCtx(ctx, chans...).
	if isSelector(call.Fun, alias, "RecvAnyCtx") {
		if len(call.Args) < 1 {
			return qSubCall{}, false, fmt.Errorf("q.RecvAnyCtx must take at least one argument (ctx); got %d", len(call.Args))
		}
		return qSubCall{Family: familyRecvAnyCtx, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr, EntryEllipsis: call.Ellipsis}, true, nil
	}
	// Bare q.DrainCtx(ctx, ch) — drain until close or cancel.
	if isSelector(call.Fun, alias, "DrainCtx") {
		if len(call.Args) != 2 {
			return qSubCall{}, false, fmt.Errorf("q.DrainCtx must take exactly two arguments (ctx, ch); got %d", len(call.Args))
		}
		return qSubCall{Family: familyDrainCtx, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr}, true, nil
	}
	// Bare q.DrainAllCtx(ctx, chans...).
	if isSelector(call.Fun, alias, "DrainAllCtx") {
		if len(call.Args) < 1 {
			return qSubCall{}, false, fmt.Errorf("q.DrainAllCtx must take at least one argument (ctx); got %d", len(call.Args))
		}
		return qSubCall{Family: familyDrainAllCtx, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr, EntryEllipsis: call.Ellipsis}, true, nil
	}
	// q.Timeout(ctx, dur) / q.Deadline(ctx, t) — define/assign shapes.
	if isSelector(call.Fun, alias, "Timeout") {
		if len(call.Args) != 2 {
			return qSubCall{}, false, fmt.Errorf("q.Timeout must take exactly two arguments (ctx, dur); got %d", len(call.Args))
		}
		return qSubCall{Family: familyTimeout, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr}, true, nil
	}
	if isSelector(call.Fun, alias, "Deadline") {
		if len(call.Args) != 2 {
			return qSubCall{}, false, fmt.Errorf("q.Deadline must take exactly two arguments (ctx, t); got %d", len(call.Args))
		}
		return qSubCall{Family: familyDeadline, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr}, true, nil
	}
	// Statement-only helpers with no chain — panic/defer shapes.
	if isSelector(call.Fun, alias, "Lock") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.Lock must take exactly one argument (a sync.Locker); got %d", len(call.Args))
		}
		return qSubCall{Family: familyLock, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	if isSelector(call.Fun, alias, "TODO") {
		if len(call.Args) > 1 {
			return qSubCall{}, false, fmt.Errorf("q.TODO takes at most one argument (an optional message string); got %d", len(call.Args))
		}
		return qSubCall{Family: familyTODO, MethodArgs: call.Args, OuterCall: expr}, true, nil
	}
	if isSelector(call.Fun, alias, "Unreachable") {
		if len(call.Args) > 1 {
			return qSubCall{}, false, fmt.Errorf("q.Unreachable takes at most one argument (an optional message string); got %d", len(call.Args))
		}
		return qSubCall{Family: familyUnreachable, MethodArgs: call.Args, OuterCall: expr}, true, nil
	}
	if isSelector(call.Fun, alias, "Require") {
		if len(call.Args) < 1 || len(call.Args) > 2 {
			return qSubCall{}, false, fmt.Errorf("q.Require takes 1 or 2 arguments (cond, [msg]); got %d", len(call.Args))
		}
		return qSubCall{Family: familyRequire, InnerExpr: call.Args[0], MethodArgs: call.Args[1:], OuterCall: expr}, true, nil
	}
	// Bare q.Trace — Try-shape with file:line-prefixed bubble.
	if isSelector(call.Fun, alias, "Trace") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.Trace must take exactly one argument (a (T, error)-returning call); got %d", len(call.Args))
		}
		if _, ok := call.Args[0].(*ast.CallExpr); !ok {
			return qSubCall{}, false, fmt.Errorf("q.Trace's argument must itself be a call expression returning (T, error)")
		}
		return qSubCall{Family: familyTrace, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}
	// Bare q.Ok — comma-ok bubble. Two valid arg shapes:
	//   q.Ok(fn())       — one CallExpr returning (T, bool)
	//   q.Ok(v, okExpr)  — two exprs, a T and a bool
	// The rewriter handles both by binding from the joined source span.
	if isSelector(call.Fun, alias, "Ok") {
		if err := validateOkArgs("q.Ok", call.Args); err != nil {
			return qSubCall{}, false, err
		}
		return qSubCall{Family: familyOk, InnerExpr: call.Args[0], OkArgs: call.Args, OuterCall: expr}, true, nil
	}
	// Bare q.Check — error-only bubble, no chain.
	if isSelector(call.Fun, alias, "Check") {
		if len(call.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.Check must take exactly one argument (an error expression); got %d", len(call.Args))
		}
		return qSubCall{Family: familyCheck, InnerExpr: call.Args[0], OuterCall: expr}, true, nil
	}

	// Chain on q.TryE / q.NotNilE / q.CheckE, or a q.Open / q.OpenE
	// chain terminated by .Release.
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		// .Release / .NoRelease terminal — q.Open / q.OpenE chain OR
		// q.Assemble / q.AssembleAll / q.AssembleStruct chain. Disambiguate
		// by inspecting the receiver: if it's q.Assemble[T](...) (an
		// indexed-selector call), dispatch to classifyAssembleChain;
		// otherwise fall through to q.Open's classifier.
		if sel.Sel.Name == "Release" || sel.Sel.Name == "NoRelease" {
			if entry, ok := sel.X.(*ast.CallExpr); ok {
				if isAssembleEntry(entry, alias) {
					return classifyAssembleChain(call, sel, entry, alias)
				}
			}
			return classifyOpenChain(call, sel, alias)
		}
		// Reject .RecoverIs / .RecoverAs as the outer (terminal)
		// method — they continue the chain and must be followed by
		// a real terminal that bubbles. Standalone use leaves the
		// captured err silently swallowed.
		if sel.Sel.Name == "RecoverIs" || sel.Sel.Name == "RecoverAs" {
			return qSubCall{}, false, fmt.Errorf("%s must be followed by a terminal method (Err, ErrF, Wrap, Wrapf, Catch); standalone use would silently swallow the bubble", sel.Sel.Name)
		}
		entry, isEntry := sel.X.(*ast.CallExpr)
		if !isEntry {
			return qSubCall{}, false, nil
		}
		// Peel any leading .RecoverIs / .RecoverAs steps off `entry`
		// before dispatching on the underlying entry name. Currently
		// only the q.TryE chain accepts these intermediates.
		actualEntry, recoverSteps, err := peelRecovers(entry)
		if err != nil {
			return qSubCall{}, false, err
		}
		entry = actualEntry
		if len(recoverSteps) > 0 && !isSelector(entry.Fun, alias, "TryE") {
			return qSubCall{}, false, fmt.Errorf("RecoverIs / RecoverAs are only supported on the q.TryE chain; chain entry must be q.TryE(...) for these intermediates to apply")
		}
		switch {
		case isSelector(entry.Fun, alias, "TryE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.TryE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf, RecoverIs, RecoverAs", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.TryE must take exactly one argument (a (T, error)-returning call); got %d", len(entry.Args))
			}
			if _, ok := entry.Args[0].(*ast.CallExpr); !ok {
				return qSubCall{}, false, fmt.Errorf("q.TryE's argument must itself be a call expression returning (T, error)")
			}
			return qSubCall{Family: familyTryE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OuterCall: expr, RecoverSteps: recoverSteps}, true, nil
		case isSelector(entry.Fun, alias, "NotNilE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.NotNilE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.NotNilE must take exactly one argument (a *T expression); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyNotNilE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "CheckE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.CheckE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.CheckE must take exactly one argument (an error expression); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyCheckE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "TraceE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.TraceE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.TraceE must take exactly one argument (a (T, error)-returning call); got %d", len(entry.Args))
			}
			if _, ok := entry.Args[0].(*ast.CallExpr); !ok {
				return qSubCall{}, false, fmt.Errorf("q.TraceE's argument must itself be a call expression returning (T, error)")
			}
			return qSubCall{Family: familyTraceE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "AwaitE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.AwaitE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.AwaitE must take exactly one argument (a Future); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyAwaitE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "RecvE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.RecvE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.RecvE must take exactly one argument (a channel); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyRecvE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "CheckCtxE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.CheckCtxE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.CheckCtxE must take exactly one argument (a context.Context); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyCheckCtxE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "RecvCtxE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.RecvCtxE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 2 {
				return qSubCall{}, false, fmt.Errorf("q.RecvCtxE must take exactly two arguments (ctx, ch); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyRecvCtxE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OkArgs: entry.Args, OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "AwaitCtxE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.AwaitCtxE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 2 {
				return qSubCall{}, false, fmt.Errorf("q.AwaitCtxE must take exactly two arguments (ctx, future); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyAwaitCtxE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OkArgs: entry.Args, OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "AwaitAllE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.AwaitAllE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			return qSubCall{Family: familyAwaitAllE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Fun, OkArgs: entry.Args, OuterCall: expr, EntryEllipsis: entry.Ellipsis}, true, nil
		case isSelector(entry.Fun, alias, "AwaitAllCtxE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.AwaitAllCtxE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) < 1 {
				return qSubCall{}, false, fmt.Errorf("q.AwaitAllCtxE must take at least one argument (ctx); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyAwaitAllCtxE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OkArgs: entry.Args, OuterCall: expr, EntryEllipsis: entry.Ellipsis}, true, nil
		case isSelector(entry.Fun, alias, "AwaitAnyE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.AwaitAnyE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			return qSubCall{Family: familyAwaitAnyE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Fun, OkArgs: entry.Args, OuterCall: expr, EntryEllipsis: entry.Ellipsis}, true, nil
		case isSelector(entry.Fun, alias, "AwaitAnyCtxE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.AwaitAnyCtxE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) < 1 {
				return qSubCall{}, false, fmt.Errorf("q.AwaitAnyCtxE must take at least one argument (ctx); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyAwaitAnyCtxE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OkArgs: entry.Args, OuterCall: expr, EntryEllipsis: entry.Ellipsis}, true, nil
		case isSelector(entry.Fun, alias, "RecvAnyE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.RecvAnyE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			return qSubCall{Family: familyRecvAnyE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Fun, OkArgs: entry.Args, OuterCall: expr, EntryEllipsis: entry.Ellipsis}, true, nil
		case isSelector(entry.Fun, alias, "RecvAnyCtxE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.RecvAnyCtxE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) < 1 {
				return qSubCall{}, false, fmt.Errorf("q.RecvAnyCtxE must take at least one argument (ctx); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyRecvAnyCtxE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OkArgs: entry.Args, OuterCall: expr, EntryEllipsis: entry.Ellipsis}, true, nil
		case isSelector(entry.Fun, alias, "DrainCtxE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.DrainCtxE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 2 {
				return qSubCall{}, false, fmt.Errorf("q.DrainCtxE must take exactly two arguments (ctx, ch); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyDrainCtxE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OkArgs: entry.Args, OuterCall: expr}, true, nil
		case isSelector(entry.Fun, alias, "DrainAllCtxE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.DrainAllCtxE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) < 1 {
				return qSubCall{}, false, fmt.Errorf("q.DrainAllCtxE must take at least one argument (ctx); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyDrainAllCtxE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OkArgs: entry.Args, OuterCall: expr, EntryEllipsis: entry.Ellipsis}, true, nil
		}
		// AsE needs a dedicated check because its entry.Fun is an
		// IndexExpr carrying the type argument, not a plain selector
		// that the switch cases above cover.
		if typeArg, ok := isIndexedSelector(entry.Fun, alias, "AsE"); ok {
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.AsE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if len(entry.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.AsE[T] must take exactly one argument (the value to assert); got %d", len(entry.Args))
			}
			return qSubCall{Family: familyAsE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], AsType: typeArg, OuterCall: expr}, true, nil
		}
		switch {
		case isSelector(entry.Fun, alias, "OkE"):
			if !chainMethods[sel.Sel.Name] {
				return qSubCall{}, false, fmt.Errorf("q.OkE chain method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", sel.Sel.Name)
			}
			if err := validateOkArgs("q.OkE", entry.Args); err != nil {
				return qSubCall{}, false, err
			}
			return qSubCall{Family: familyOkE, Method: sel.Sel.Name, MethodArgs: call.Args, InnerExpr: entry.Args[0], OkArgs: entry.Args, OuterCall: expr}, true, nil
		}
	}

	return qSubCall{}, false, nil
}

// peelRecovers walks down through any leading
// .RecoverIs(sentinel, value) / .RecoverAs(typedNil, value) chain
// calls on `entry`, returning the underlying entry call and the
// recover steps in source order. If `entry` itself is not a chain
// (i.e. its .Fun is not a SelectorExpr selecting RecoverIs/RecoverAs),
// returns entry and nil.
//
// `entry` is the outer terminal's `.X` — what would otherwise be
// the entry call directly. If RecoverIs/RecoverAs sit between the
// entry and the terminal, this peels them off.
func peelRecovers(entry *ast.CallExpr) (*ast.CallExpr, []recoverStep, error) {
	var steps []recoverStep
	cur := entry
	for {
		sel, ok := cur.Fun.(*ast.SelectorExpr)
		if !ok {
			return cur, steps, nil
		}
		var kind recoverKind
		switch sel.Sel.Name {
		case "RecoverIs":
			kind = recoverKindIs
		case "RecoverAs":
			kind = recoverKindAs
		default:
			// Not a Recover step — done peeling.
			return cur, steps, nil
		}
		if len(cur.Args) != 2 {
			return nil, nil, fmt.Errorf("q.TryE(...).%s requires exactly two arguments (match target, recovery value); got %d", sel.Sel.Name, len(cur.Args))
		}
		// Prepend so steps end up in source order
		// (innermost-first walked, so build in reverse).
		steps = append([]recoverStep{{
			Kind:     kind,
			MatchArg: cur.Args[0],
			ValueArg: cur.Args[1],
		}}, steps...)
		next, ok := sel.X.(*ast.CallExpr)
		if !ok {
			return nil, nil, fmt.Errorf("q.TryE(...).%s applied to a non-call expression; the chain must reach a q.TryE entry call", sel.Sel.Name)
		}
		cur = next
	}
}

// classifyOpenChain recognises the q.Open / q.OpenE terminal
// Release / NoRelease shape, optionally with one intermediate shape
// method between the entry and the terminal:
//
//	q.Open(call()).Release(cleanup)
//	q.Open(call()).NoRelease()                       // explicit no-cleanup
//	q.OpenE(call()).Release(cleanup)
//	q.OpenE(call()).NoRelease()
//	q.OpenE(call()).<Shape>(args).Release(cleanup)   // Shape ∈ Err/ErrF/Wrap/Wrapf/Catch
//	q.OpenE(call()).<Shape>(args).NoRelease()
//
// call is the outer Release/NoRelease CallExpr; sel is its .Fun
// SelectorExpr (sel.Sel.Name ∈ {"Release", "NoRelease"}). expr is
// the source expression (== call) used for OuterCall span.
// isAssembleEntry reports whether the given CallExpr is the entry
// call of a q.Assemble / q.AssembleAll / q.AssembleStruct chain
// (i.e. the receiver of `.Release()` / `.NoRelease()` in an Assemble
// chain). The differentiator from q.Open is that Assemble-family
// entries are *indexed* selector calls (`q.Assemble[T](...)`) — they
// always carry an explicit type argument.
func isAssembleEntry(call *ast.CallExpr, alias string) bool {
	if _, ok := isIndexedSelector(call.Fun, alias, "Assemble"); ok {
		return true
	}
	if _, ok := isIndexedSelector(call.Fun, alias, "AssembleAll"); ok {
		return true
	}
	if _, ok := isIndexedSelector(call.Fun, alias, "AssembleStruct"); ok {
		return true
	}
	return false
}

// classifyAssembleChain captures one `q.Assemble[T](...).Release()`
// or `.NoRelease()` chain as a single call shape. The receiver
// `entry` is the q.Assemble* call carrying the type argument and
// recipe list; the outer `call` carries the chain method invocation
// (no args; both terminators are zero-arg today).
func classifyAssembleChain(call *ast.CallExpr, sel *ast.SelectorExpr, entry *ast.CallExpr, alias string) (qSubCall, bool, error) {
	if len(call.Args) != 0 {
		return qSubCall{}, false, fmt.Errorf("q.Assemble[T](...).%s takes no arguments; got %d", sel.Sel.Name, len(call.Args))
	}
	chain := assembleChainRelease
	if sel.Sel.Name == "NoRelease" {
		chain = assembleChainNoRelease
	}

	var fam family
	var typeArg ast.Expr
	switch {
	case mustIndexed(entry.Fun, alias, "Assemble"):
		fam = familyAssemble
		typeArg, _ = indexedTypeArg(entry.Fun, alias, "Assemble")
	case mustIndexed(entry.Fun, alias, "AssembleAll"):
		fam = familyAssembleAll
		typeArg, _ = indexedTypeArg(entry.Fun, alias, "AssembleAll")
	case mustIndexed(entry.Fun, alias, "AssembleStruct"):
		fam = familyAssembleStruct
		typeArg, _ = indexedTypeArg(entry.Fun, alias, "AssembleStruct")
	default:
		return qSubCall{}, false, nil
	}
	if len(entry.Args) == 0 {
		return qSubCall{}, false, fmt.Errorf("q.%s[T] requires at least one recipe argument", entryNameForFamily(fam))
	}
	recipes := make([]ast.Expr, len(entry.Args))
	permitNil := make([]bool, len(entry.Args))
	for i, a := range entry.Args {
		inner, isPermit, err := unwrapPermitNil(a, alias)
		if err != nil {
			return qSubCall{}, false, err
		}
		recipes[i] = inner
		permitNil[i] = isPermit
	}
	return qSubCall{
		Family:            fam,
		AsType:            typeArg,
		AssembleRecipes:   recipes,
		AssemblePermitNil: permitNil,
		AssembleChain:     chain,
		OuterCall:         ast.Expr(call),
	}, true, nil
}

// unwrapPermitNil detects `q.PermitNil(x)` (or its `q.PermitNil[T](x)`
// explicit-type-arg form) and returns (x, true, nil). For any other
// expression returns (expr, false, nil) unchanged. A `q.PermitNil()`
// call with the wrong number of arguments is rejected as a scanner
// error so the user gets a precise diagnostic at the call site.
func unwrapPermitNil(e ast.Expr, alias string) (ast.Expr, bool, error) {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return e, false, nil
	}
	// q.PermitNil(x) — selector form.
	if isSelector(call.Fun, alias, "PermitNil") {
		if len(call.Args) != 1 {
			return e, false, fmt.Errorf("q.PermitNil(recipe) takes exactly 1 argument; got %d", len(call.Args))
		}
		return call.Args[0], true, nil
	}
	// q.PermitNil[T](x) — explicit type-arg form via IndexExpr /
	// IndexListExpr (Go 1.18+).
	if _, ok := isIndexedSelector(call.Fun, alias, "PermitNil"); ok {
		if len(call.Args) != 1 {
			return e, false, fmt.Errorf("q.PermitNil[T](recipe) takes exactly 1 argument; got %d", len(call.Args))
		}
		return call.Args[0], true, nil
	}
	return e, false, nil
}

// mustIndexed is a tiny helper around isIndexedSelector — returns
// just the bool so the switch above reads cleanly.
func mustIndexed(fn ast.Expr, alias, name string) bool {
	_, ok := isIndexedSelector(fn, alias, name)
	return ok
}

// indexedTypeArg extracts the type argument from an indexed
// selector. Returns (nil, false) if the shape doesn't match —
// callers paired with mustIndexed before calling, so this is a
// straight-line extraction in practice.
func indexedTypeArg(fn ast.Expr, alias, name string) (ast.Expr, bool) {
	return isIndexedSelector(fn, alias, name)
}

// entryNameForFamily returns the user-facing q.* name (without the
// `q.` prefix) for an Assemble-family family. Used in scanner error
// messages so the user sees the entry they wrote.
func entryNameForFamily(f family) string {
	switch f {
	case familyAssembleAll:
		return "AssembleAll"
	case familyAssembleStruct:
		return "AssembleStruct"
	}
	return "Assemble"
}

func classifyOpenChain(call *ast.CallExpr, sel *ast.SelectorExpr, alias string) (qSubCall, bool, error) {
	expr := ast.Expr(call)
	noRelease := sel.Sel.Name == "NoRelease"

	var (
		releaseArg  ast.Expr
		autoRelease bool
	)
	switch {
	case noRelease:
		if len(call.Args) != 0 {
			return qSubCall{}, false, fmt.Errorf("q.Open/OpenE(...).NoRelease takes no arguments; got %d", len(call.Args))
		}
	case len(call.Args) == 0:
		// .Release() with no args — preprocessor infers the cleanup
		// from the resource type at compile time.
		autoRelease = true
	case len(call.Args) == 1:
		releaseArg = call.Args[0]
	default:
		return qSubCall{}, false, fmt.Errorf("q.Open/OpenE(...).Release accepts at most one cleanup function; got %d", len(call.Args))
	}

	inner, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return qSubCall{}, false, nil
	}

	// Case 1: inner is q.Open(x) or q.OpenE(x) directly — no shape.
	if family, entry, ok := matchOpenEntry(inner, alias); ok {
		if len(entry.Args) != 1 {
			return qSubCall{}, false, fmt.Errorf("q.Open/OpenE must take exactly one argument (a (T, error)-returning call); got %d", len(entry.Args))
		}
		if _, isCall := entry.Args[0].(*ast.CallExpr); !isCall {
			return qSubCall{}, false, fmt.Errorf("q.Open/OpenE's argument must itself be a call expression returning (T, error)")
		}
		return qSubCall{
			Family:      family,
			InnerExpr:   entry.Args[0],
			OuterCall:   expr,
			ReleaseArg:  releaseArg,
			NoRelease:   noRelease,
			AutoRelease: autoRelease,
		}, true, nil
	}

	// Case 2: inner is a shape-method call on q.OpenE: q.OpenE(x).<Shape>(args).<Release|NoRelease>().
	shapeSel, ok := inner.Fun.(*ast.SelectorExpr)
	if !ok {
		return qSubCall{}, false, nil
	}
	if !chainMethods[shapeSel.Sel.Name] {
		return qSubCall{}, false, fmt.Errorf("q.OpenE shape method %q not recognised; valid: Err, ErrF, Catch, Wrap, Wrapf", shapeSel.Sel.Name)
	}
	entryCall, ok := shapeSel.X.(*ast.CallExpr)
	if !ok {
		return qSubCall{}, false, nil
	}
	family, entry, ok := matchOpenEntry(entryCall, alias)
	if !ok || family != familyOpenE {
		// Shape methods are only valid on OpenE; bare Open's
		// type doesn't expose them. If we got familyOpen here,
		// the user's source won't type-check — let Go tell them.
		return qSubCall{}, false, nil
	}
	if len(entry.Args) != 1 {
		return qSubCall{}, false, fmt.Errorf("q.OpenE must take exactly one argument (a (T, error)-returning call); got %d", len(entry.Args))
	}
	if _, isCall := entry.Args[0].(*ast.CallExpr); !isCall {
		return qSubCall{}, false, fmt.Errorf("q.OpenE's argument must itself be a call expression returning (T, error)")
	}
	return qSubCall{
		Family:      familyOpenE,
		Method:      shapeSel.Sel.Name,
		MethodArgs:  inner.Args,
		InnerExpr:   entry.Args[0],
		OuterCall:   expr,
		ReleaseArg:  releaseArg,
		NoRelease:   noRelease,
		AutoRelease: autoRelease,
	}, true, nil
}

// scanContainerInit scans an Init / Post sub-statement of an
// IfStmt / ForStmt / SwitchStmt. The sub-stmt sits inside the
// container's header — `if v := q.X(); cond { … }` — so the rewrite
// must be a single-line span substitution to keep the header valid.
//
// Supported: in-place families only (q.EnumName, q.F, q.SQL, etc.).
// They rewrite to a same-line expression, so the substituted Init
// remains a valid SimpleStmt.
//
// Bubble families (q.Try, q.NotNil, q.Check, q.Recv, …) would need
// to inject a multi-line bind+check block — illegal inside the
// container header. For those, the user must extract the call to a
// preceding statement. Diagnostic guides them there. The keyword
// arg ("if" / "for" / "switch") is interpolated into the message
// for clarity.
func scanContainerInit(fset *token.FileSet, path string, stmt ast.Stmt, alias string, fnType *ast.FuncType, shapes *[]callShape, diags *[]Diagnostic, kw string) {
	shape, ok, err := matchStatement(stmt, alias, fnType)
	if err != nil {
		*diags = append(*diags, diagAt(fset, path, stmt.Pos(), err.Error()))
		return
	}
	if !ok {
		// No q.* matched. If a q.* reference exists in an
		// unsupported sub-shape, the regular fall-through
		// diagnostic in walkBlock would catch it — but we're not
		// in walkBlock. Produce the same diagnostic here.
		if !isContainerStmt(stmt) {
			if pos := findQReference(stmt, alias); pos.IsValid() {
				*diags = append(*diags, diagAt(fset, path, pos,
					fmt.Sprintf("unsupported q.* call shape inside %s-statement init/post", kw)))
			}
		}
		return
	}
	for _, sc := range shape.Calls {
		if !isInPlaceFamily(sc.Family) {
			pos := fset.Position(sc.OuterCall.Pos())
			*diags = append(*diags, Diagnostic{
				File: pos.Filename,
				Line: pos.Line,
				Col:  pos.Column,
				Msg:  fmt.Sprintf("q: bubble-shape q.* (%s) is not supported inside the %s-statement header — the rewrite would inject a multi-line bind+check that breaks the header. Extract the call to a preceding statement: `v, err := …; if err != nil { … }`", familyDisplayName(sc.Family), kw),
			})
			return
		}
	}
	*shapes = append(*shapes, shape)
}

// scanContainerExpr walks the expression position(s) of a container
// statement (RangeStmt.X, IfStmt.Cond, ForStmt.Cond, SwitchStmt.Tag)
// for q.* calls. Only IN-PLACE families are supported in these
// positions: their span substitutes cleanly into the container's
// header. Bubble families would need to inject a multi-line
// bind+check block, which has no place inside a header expression —
// those produce a diagnostic asking the user to extract the call.
//
// One shape is emitted PER q.* call (not per container) so the
// edit span is scoped to just the q.*'s OuterCall — without this,
// the container's body would be re-emitted by the in-place
// substitution, overlapping with edits the body's own statements
// generate and corrupting the rewrite.
//
// The synthetic *ast.ExprStmt wrapper ensures Stmt.Pos/End match
// the q.*'s OuterCall span; renderShape's all-in-place branch then
// emits a span-only substitution at exactly that range.
func scanContainerExpr(fset *token.FileSet, path string, container ast.Stmt, exprs []ast.Expr, alias string, fnType *ast.FuncType, shapes *[]callShape, diags *[]Diagnostic, kw string) {
	subs, err := collectQCalls(exprs, alias)
	if err != nil {
		*diags = append(*diags, diagAt(fset, path, container.Pos(), err.Error()))
		return
	}
	if len(subs) == 0 {
		return
	}
	for _, sc := range subs {
		if !isInPlaceFamily(sc.Family) {
			pos := fset.Position(sc.OuterCall.Pos())
			*diags = append(*diags, Diagnostic{
				File: pos.Filename,
				Line: pos.Line,
				Col:  pos.Column,
				Msg:  fmt.Sprintf("q: bubble-shape q.* (%s) is not supported inside the %s-statement header — the rewrite would inject a multi-line bind+check that breaks the header. Extract the call to a preceding statement: `v, err := …; if err != nil { … }; %s … { … }`", familyDisplayName(sc.Family), kw, kw),
			})
			return
		}
	}
	for i := range subs {
		// Synthesize a per-call ExprStmt so the rewriter's
		// edit-span machinery sees the q.* call's exact byte
		// range. The ExprStmt isn't part of the source AST; it's
		// only used for Pos/End.
		synthetic := &ast.ExprStmt{X: subs[i].OuterCall}
		*shapes = append(*shapes, callShape{
			Stmt:              synthetic,
			Form:              formDiscard,
			Calls:             []qSubCall{subs[i]},
			EnclosingFuncType: fnType,
		})
	}
}

// familyDisplayName returns the user-facing q.* name for a family —
// used in diagnostic messages so users can map directly to docs.
func familyDisplayName(f family) string {
	switch f {
	case familyTry:
		return "q.Try"
	case familyTryE:
		return "q.TryE"
	case familyNotNil:
		return "q.NotNil"
	case familyNotNilE:
		return "q.NotNilE"
	case familyOk:
		return "q.Ok"
	case familyOkE:
		return "q.OkE"
	case familyCheck:
		return "q.Check"
	case familyCheckE:
		return "q.CheckE"
	case familyOpen:
		return "q.Open"
	case familyOpenE:
		return "q.OpenE"
	case familyRecv:
		return "q.Recv"
	case familyRecvE:
		return "q.RecvE"
	case familyAs:
		return "q.As"
	case familyAsE:
		return "q.AsE"
	case familyTrace:
		return "q.Trace"
	case familyTraceE:
		return "q.TraceE"
	case familyAwait:
		return "q.Await"
	case familyAwaitE:
		return "q.AwaitE"
	case familyCheckCtx:
		return "q.CheckCtx"
	case familyCheckCtxE:
		return "q.CheckCtxE"
	case familyRequire:
		return "q.Require"
	}
	return "q.*"
}

// matchExhaustiveSwitch detects the
//
//	switch q.Exhaustive(v) { case … }
//
// shape and returns a callShape that wraps the entire SwitchStmt as
// its Stmt with a single q.Exhaustive sub-call. Returns
// (zero, false, nil) when the switch's tag isn't a `q.Exhaustive`
// call (or when the switch has no tag at all). Returns an error
// when the tag matches the q.Exhaustive *selector* but the call is
// malformed (wrong arity), which becomes a diagnostic.
//
// The rewriter handles this shape via the existing all-in-place
// pathway: q.Exhaustive's span gets replaced by the inner expr's
// source text, and the rest of the switch source survives intact.
func matchExhaustiveSwitch(s *ast.SwitchStmt, alias string, fnType *ast.FuncType) (callShape, bool, error) {
	if s.Tag == nil {
		return callShape{}, false, nil
	}
	call, ok := s.Tag.(*ast.CallExpr)
	if !ok {
		return callShape{}, false, nil
	}
	if !isSelector(call.Fun, alias, "Exhaustive") {
		return callShape{}, false, nil
	}
	if len(call.Args) != 1 {
		return callShape{}, false, fmt.Errorf("q.Exhaustive must take exactly one argument (the value to switch on); got %d", len(call.Args))
	}
	return callShape{
		Stmt:              s,
		Form:              formHoist,
		Calls:             []qSubCall{{Family: familyExhaustive, InnerExpr: call.Args[0], OuterCall: call}},
		EnclosingFuncType: fnType,
	}, true, nil
}

// parseMatchArms walks q.Match's tail arguments (each expected to be
// a q.Case or q.Default call) and extracts the cond/result
// expressions. The typecheck pass classifies each arm later, by
// inspecting cond's resolved type — see resolveMatch.
//
// At most one q.Default arm is allowed.
func parseMatchArms(args []ast.Expr, alias string) ([]matchCase, error) {
	cases := make([]matchCase, 0, len(args))
	defaultSeen := false
	for i, a := range args {
		call, ok := a.(*ast.CallExpr)
		if !ok {
			return nil, fmt.Errorf("q.Match argument %d is not a q.Case / q.Default call", i+1)
		}
		switch {
		case isSelector(call.Fun, alias, "Case"):
			if len(call.Args) != 2 {
				return nil, fmt.Errorf("q.Case must take exactly two arguments (cond, result); got %d", len(call.Args))
			}
			cases = append(cases, matchCase{
				CondExpr:   call.Args[0],
				ResultExpr: call.Args[1],
			})
		case isSelector(call.Fun, alias, "Default"):
			if defaultSeen {
				return nil, fmt.Errorf("q.Match has more than one default arm; at most one q.Default is allowed")
			}
			if len(call.Args) != 1 {
				return nil, fmt.Errorf("q.Default must take exactly one argument (the default result); got %d", len(call.Args))
			}
			cases = append(cases, matchCase{
				ResultExpr: call.Args[0],
				IsDefault:  true,
			})
			defaultSeen = true
		default:
			return nil, fmt.Errorf("q.Match argument %d is not a q.Case / q.Default call", i+1)
		}
	}
	return cases, nil
}

// stringCaseFamilies pairs the q-aliased name with its scanner family
// for the compile-time string-case ops. Order doesn't matter for
// correctness; only the names are matched.
var stringCaseFamilies = []struct {
	name string
	fam  family
}{
	{"Upper", familyUpper},
	{"Lower", familyLower},
	{"Snake", familySnake},
	{"Kebab", familyKebab},
	{"Camel", familyCamel},
	{"Pascal", familyPascal},
	{"Title", familyTitle},
}

// validateStringLiteralArg enforces that the call has exactly one
// string-literal argument. Used by the compile-time string-case ops
// where dynamic input would defeat the whole point.
func validateStringLiteralArg(name string, args []ast.Expr) error {
	if len(args) != 1 {
		return fmt.Errorf("%s takes exactly one argument (a string literal); got %d", name, len(args))
	}
	lit, ok := args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return fmt.Errorf("%s's argument must be a Go string literal; dynamic strings are not supported (use the standard `strings` package for those)", name)
	}
	return nil
}

// validateFLiteral enforces that q.F / q.Ferr / q.Fln have a single
// string-literal argument. Dynamic format strings would defeat
// compile-time placeholder extraction (and, for q.SQL, re-open the
// injection hole the helper exists to close).
func validateFLiteral(name string, args []ast.Expr) error {
	if len(args) != 1 {
		return fmt.Errorf("%s takes exactly one argument (the format string literal); got %d", name, len(args))
	}
	lit, ok := args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return fmt.Errorf("%s's argument must be a Go string literal; dynamic format strings are not supported (use fmt.Sprintf for those)", name)
	}
	return nil
}

// validateOkArgs enforces Ok / OkE's two valid arg shapes: one
// CallExpr returning (T, bool), or two separate expressions (T, bool).
// The rewriter reads the source span from Args[0].Pos() to
// Args[last].End(), so either shape drops straight into a tuple-
// binding `<LHS>, _qOkN := <span>` line.
func validateOkArgs(name string, args []ast.Expr) error {
	switch len(args) {
	case 1:
		if _, ok := args[0].(*ast.CallExpr); !ok {
			return fmt.Errorf("%s's single argument must be a call expression returning (T, bool); pass two separate arguments (value, ok) otherwise", name)
		}
		return nil
	case 2:
		return nil
	default:
		return fmt.Errorf("%s must take one (T, bool)-returning call or two arguments (value, ok); got %d", name, len(args))
	}
}

// matchOpenEntry reports whether c is a direct q.Open / q.OpenE call
// under the local alias, and which family it belongs to.
func matchOpenEntry(c *ast.CallExpr, alias string) (family, *ast.CallExpr, bool) {
	if isSelector(c.Fun, alias, "Open") {
		return familyOpen, c, true
	}
	if isSelector(c.Fun, alias, "OpenE") {
		return familyOpenE, c, true
	}
	return 0, nil, false
}

// recoverEChainMethods enumerates the chain methods the rewriter
// knows how to splice &err into for `defer q.RecoverE().X(args)`.
// Superset of chainMethods — RecoverE has .Map which the bubble
// families do not.
var recoverEChainMethods = map[string]bool{
	"Map":   true,
	"Err":   true,
	"ErrF":  true,
	"Wrap":  true,
	"Wrapf": true,
}

// classifyDeferredRecover recognises the two auto-Recover shapes:
//
//	q.Recover()                     — no args  → familyRecoverAuto
//	q.RecoverE().<Method>(args...)  — no args on RecoverE, valid chain method → familyRecoverEAuto
//
// Returns (_, false, nil) when the call doesn't match either shape;
// the caller treats that as "not our form" and falls back to the
// existing runtime-helper path (qRuntimeHelpers skip).
func classifyDeferredRecover(call *ast.CallExpr, alias string) (qSubCall, bool, error) {
	// Bare form: defer q.Recover()
	if isSelector(call.Fun, alias, "Recover") && len(call.Args) == 0 {
		return qSubCall{Family: familyRecoverAuto, OuterCall: call}, true, nil
	}
	// Chain form: defer q.RecoverE().Method(args)
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return qSubCall{}, false, nil
	}
	entry, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return qSubCall{}, false, nil
	}
	if !isSelector(entry.Fun, alias, "RecoverE") || len(entry.Args) != 0 {
		return qSubCall{}, false, nil
	}
	if !recoverEChainMethods[sel.Sel.Name] {
		return qSubCall{}, false, fmt.Errorf("q.RecoverE chain method %q not recognised; valid: Map, Err, ErrF, Wrap, Wrapf", sel.Sel.Name)
	}
	return qSubCall{
		Family:     familyRecoverEAuto,
		Method:     sel.Sel.Name,
		MethodArgs: call.Args,
		OuterCall:  call,
	}, true, nil
}

// isIndexedSelector reports whether expr has the shape
// `<alias>.<name>[<typeArg>]` (a generic call with an explicit type
// argument). Returns the type-argument expression plus ok=true on
// match, nil + false otherwise. Handles the single-type-arg case
// only — q.As[T](x), q.AsE[T](x).
func isIndexedSelector(expr ast.Expr, alias, name string) (ast.Expr, bool) {
	ix, ok := expr.(*ast.IndexExpr)
	if !ok {
		return nil, false
	}
	if !isSelector(ix.X, alias, name) {
		return nil, false
	}
	return ix.Index, true
}

// isSelector reports whether expr has the shape `<alias>.<name>`.
func isSelector(expr ast.Expr, alias, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return x.Name == alias && sel.Sel.Name == name
}

// findQReference walks a statement's AST and returns the position of
// any q.* CALL in an unsupported position, or an invalid token.Pos
// if none is found. Only calls are flagged — plain value references
// to exported q identifiers (e.g. `errors.Is(err, q.ErrNil)`) are
// legitimate uses that don't need rewriting.
//
// A call is "rooted at q" if its Fun chain (through any number of
// `.<method>` selectors on sub-CallExprs) eventually resolves to an
// Ident named alias. That catches both the bare form `q.Try(x)` and
// the chain form `q.TryE(x).Method(y)` whose outer Fun's leftmost
// ident is still q.
//
// Bounded: descent stops at any nested *ast.BlockStmt or FuncLit.
// Nested blocks are scanned separately by walkChildBlocks; FuncLit
// bodies are scanned by walkFuncLits with their own scope. Without
// these bounds, a recognised `v := q.Try(call())` inside an if-body
// or a closure would also be flagged as "unsupported" against the
// enclosing container.
func findQReference(stmt ast.Stmt, alias string) token.Pos {
	var found token.Pos
	ast.Inspect(stmt, func(n ast.Node) bool {
		if blk, ok := n.(*ast.BlockStmt); ok && ast.Node(blk) != ast.Node(stmt) {
			return false
		}
		if _, isLit := n.(*ast.FuncLit); isLit {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if pos := qCallRootPos(call, alias); pos.IsValid() {
			found = pos
			return false
		}
		return true
	})
	return found
}

// qCallRootPos reports the position of call's outer selector when
// call is rooted at the q alias (directly or through a chain),
// or token.NoPos otherwise.
//
// Calls whose entry name (the segment immediately after the alias)
// is in qRuntimeHelpers are treated as non-q for flagging purposes
// — they are plain runtime helpers the preprocessor never rewrites,
// so a standalone `q.ToErr(...)` should not trip the
// "unsupported shape" diagnostic.
func qCallRootPos(call *ast.CallExpr, alias string) token.Pos {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return token.NoPos
	}
	// entryName tracks the segment immediately after the alias.
	// For bare `q.Try(...)`, entryName == sel.Sel.Name. For a
	// chain like `q.TryE(...).Wrap(...)`, entryName is picked up
	// when the walk reaches the inner call (q.TryE) whose .X is
	// the alias ident.
	entryName := sel.Sel.Name
	root := sel.X
	for {
		switch v := root.(type) {
		case *ast.Ident:
			if v.Name != alias {
				return token.NoPos
			}
			if qRuntimeHelpers[entryName] {
				return token.NoPos
			}
			return sel.Pos()
		case *ast.CallExpr:
			innerSel, ok := v.Fun.(*ast.SelectorExpr)
			if !ok {
				return token.NoPos
			}
			entryName = innerSel.Sel.Name
			root = innerSel.X
		default:
			return token.NoPos
		}
	}
}

// unquote strips the surrounding quotes from a Go string literal as
// found in *ast.BasicLit.Value. Avoids the strconv.Unquote dependency
// for the simple ASCII-only import-path case.
func unquote(lit string) (string, error) {
	if len(lit) < 2 || lit[0] != '"' || lit[len(lit)-1] != '"' {
		return "", fmt.Errorf("invalid string literal %q", lit)
	}
	return lit[1 : len(lit)-1], nil
}

// diagAt builds a Diagnostic for a position in the named file.
func diagAt(fset *token.FileSet, path string, pos token.Pos, msg string) Diagnostic {
	p := fset.Position(pos)
	if p.Filename == "" {
		p.Filename = path
	}
	return Diagnostic{
		File: p.Filename,
		Line: p.Line,
		Col:  p.Column,
		Msg:  "q: " + strings.TrimPrefix(msg, "q: "),
	}
}
