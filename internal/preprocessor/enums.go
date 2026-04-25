package preprocessor

// enums.go — rewriter helpers for the q.Enum* family. Each
// q.Enum*[T](...) call site rewrites in place to a literal slice or
// an inline switch expression. The constants of T are discovered by
// the typecheck pass (resolveEnum in typecheck.go) and stored on the
// qSubCall as EnumConsts (names, source declaration order) and
// EnumTypeText (the type's name as it appears unqualified in the
// declaring package).
//
// All rewrites are syntactically self-contained expressions so they
// drop into any expression position — define / assign / discard /
// return / hoist — without further plumbing.

import (
	"fmt"
	"go/token"
	"strconv"
	"strings"
)

// buildEnumValuesReplacement emits a literal slice of T's constants
// in declaration order, e.g. `[]Color{Red, Green, Blue}`. With no
// resolved consts (typecheck pass missing or failed silently) the
// result is `[]Color(nil)` — the panic body in pkg/q would normally
// catch this, but since we return a value here we need a syntactic
// fallback that compiles.
func buildEnumValuesReplacement(sub qSubCall) string {
	if len(sub.EnumConsts) == 0 || sub.EnumTypeText == "" {
		return fmt.Sprintf("[]%s(nil)", enumTypeText(sub))
	}
	return fmt.Sprintf("[]%s{%s}", sub.EnumTypeText, strings.Join(sub.EnumConsts, ", "))
}

// buildEnumNamesReplacement emits a literal []string of constant
// names in declaration order.
func buildEnumNamesReplacement(sub qSubCall) string {
	if len(sub.EnumConsts) == 0 {
		return "[]string(nil)"
	}
	parts := make([]string, len(sub.EnumConsts))
	for i, n := range sub.EnumConsts {
		parts[i] = strconv.Quote(n)
	}
	return "[]string{" + strings.Join(parts, ", ") + "}"
}

// buildEnumNameReplacement emits an IIFE that switches on the
// argument and returns the matching name (or "" on miss).
func buildEnumNameReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string) string {
	argText := exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	t := enumTypeText(sub)
	if len(sub.EnumConsts) == 0 {
		return fmt.Sprintf("(func(_v %s) string { return \"\" }(%s))", t, argText)
	}
	cases := make([]string, len(sub.EnumConsts))
	for i, n := range sub.EnumConsts {
		cases[i] = fmt.Sprintf("case %s: return %s", n, strconv.Quote(n))
	}
	return fmt.Sprintf("(func(_v %s) string { switch _v { %s }; return \"\" }(%s))",
		t, strings.Join(cases, "; "), argText)
}

// buildEnumParseReplacement emits an IIFE that switches on the
// argument string and returns the matching constant + nil, or
// (zero, q.ErrEnumUnknown wrapped via fmt.Errorf with the input).
func buildEnumParseReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string, alias string) string {
	argText := exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	t := enumTypeText(sub)
	notFound := fmt.Sprintf("fmt.Errorf(\"%%q: %%w\", _s, %s.ErrEnumUnknown)", alias)
	if len(sub.EnumConsts) == 0 {
		return fmt.Sprintf("(func(_s string) (%s, error) { return *new(%s), %s }(%s))", t, t, notFound, argText)
	}
	cases := make([]string, len(sub.EnumConsts))
	for i, n := range sub.EnumConsts {
		cases[i] = fmt.Sprintf("case %s: return %s, nil", strconv.Quote(n), n)
	}
	return fmt.Sprintf("(func(_s string) (%s, error) { switch _s { %s }; return *new(%s), %s }(%s))",
		t, strings.Join(cases, "; "), t, notFound, argText)
}

// buildEnumValidReplacement emits an IIFE that switches on the
// argument and returns true for any known constant, false otherwise.
func buildEnumValidReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string) string {
	argText := exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	t := enumTypeText(sub)
	if len(sub.EnumConsts) == 0 {
		return fmt.Sprintf("(func(_v %s) bool { return false }(%s))", t, argText)
	}
	return fmt.Sprintf("(func(_v %s) bool { switch _v { case %s: return true }; return false }(%s))",
		t, strings.Join(sub.EnumConsts, ", "), argText)
}

// buildEnumOrdinalReplacement emits an IIFE that switches on the
// argument and returns the 0-based index of the matching constant,
// or -1 on miss.
func buildEnumOrdinalReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string) string {
	argText := exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	t := enumTypeText(sub)
	if len(sub.EnumConsts) == 0 {
		return fmt.Sprintf("(func(_v %s) int { return -1 }(%s))", t, argText)
	}
	cases := make([]string, len(sub.EnumConsts))
	for i, n := range sub.EnumConsts {
		cases[i] = fmt.Sprintf("case %s: return %d", n, i)
	}
	return fmt.Sprintf("(func(_v %s) int { switch _v { %s }; return -1 }(%s))",
		t, strings.Join(cases, "; "), argText)
}

