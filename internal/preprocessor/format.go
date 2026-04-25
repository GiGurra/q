package preprocessor

// format.go — q.F / q.Ferr / q.Fln rewriter.
//
// Each q.F("hi {name}, you are {age+1}") call site rewrites to a
// fmt.Sprintf with the {expr} segments lifted out as positional %v
// arguments. Brace-escape `{{` for literal `{` and `}}` for literal
// `}`. The format string MUST be a Go string literal — dynamic
// formats surface a diagnostic.
//
// Inside an {expr} segment, Go string literals (`"..."`, `'...'`,
// `` `...` ``) are honoured: braces and quotes inside them don't
// terminate the placeholder. So `q.F("got {f(\"}\")}")` extracts
// `f("}")` as the expression.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// fParseResult holds the outcome of parsing a q.F format literal.
type fParseResult struct {
	// FormatLiteral is the rewritten format string with each
	// `{expr}` replaced by `%v` and `{{`/`}}` collapsed to single
	// `{`/`}`. Already %-escaped — any literal `%` in the source
	// becomes `%%` so fmt.Sprintf reproduces it verbatim.
	FormatLiteral string

	// ExprTexts are the extracted Go-expression strings, one per
	// `%v` in FormatLiteral, in left-to-right order. Each has been
	// validated as a parseable Go expression.
	ExprTexts []string
}

// parseFFormat walks the unquoted contents of the format string
// literal and produces the rewritten format + extracted expression
// list. Returns an error with the user-facing diagnostic message
// when the format is malformed (unbalanced braces, non-Go expression
// text, …).
func parseFFormat(unquoted string) (fParseResult, error) {
	var out strings.Builder
	var exprs []string
	i := 0
	for i < len(unquoted) {
		c := unquoted[i]
		switch c {
		case '{':
			if i+1 < len(unquoted) && unquoted[i+1] == '{' {
				out.WriteByte('{')
				i += 2
				continue
			}
			end, err := findFExprClose(unquoted, i)
			if err != nil {
				return fParseResult{}, err
			}
			exprText := unquoted[i+1 : end]
			if strings.TrimSpace(exprText) == "" {
				return fParseResult{}, fmt.Errorf("q.F format has empty placeholder `{}` (use `{{` for a literal `{`)")
			}
			if _, perr := parser.ParseExpr(exprText); perr != nil {
				return fParseResult{}, fmt.Errorf("q.F format placeholder %q is not a valid Go expression: %v", exprText, perr)
			}
			exprs = append(exprs, exprText)
			out.WriteString("%v")
			i = end + 1
		case '}':
			if i+1 < len(unquoted) && unquoted[i+1] == '}' {
				out.WriteByte('}')
				i += 2
				continue
			}
			return fParseResult{}, fmt.Errorf("q.F format has an unmatched `}` at offset %d (use `}}` for a literal `}`)", i)
		case '%':
			out.WriteString("%%")
			i++
		default:
			out.WriteByte(c)
			i++
		}
	}
	return fParseResult{FormatLiteral: out.String(), ExprTexts: exprs}, nil
}

// findFExprClose returns the index of the closing `}` matching the
// `{` at openIdx. Tracks brace depth and skips over Go string and
// rune literals so braces inside them don't terminate the
// placeholder. Returns an error when no matching `}` is found.
func findFExprClose(s string, openIdx int) (int, error) {
	depth := 0
	i := openIdx
	for i < len(s) {
		c := s[i]
		switch c {
		case '{':
			depth++
			i++
			continue
		case '}':
			depth--
			if depth == 0 {
				return i, nil
			}
			i++
			continue
		case '"':
			i = skipQuotedLiteral(s, i, '"')
			continue
		case '\'':
			i = skipQuotedLiteral(s, i, '\'')
			continue
		case '`':
			i++
			for i < len(s) && s[i] != '`' {
				i++
			}
			if i < len(s) {
				i++
			}
			continue
		}
		i++
	}
	return -1, fmt.Errorf("q.F format has an unclosed `{` at offset %d", openIdx)
}

// skipQuotedLiteral returns the index just past the closing quote
// (matching `quote`) for a Go string or rune literal beginning at
// startIdx (the position of the opening quote). Backslash-escaped
// characters inside the literal are honoured — `\"` doesn't
// terminate a `"`-quoted string.
func skipQuotedLiteral(s string, startIdx int, quote byte) int {
	i := startIdx + 1
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			i += 2
			continue
		}
		if s[i] == quote {
			return i + 1
		}
		i++
	}
	return i
}

// fLiteralOrError extracts the unquoted format text from the q.F
// call's InnerExpr, which the scanner validated as a *ast.BasicLit
// with Kind=STRING. Errors surface as build-aborting messages.
func fLiteralOrError(sub qSubCall) (string, error) {
	if sub.InnerExpr == nil {
		return "", fmt.Errorf("q.F internal: missing format argument")
	}
	lit, ok := sub.InnerExpr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", fmt.Errorf("q.F's argument must be a Go string literal")
	}
	unquoted, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", fmt.Errorf("q.F: malformed string literal %q: %w", lit.Value, err)
	}
	return unquoted, nil
}

