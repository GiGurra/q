package preprocessor

// lazy.go — typecheck + rewriter for q.Lazy[T](v).
//
// q.Lazy(v) reads as eager Go but the rewriter wraps the arg
// expression in a thunk closure so evaluation defers until the first
// .Value() call. Same source-splicing trick as q.Tern, with one
// branch instead of three.
//
// Surface (one entry, runtime helper struct):
//
//	q.Lazy[T any](v T) *Lazy[T]
//	(*Lazy[T]).Value() T          // sync.Once-backed first-eval
//	(*Lazy[T]).IsForced() bool    // diagnostic
//
// Rewriter output for `q.Lazy(<expr>)`:
//
//	q.LazyFromThunk(func() T { return <expr> })
//
// where q.LazyFromThunk is the real runtime constructor (exported but
// not advertised in the public API; callers always go through q.Lazy).

import (
	"fmt"
	"go/token"
	"go/types"
)

// resolveLazy populates LazyTypeText from go/types info.
//
// For q.Lazy(v): T is the value expression's type.
// For q.LazyE(call()): T is the *first* return type of call() — the
// rewriter emits a thunk returning (T, error), so we need T's spelling
// independently from error.
func resolveLazy(_ *token.FileSet, sc *qSubCall, info *types.Info, pkgPath string) (Diagnostic, bool) {
	qualifier := func(p *types.Package) string {
		if p == nil || p.Path() == pkgPath {
			return ""
		}
		return p.Name()
	}
	var resolved types.Type
	if sc.AsType != nil {
		if tv, ok := info.Types[sc.AsType]; ok && tv.Type != nil {
			resolved = tv.Type
		}
	}
	if resolved == nil && sc.LazyExpr != nil {
		if tv, ok := info.Types[sc.LazyExpr]; ok && tv.Type != nil {
			if sc.Family == familyLazyE {
				// (T, error) call: extract T from the tuple.
				if tup, ok := tv.Type.(*types.Tuple); ok && tup.Len() >= 1 {
					resolved = tup.At(0).Type()
				} else {
					resolved = tv.Type
				}
			} else {
				resolved = tv.Type
			}
		}
	}
	if resolved != nil {
		sc.LazyTypeText = types.TypeString(resolved, qualifier)
	}
	return Diagnostic{}, false
}

// buildLazyReplacement emits the rewritten call.
//
// q.Lazy(<expr>):
//
//	q.LazyFromThunk(func() T { return <expr> })
//
// q.LazyE(<call>):
//
//	q.LazyEFromThunk(func() (T, error) { return <call> })
//
// The thunk captures whatever locals the spliced expression referenced
// (normal Go closure semantics). T spelling drives the closure's
// first return type; the LazyE form pairs T with error.
func buildLazyReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string, alias string) string {
	exprText := exprTextSubst(fset, src, sub.LazyExpr, subs, subTexts)
	typeText := sub.LazyTypeText
	if typeText == "" {
		typeText = "any"
	}
	if sub.Family == familyLazyE {
		return fmt.Sprintf("%s.LazyEFromThunk(func() (%s, error) { return %s })", alias, typeText, exprText)
	}
	return fmt.Sprintf("%s.LazyFromThunk(func() %s { return %s })", alias, typeText, exprText)
}
