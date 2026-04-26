package preprocessor

// tern.go — typecheck + rewriter for q.Tern[T](cond bool, ifTrue, ifFalse T) T.
//
// q.Tern's surface looks like a regular function call, but the
// preprocessor rewrites it to an IIFE that evaluates only the
// branch matching `cond`:
//
//	(func() T {
//	    if <condText> {
//	        return <ifTrueText>
//	    }
//	    return <ifFalseText>
//	}())
//
// Source-splicing the branches into the IIFE side-steps Go's
// standard argument-evaluation semantics (which would evaluate
// both branches unconditionally if q.Tern were a real runtime
// function). That's what makes q.Tern a true conditional expression
// rather than a function call masquerading as one — and lets nested
// q.Tern calls chain naturally for multi-way picks.
//
// resolveTern just records T's spelling under the call's package
// qualifier; there's no lazy/eager classification because the
// signature is single-shape.

import (
	"fmt"
	"go/token"
	"go/types"
)

// resolveTern populates sc.TernResultTypeText from the resolved
// type-arg T. Two source forms feed in:
//
//   - q.Tern[T](cond, ifTrue, ifFalse) — explicit type arg;
//     sc.AsType is the AST node for T, and we read its type from
//     info.Types.
//   - q.Tern(cond, ifTrue, ifFalse)    — implicit; sc.AsType is
//     nil, and we infer T from the resolved type of ifTrue
//     (sc.TernThen). Go's type inference ensures the call
//     type-checks (both branches must agree); the rewriter just
//     needs the spelling of T to emit the IIFE return type.
//
// Returns no diagnostic — Go's own type-checker will flag bad
// branch types via the strict signature.
func resolveTern(_ *token.FileSet, sc *qSubCall, info *types.Info, pkgPath string) (Diagnostic, bool) {
	var resolved types.Type
	if sc.AsType != nil {
		if tv, ok := info.Types[sc.AsType]; ok && tv.Type != nil {
			resolved = tv.Type
		}
	}
	if resolved == nil && sc.TernThen != nil {
		if tv, ok := info.Types[sc.TernThen]; ok && tv.Type != nil {
			resolved = tv.Type
		}
	}
	if resolved == nil {
		return Diagnostic{}, false
	}
	qualifier := func(p *types.Package) string {
		if p == nil || p.Path() == pkgPath {
			return ""
		}
		return p.Name()
	}
	sc.TernResultTypeText = types.TypeString(resolved, qualifier)
	return Diagnostic{}, false
}

// buildTernReplacement emits the IIFE for q.Tern. Returns the text
// to splice in place of the original q.Tern(...) call.
func buildTernReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string) string {
	resultText := sub.TernResultTypeText
	if resultText == "" {
		resultText = "any"
	}
	condText := exprTextSubst(fset, src, sub.TernCond, subs, subTexts)
	thenText := exprTextSubst(fset, src, sub.TernThen, subs, subTexts)
	elseText := exprTextSubst(fset, src, sub.TernElse, subs, subTexts)
	return fmt.Sprintf("(func() %s { if %s { return %s }; return %s }())",
		resultText, condText, thenText, elseText)
}
