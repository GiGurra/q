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

// renderAtErr renders a q.At chain whose terminal is .OrError or .OrE.
// The IIFE built by buildAtReplacement returns (T, error); this
// function wraps it with the standard bind-and-bubble shape so the
// error flows through to the enclosing function's error return slot.
//
// Form-aware (matches tryBindLine's branches):
//
//	formDefine:           v, _qErrN := <iife>
//	formAssign:           var _qErrN error; v, _qErrN = <iife>
//	formDiscard:          _, _qErrN := <iife>
//	formReturn / formHoist: _qTmpN, _qErrN := <iife>
//
// followed by `if _qErrN != nil { return <zeros> }`.
func renderAtErr(fset *token.FileSet, src []byte, sh callShape, sub qSubCall, counter int, subs []qSubCall, subTexts []string) (string, error) {
	results := sh.EnclosingFuncType.Results
	if results == nil || results.NumFields() == 0 {
		return "", fmt.Errorf("q.At(...).%s used in a function with no return values; the bubble has nowhere to go", atTerminalName(sub.AtTerminal))
	}
	zeros, err := zeroExprs(fset, src, results)
	if err != nil {
		return "", err
	}
	iife := buildAtReplacement(fset, src, sub, subs, subTexts)
	errVar := fmt.Sprintf("_qErr%d", counter)
	indent := indentOf(src, fset.Position(sh.Stmt.Pos()).Offset)
	bindLine := tryBindLine(fset, src, sh, errVar, iife, indent, counter)
	zeros[len(zeros)-1] = errVar
	return assembleErrBlock(bindLine, errVar, indent, zeros), nil
}

// atTerminalName returns the user-facing method name for a terminal,
// used in diagnostics so the message mentions what the user wrote.
func atTerminalName(t atTerminal) string {
	switch t {
	case atTerminalOr:
		return "Or"
	case atTerminalOrZero:
		return "OrZero"
	case atTerminalOrError:
		return "OrError"
	case atTerminalOrE:
		return "OrE"
	}
	return ""
}

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
	case "OrError":
		if len(call.Args) != 1 {
			return qSubCall{}, false, nil
		}
		terminal = atTerminalOrError
		terminalArg = call.Args[0]
	case "OrE":
		// q.At(...).OrE(<call>) where <call> returns (T, error). Go's
		// f(g()) multi-spread rule means call.Args is a single CallExpr
		// in the spread form. Two-arg form (.OrE(v, err)) is also
		// syntactically legal at the call site; we capture both.
		if len(call.Args) == 1 {
			terminalArg = call.Args[0]
		} else if len(call.Args) == 2 {
			// Wrap the two args in a synthetic ParenExpr-Tuple isn't
			// possible directly; we capture them as a CompositeExpr-like
			// hack: store the first arg here and stash the second arg in
			// AtPaths-via-side-channel. For v1, only support the spread
			// form so the common case works without surface complexity.
			return qSubCall{}, false, fmt.Errorf("q.At(...).OrE takes a single (T, error)-returning call; got two args (use q.At(...).OrE(myFetcher()) or wrap your two args in a helper)")
		} else {
			return qSubCall{}, false, nil
		}
		terminal = atTerminalOrE
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
// Non-errored shape (one path, .Or terminal, every hop nilable):
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
// Errored shape (.OrError or .OrE terminal) — the IIFE returns
// (T, error); the rewriter wraps it with a bind-and-bubble check so
// the error flows through the enclosing function's error return:
//
//	(func() (T, error) {
//	    for { ... return val, nil }
//	    return *new(T), <err>             // .OrError(err)
//	    // OR:
//	    return <fetcher-call>             // .OrE(call())
//	}())
//
// Each path lives in its own one-iteration `for { ... break ... }` so
// per-path variable declarations are scoped to the loop body — no
// `goto`-over-decl violations.
func buildAtReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string) string {
	resultText := sub.AtResultTypeText
	if resultText == "" {
		resultText = "any"
	}
	errored := isErroredAtTerminal(sub.AtTerminal)

	var b []byte
	b = append(b, "(func() "...)
	if errored {
		b = append(b, "("...)
		b = append(b, resultText...)
		b = append(b, ", error)"...)
	} else {
		b = append(b, resultText...)
	}
	b = append(b, " { "...)

	for pi, path := range sub.AtPaths {
		chain := collectAtChain(path)
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
		if errored {
			b = append(b, fmt.Sprintf("return _qAt%d_%d, nil ", pi, len(chain)-1)...)
		} else {
			b = append(b, fmt.Sprintf("return _qAt%d_%d ", pi, len(chain)-1)...)
		}
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
	case atTerminalOrError:
		errText := exprTextSubst(fset, src, sub.AtTerminalArg, subs, subTexts)
		b = append(b, "var _qAtZero "...)
		b = append(b, resultText...)
		b = append(b, "; return _qAtZero, "...)
		b = append(b, errText...)
		b = append(b, " "...)
	case atTerminalOrE:
		argText := exprTextSubst(fset, src, sub.AtTerminalArg, subs, subTexts)
		b = append(b, "return "...)
		b = append(b, argText...)
		b = append(b, " "...)
	}
	b = append(b, "}())"...)
	return string(b)
}
