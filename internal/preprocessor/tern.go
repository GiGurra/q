package preprocessor

// tern.go — typecheck + rewriter for q.Tern[T](cond bool, t T) T.
//
// q.Tern's surface looks like a regular function call, but the
// preprocessor rewrites it to an IIFE that evaluates `t` only when
// `cond` is true:
//
//	(func() T {
//	    if <condText> {
//	        return <tText>
//	    }
//	    var _zero T
//	    return _zero
//	}())
//
// Source-splicing the args into the IIFE side-steps Go's standard
// argument-evaluation semantics (which would evaluate `t`
// unconditionally if q.Tern were a real runtime function). That's
// what makes q.Tern a true conditional expression rather than a
// function call masquerading as one.
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
// type-arg T. Returns no diagnostic — Go's own type-checker will
// flag bad cond / t types via the strict signature.
func resolveTern(_ *token.FileSet, sc *qSubCall, info *types.Info, pkgPath string) (Diagnostic, bool) {
	if sc.AsType == nil {
		return Diagnostic{}, false
	}
	tv, ok := info.Types[sc.AsType]
	if !ok || tv.Type == nil {
		return Diagnostic{}, false
	}
	qualifier := func(p *types.Package) string {
		if p == nil || p.Path() == pkgPath {
			return ""
		}
		return p.Name()
	}
	sc.TernResultTypeText = types.TypeString(tv.Type, qualifier)
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
	tText := exprTextSubst(fset, src, sub.TernT, subs, subTexts)
	return fmt.Sprintf("(func() %s { if %s { return %s }; var _zero %s; return _zero }())",
		resultText, condText, tText, resultText)
}
