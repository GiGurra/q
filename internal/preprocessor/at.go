package preprocessor

// at.go — scanner + typecheck + rewriter for the q.At family.
//
// Source shape:
//
//	q.At(<chain>).OrElse(<alt>)*.{Or(<fallback>) | OrZero()}
//
// where <chain> is any expression — a selector chain like
// `user.Profile.Theme` is the common case but a single identifier
// or a call result also works. Each .OrElse(<alt>) may carry another
// chain expression (which the rewriter walks the same way) or any
// other expression (which the rewriter binds and nil-checks once).
//
// The rewriter emits an IIFE that walks each path in source order,
// nil-guarding every nilable hop. The first path whose final hop is
// non-nil wins; if every path's final hop is nil, the terminal kicks
// in:
//
//   - .Or(fallback) returns the fallback expression.
//   - .OrZero() returns the zero value of T.
//
// Bubble terminals (.OrErr / .OrWrap / etc.) are deferred to a
// follow-up; the chain shape leaves room for them as additional
// terminal methods.

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
)

// matchAtChain inspects a CallExpr and, if it is a terminal call on a
// q.At chain (.Or(...) or .OrZero()), unwinds inward through any
// .OrElse hops to find the q.At entry and returns a populated
// qSubCall. Returns (zero, false, nil) when the call is not a q.At
// terminal. Returns an error for malformed chains (e.g., wrong arg
// counts, .OrElse outside a chain).
func matchAtChain(call *ast.CallExpr, alias string) (qSubCall, bool, error) {
	terminalSel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return qSubCall{}, false, nil
	}
	var terminal atTerminal
	var terminalArg ast.Expr
	switch terminalSel.Sel.Name {
	case "Or":
		if len(call.Args) != 1 {
			return qSubCall{}, false, nil
		}
		terminal = atTerminalOr
		terminalArg = call.Args[0]
	case "OrZero":
		if len(call.Args) != 0 {
			return qSubCall{}, false, nil
		}
		terminal = atTerminalOrZero
	default:
		return qSubCall{}, false, nil
	}

	// Walk inward: terminalSel.X is q.At(chain) directly, or a chain of
	// .OrElse(alt) calls on top of an inner q.At(chain).
	var paths []ast.Expr
	cur := terminalSel.X
	for {
		innerCall, ok := cur.(*ast.CallExpr)
		if !ok {
			return qSubCall{}, false, nil
		}
		innerSel, ok := innerCall.Fun.(*ast.SelectorExpr)
		if !ok {
			return qSubCall{}, false, nil
		}
		switch innerSel.Sel.Name {
		case "OrElse":
			if len(innerCall.Args) != 1 {
				return qSubCall{}, false, nil
			}
			paths = append([]ast.Expr{innerCall.Args[0]}, paths...)
			cur = innerSel.X
		case "At":
			if !isQRef(innerSel.X, alias) {
				return qSubCall{}, false, nil
			}
			if len(innerCall.Args) != 1 {
				return qSubCall{}, false, fmt.Errorf("q.At takes exactly one argument (the path expression); got %d", len(innerCall.Args))
			}
			paths = append([]ast.Expr{innerCall.Args[0]}, paths...)
			return qSubCall{
				Family:        familyAt,
				AtPaths:       paths,
				AtTerminal:    terminal,
				AtTerminalArg: terminalArg,
			}, true, nil
		default:
			return qSubCall{}, false, nil
		}
	}
}

// isQRef reports whether expr is a bare identifier referring to the
// pkg/q import under the local alias.
func isQRef(expr ast.Expr, alias string) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == alias
}