// buildFReplacement is the per-sub replacement for q.F: a
// fmt.Sprintf call with the rewritten format literal and extracted
// expressions. The extracted expressions are the literal source
// text from the format string — they don't need q.* substitution
// because q.* calls inside a string literal are just opaque text.
func buildFReplacement(sub qSubCall) (string, error) {
	unquoted, err := fLiteralOrError(sub)
	if err != nil {
		return "", err
	}
	res, err := parseFFormat(unquoted)
	if err != nil {
		return "", err
	}
	if len(res.ExprTexts) == 0 {
		return strconv.Quote(res.FormatLiteral), nil
	}
	parts := []string{strconv.Quote(res.FormatLiteral)}
	parts = append(parts, res.ExprTexts...)
	return fmt.Sprintf("fmt.Sprintf(%s)", strings.Join(parts, ", ")), nil
}

// buildFerrReplacement returns errors.New(<format>) when no exprs,
// fmt.Errorf(<format>, <exprs>...) otherwise. q.Ferr produces a
// fresh error rather than wrapping one, so no `%w` is added.
func buildFerrReplacement(sub qSubCall) (string, bool, bool, error) {
	unquoted, err := fLiteralOrError(sub)
	if err != nil {
		return "", false, false, err
	}
	res, err := parseFFormat(unquoted)
	if err != nil {
		return "", false, false, err
	}
	if len(res.ExprTexts) == 0 {
		return fmt.Sprintf("errors.New(%s)", strconv.Quote(res.FormatLiteral)), false, true, nil
	}
	parts := []string{strconv.Quote(res.FormatLiteral)}
	parts = append(parts, res.ExprTexts...)
	return fmt.Sprintf("fmt.Errorf(%s)", strings.Join(parts, ", ")), true, false, nil
}

// buildFlnReplacement is fmt.Fprintln(q.DebugWriter, <message>) so
// fixtures can capture output via the same DebugWriter pkg/q exposes
// for q.DebugPrintln.
func buildFlnReplacement(sub qSubCall, alias string) (string, error) {
	unquoted, err := fLiteralOrError(sub)
	if err != nil {
		return "", err
	}
	res, err := parseFFormat(unquoted)
	if err != nil {
		return "", err
	}
	if len(res.ExprTexts) == 0 {
		return fmt.Sprintf("fmt.Fprintln(%s.DebugWriter, %s)", alias, strconv.Quote(res.FormatLiteral)), nil
	}
	parts := []string{strconv.Quote(res.FormatLiteral)}
	parts = append(parts, res.ExprTexts...)
	return fmt.Sprintf("fmt.Fprintln(%s.DebugWriter, fmt.Sprintf(%s))", alias, strings.Join(parts, ", ")), nil
}

// sqlPlaceholder generates the placeholder text for the i-th
// (0-indexed) extracted expression for a given SQL family. Drivers
// differ on placeholder syntax — same parsing, different output.
func sqlPlaceholder(f family, i int) string {
	switch f {
	case familyPgSQL:
		return fmt.Sprintf("$%d", i+1)
	case familyNamedSQL:
		return fmt.Sprintf(":name%d", i+1)
	}
	return "?"
}

// parseSQLFormat is parseFFormat's twin: same brace-tracking and
// expression-validation, different placeholder generation per
// family. Returns the rewritten SQL with placeholders + the
// extracted expressions in left-to-right order. `%` characters in
// the format are NOT escaped (unlike q.F) — fmt.Sprintf is not
// involved on the output side, so `%` is just a literal character
// in SQL.
func parseSQLFormat(unquoted string, f family) (queryText string, exprTexts []string, err error) {
	var out strings.Builder
	var exprs []string
	i := 0
	for i < len(unquoted) {
		c := unquoted[i]
		switch c {
		case '{':
			if i+1 < len(unquoted) && unquoted[i+1] == '{' {
				out.WriteByte('{')
				i += 2
				continue
			}
			end, ferr := findFExprClose(unquoted, i)
			if ferr != nil {
				return "", nil, ferr
			}
			exprText := unquoted[i+1 : end]
			if strings.TrimSpace(exprText) == "" {
				return "", nil, fmt.Errorf("q.SQL format has empty placeholder `{}` (use `{{` for a literal `{`)")
			}
			if _, perr := parser.ParseExpr(exprText); perr != nil {
				return "", nil, fmt.Errorf("q.SQL placeholder %q is not a valid Go expression: %v", exprText, perr)
			}
			out.WriteString(sqlPlaceholder(f, len(exprs)))
			exprs = append(exprs, exprText)
			i = end + 1
		case '}':
			if i+1 < len(unquoted) && unquoted[i+1] == '}' {
				out.WriteByte('}')
				i += 2
				continue
			}
			return "", nil, fmt.Errorf("q.SQL format has an unmatched `}` at offset %d (use `}}` for a literal `}`)", i)
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String(), exprs, nil
}

// buildSQLReplacement emits a SQLQuery composite literal. The alias
// is the user file's import name for pkg/q; when no expressions are
// extracted, Args is nil to keep the literal valid without
// allocating.
func buildSQLReplacement(sub qSubCall, alias string) (string, error) {
	unquoted, err := fLiteralOrError(sub)
	if err != nil {
		return "", err
	}
	queryText, exprTexts, err := parseSQLFormat(unquoted, sub.Family)
	if err != nil {
		return "", err
	}
	if len(exprTexts) == 0 {
		return fmt.Sprintf("%s.SQLQuery{Query: %s, Args: nil}", alias, strconv.Quote(queryText)), nil
	}
	return fmt.Sprintf("%s.SQLQuery{Query: %s, Args: []any{%s}}",
		alias, strconv.Quote(queryText), strings.Join(exprTexts, ", ")), nil
}
