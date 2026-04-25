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