// resolveAt populates AtHopNilable + AtResultTypeText from go/types
// info. For each path, it walks the selector chain (or treats the
// expression as a single hop when it isn't a SelectorExpr), recording
// per-hop nilability so the rewriter can decide which hops need a
// guard.
//
// The result type T is read from the entry path's leaf.
func resolveAt(fset *token.FileSet, sc *qSubCall, info *types.Info, pkgPath string) (Diagnostic, bool) {
	if len(sc.AtPaths) == 0 {
		pos := fset.Position(sc.OuterCall.Pos())
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  "q.At chain has no path; expected q.At(<expr>).{Or|OrZero|...}",
		}, true
	}

	hopsPerPath := make([][]bool, len(sc.AtPaths))
	for pi, path := range sc.AtPaths {
		chain := collectAtChain(path)
		hops := make([]bool, len(chain))
		for hi, hop := range chain {
			tv, ok := info.Types[hop]
			if !ok || tv.Type == nil {
				pos := fset.Position(hop.Pos())
				return Diagnostic{
					File: pos.Filename,
					Line: pos.Line,
					Col:  pos.Column,
					Msg:  fmt.Sprintf("q.At: cannot resolve type of path %d hop %d", pi, hi),
				}, true
			}
			hops[hi] = isNilableType(tv.Type)
		}
		hopsPerPath[pi] = hops
	}
	sc.AtHopNilable = hopsPerPath

	qualifier := func(p *types.Package) string {
		if p == nil || p.Path() == pkgPath {
			return ""
		}
		return p.Name()
	}

	entry := sc.AtPaths[0]
	if tv, ok := info.Types[entry]; ok && tv.Type != nil {
		sc.AtResultTypeText = types.TypeString(tv.Type, qualifier)
	}
	return Diagnostic{}, false
}

// collectAtChain returns the sequence of subexpressions from the root
// of a selector chain to the leaf:
//
//	a.b.c.d  ->  [a, a.b, a.b.c, a.b.c.d]
//
// For non-selector input, the slice has a single element (the input
// itself). The leaf is always the last element.
func collectAtChain(e ast.Expr) []ast.Expr {
	var chain []ast.Expr
	cur := e
	for {
		chain = append([]ast.Expr{cur}, chain...)
		sel, ok := cur.(*ast.SelectorExpr)
		if !ok {
			break
		}
		cur = sel.X
	}
	return chain
}

// buildAtReplacement emits the IIFE for a q.At chain.
//
// Output shape (one path, .Or terminal, every hop nilable):
//
//	(func() T {
//	    for {
//	        _qAt0_0 := <root>
//	        if _qAt0_0 == nil { break }
//	        _qAt0_1 := _qAt0_0.<sel>
//	        if _qAt0_1 == nil { break }
//	        return _qAt0_1
//	    }
//	    return <fallback>
//	}())
//
// Each path lives in its own one-iteration `for { ... break ... }` so
// per-path variable declarations are scoped to the loop body — no
// `goto`-over-decl violations, and the outer IIFE returns from inside
// the loop on success or falls through to the next path's loop / the
// terminal on break.
func buildAtReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string) string {
	resultText := sub.AtResultTypeText
	if resultText == "" {
		resultText = "any"
	}

	var b []byte
	b = append(b, "(func() "...)
	b = append(b, resultText...)
	b = append(b, " { "...)

	for pi, path := range sub.AtPaths {
		chain := collectAtChain(path)
		// hops may be absent in tests that bypass typecheck — fall back
		// to no guards so the output still parses.
		var hops []bool
		if pi < len(sub.AtHopNilable) {
			hops = sub.AtHopNilable[pi]
		}
		b = append(b, "for { "...)
		for hi := range chain {
			varName := fmt.Sprintf("_qAt%d_%d", pi, hi)
			if hi == 0 {
				rootText := exprTextSubst(fset, src, chain[0], subs, subTexts)
				b = append(b, varName...)
				b = append(b, " := "...)
				b = append(b, rootText...)
				b = append(b, "; "...)
			} else {
				selName := chain[hi].(*ast.SelectorExpr).Sel.Name
				b = append(b, fmt.Sprintf("%s := _qAt%d_%d.%s; ", varName, pi, hi-1, selName)...)
			}
			if hi < len(hops) && hops[hi] {
				b = append(b, fmt.Sprintf("if %s == nil { break }; ", varName)...)
			}
		}
		b = append(b, fmt.Sprintf("return _qAt%d_%d ", pi, len(chain)-1)...)
		b = append(b, "}; "...)
	}

	switch sub.AtTerminal {
	case atTerminalOr:
		fallbackText := exprTextSubst(fset, src, sub.AtTerminalArg, subs, subTexts)
		b = append(b, "return "...)
		b = append(b, fallbackText...)
		b = append(b, " "...)
	case atTerminalOrZero:
		b = append(b, "var _qAtZero "...)
		b = append(b, resultText...)
		b = append(b, "; return _qAtZero "...)
	}
	b = append(b, "}())"...)
	return string(b)
}