// buildMatchReplacement emits an IIFE-wrapped switch (or if-chain)
// for q.Match.
//
// Two output shapes:
//
//	switch shape (every arm's cond is value-typed — no predicate):
//	  (func() R {
//	      switch <value> {
//	      case <cond1>: return <result1>
//	      default:      return <defaultResult>  // when q.Default is present
//	      }
//	      var _zero R; return _zero             // when no q.Default
//	  }())
//
//	if-chain shape (at least one arm has a bool / func() bool cond):
//	  (func() R {
//	      _v := <value>                          // bound only if any value-match arm exists
//	      if <pred1>            { return <r1> } // bool cond — emitted verbatim
//	      if _v == <caseVal2>   { return <r2> } // value match mixed in if-chain
//	      if (<predFn>)()       { return <r3> } // func() bool cond — called lazily
//	      return <defaultResult>
//	  }())
//
// R's type text comes from sub.ResolvedString. When missing, falls
// back to `any`.
func buildMatchReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string) string {
	valueText := exprTextSubst(fset, src, sub.InnerExpr, subs, subTexts)
	resultType := sub.ResolvedString
	if resultType == "" {
		resultType = "any"
	}

	hasPredicate := false
	for _, mc := range sub.MatchCases {
		if mc.IsPredicate {
			hasPredicate = true
			break
		}
	}
	if hasPredicate {
		return buildMatchIfChain(fset, src, sub, subs, subTexts, valueText, resultType)
	}
	return buildMatchSwitch(fset, src, sub, subs, subTexts, valueText, resultType)
}

// buildMatchSwitch is the all-value-equality shape — emits a Go
// `switch` expression inside an IIFE. Used when every non-default
// arm is a value match (no q.Case with bool / func()bool cond).
func buildMatchSwitch(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string, valueText, resultType string) string {
	var caseLines []string
	var defaultText string
	hasDefault := false
	for _, mc := range sub.MatchCases {
		resText := exprTextSubst(fset, src, mc.ResultExpr, subs, subTexts)
		if mc.IsDefault {
			defaultText = resText
			hasDefault = true
			continue
		}
		condText := exprTextSubst(fset, src, mc.CondExpr, subs, subTexts)
		if mc.CondLazy {
			condText = "(" + condText + ")()"
		}
		caseLines = append(caseLines, fmt.Sprintf("case %s: return %s", condText, resText))
	}
	cases := joinWith(caseLines, "; ")
	if hasDefault {
		return fmt.Sprintf("(func() %s { switch %s { %s; default: return %s } }())",
			resultType, valueText, cases, defaultText)
	}
	return fmt.Sprintf("(func() %s { switch %s { %s }; var _zero %s; return _zero }())",
		resultType, valueText, cases, resultType)
}

// buildMatchIfChain handles the case where at least one arm is a
// predicate (cond is bool or func() bool) — emits an if/return
// chain instead of a switch, since Go's switch can't carry predicate
// cases.
//
// _v binding: only emitted when at least one non-predicate arm
// (value-match q.Case) exists, so it's never declared-and-unused.
// When no value-match arms exist we still evaluate <value> for side
// effects via `_ = <value>`.
func buildMatchIfChain(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string, valueText, resultType string) string {
	bindV := false
	for _, mc := range sub.MatchCases {
		if !mc.IsDefault && !mc.IsPredicate {
			bindV = true
			break
		}
	}

	var lines []string
	if bindV {
		lines = append(lines, fmt.Sprintf("_v := %s", valueText))
	} else {
		lines = append(lines, fmt.Sprintf("_ = %s", valueText))
	}

	var defaultText string
	hasDefault := false
	for _, mc := range sub.MatchCases {
		resText := exprTextSubst(fset, src, mc.ResultExpr, subs, subTexts)
		if mc.IsDefault {
			defaultText = resText
			hasDefault = true
			continue
		}
		condText := exprTextSubst(fset, src, mc.CondExpr, subs, subTexts)
		if mc.CondLazy {
			condText = "(" + condText + ")()"
		}
		if !mc.IsPredicate {
			condText = "_v == " + condText
		}
		lines = append(lines, fmt.Sprintf("if %s { return %s }", condText, resText))
	}

	if hasDefault {
		lines = append(lines, "return "+defaultText)
	} else {
		// The validateMatch pass should have rejected this combination
		// already (no q.Default with predicate arms is a build error);
		// emit a zero-value return as a defensive backstop so the
		// rewritten expression still parses while the diagnostic
		// surfaces.
		lines = append(lines, fmt.Sprintf("var _zero %s; return _zero", resultType))
	}

	return fmt.Sprintf("(func() %s { %s }())", resultType, joinWith(lines, "; "))
}

// buildFieldsReplacement emits a literal `[]string{"a", "b", "c"}`
// expression for q.Fields / q.AllFields. The names come from the
// typecheck pass's resolveReflection.
func buildFieldsReplacement(sub qSubCall) string {
	if len(sub.StructFields) == 0 {
		return "[]string(nil)"
	}
	parts := make([]string, len(sub.StructFields))
	for i, n := range sub.StructFields {
		parts[i] = strconv.Quote(n)
	}
	return "[]string{" + strings.Join(parts, ", ") + "}"
}

// enumTypeText returns the type-text the rewriter should splice into
// the generated literal / IIFE param. Falls back to "any" only when
// the typecheck pass couldn't resolve T — which already produced a
// diagnostic, so the rewriter never reaches here in a successful
// build. The fallback exists only to keep generated code parseable
// while a diagnostic flows through.
func enumTypeText(sub qSubCall) string {
	if sub.EnumTypeText != "" {
		return sub.EnumTypeText
	}
	return "any"
}
