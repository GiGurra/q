package preprocessor

// strings.go — rewriter helpers for the compile-time string-case
// family (q.Upper / q.Lower / q.Snake / q.Kebab / q.Camel / q.Pascal
// / q.Title). Each call site rewrites in-place to a Go string
// literal. The transformation is purely textual: tokenise the input
// on case-boundaries / separators, then re-emit with the requested
// shape.

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"
	"unicode"
)

// buildStringCaseReplacement extracts the unquoted string literal
// from sub.InnerExpr (validated as *ast.BasicLit/STRING by the
// scanner) and runs the family-specific transform. Returns a
// Go-quoted string literal ready to substitute at the call site.
func buildStringCaseReplacement(sub qSubCall) (string, error) {
	if sub.InnerExpr == nil {
		return "", fmt.Errorf("q.<string-case>: missing argument")
	}
	lit, ok := sub.InnerExpr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", fmt.Errorf("q.<string-case>: argument must be a Go string literal")
	}
	unquoted, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", fmt.Errorf("q.<string-case>: malformed string literal %q: %w", lit.Value, err)
	}
	transformed := applyStringCase(unquoted, sub.Family)
	return strconv.Quote(transformed), nil
}

// applyStringCase dispatches to the per-family case transform.
func applyStringCase(s string, f family) string {
	switch f {
	case familyUpper:
		return strings.ToUpper(s)
	case familyLower:
		return strings.ToLower(s)
	case familySnake:
		return joinWords(splitWords(s), "_", caseLower)
	case familyKebab:
		return joinWords(splitWords(s), "-", caseLower)
	case familyCamel:
		return joinWords(splitWords(s), "", caseCamel)
	case familyPascal:
		return joinWords(splitWords(s), "", casePascal)
	case familyTitle:
		// Title preserves space separators; only the first letter
		// of each word is upper-cased. Splits on space only,
		// leaving any other separators in place.
		return joinWords(strings.Split(s, " "), " ", caseTitle)
	}
	return s
}

// caseStyle selects how each word from splitWords is rendered.
type caseStyle int

const (
	caseLower  caseStyle = iota // every char lower
	caseUpper                   // every char upper
	caseCamel                   // first word lower, others upper-initial
	casePascal                  // every word upper-initial
	caseTitle                   // every word upper-initial (preserves rest)
)

// splitWords tokenises s into "words" by case-boundaries and
// runs of separators. Each token is one camel/pascal-style word.
//
// Rules:
//
//   - Runs of separator characters (`_`, `-`, ` `, `.`, `/`) split
//     the input.
//   - Within a run of letters, an uppercase character followed by
//     a lowercase character starts a new word ("HTTPRequest" →
//     "HTTP", "Request").
//   - A lowercase-to-uppercase boundary starts a new word
//     ("helloWorld" → "hello", "World").
//   - Digits cluster with whatever's adjacent — `v2Beta` → `v2`,
//     `Beta`.
//
// Every returned word is a contiguous run of letters and/or digits
// from the original; case is preserved here so callers can decide
// how to re-cap each word.
func splitWords(s string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = cur[:0]
		}
	}
	prev := func(i int) rune {
		if i == 0 {
			return 0
		}
		return rune(s[i-1])
	}
	runes := []rune(s)
	for i, r := range runes {
		if isSeparator(r) {
			flush()
			continue
		}
		if i > 0 && isCaseBoundary(rune(prev(i)), r, runes, i) {
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	return words
}

// isSeparator reports whether r is one of the characters that
// separates words in the source.
func isSeparator(r rune) bool {
	switch r {
	case '_', '-', ' ', '.', '/':
		return true
	}
	return false
}

// isCaseBoundary reports whether the transition from prev to curr
// (in runes, with curr at index i) starts a new word.
func isCaseBoundary(prev, curr rune, runes []rune, i int) bool {
	// Lowercase → Uppercase: helloWorld → hello, World.
	if unicode.IsLower(prev) && unicode.IsUpper(curr) {
		return true
	}
	// Uppercase → Uppercase followed by Lowercase: HTTPRequest → HTTP, Request.
	if i+1 < len(runes) && unicode.IsUpper(prev) && unicode.IsUpper(curr) && unicode.IsLower(runes[i+1]) {
		return true
	}
	// Letter → Digit or Digit → Letter: keep digit-runs with the
	// preceding letter cluster (no boundary). Adjust only on
	// transitions that look like camel-style breaks.
	return false
}

// joinWords reassembles tokens with the chosen separator and case
// style.
func joinWords(words []string, sep string, style caseStyle) string {
	if len(words) == 0 {
		return ""
	}
	parts := make([]string, len(words))
	for i, w := range words {
		switch style {
		case caseLower:
			parts[i] = strings.ToLower(w)
		case caseUpper:
			parts[i] = strings.ToUpper(w)
		case caseCamel:
			if i == 0 {
				parts[i] = strings.ToLower(w)
			} else {
				parts[i] = upperInitial(strings.ToLower(w))
			}
		case casePascal:
			parts[i] = upperInitial(strings.ToLower(w))
		case caseTitle:
			parts[i] = upperInitial(w)
		}
	}
	return strings.Join(parts, sep)
}

// upperInitial returns s with its first rune upper-cased; the rest
// of s is left untouched (so caseTitle preserves intra-word case).
func upperInitial(s string) string {
	if s == "" {
		return s
	}
	rs := []rune(s)
	rs[0] = unicode.ToUpper(rs[0])
	return string(rs)
}
