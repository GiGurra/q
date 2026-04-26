package preprocessor

// from.go — typecheck + rewriter for q.Convert[Target](src, opts...).
//
// Detection lives in scanner.go (familyConvert branch). This file
// owns:
//
//   - convertField — per-target-field record. Carries enough info for
//     buildConvertReplacement to render the field's value text given a
//     source-variable name. Three shapes:
//
//       kindDirect:   srcVar.<accessor>
//       kindOverride: <override expression rendered from raw src bytes>
//       kindNested:   <nested struct literal built from child fields>
//
//   - resolveConvert — validates source/target shapes, parses
//     overrides from variadic option calls (only the AST nodes are
//     stored — text rendering happens at rewrite time when src
//     bytes are in scope), then walks Target's exported fields and
//     resolves each via:
//
//       1. Override (q.Set / q.SetFn).
//       2. Direct copy (same-named source field, AssignableTo).
//       3. Nested derivation (same-named source field, both struct).
//       4. Diagnostic.
//
//   - buildConvertReplacement — emits the struct-literal expression
//     (or an IIFE binding the source to a temp first when src is a
//     non-trivial expression).

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"
)

type convertFieldKind int

const (
	convertKindDirect convertFieldKind = iota
	convertKindOverride
	convertKindNested
)

// convertOverrideKind discriminates Set (verbatim value) vs SetFn
// (function-of-source). Renderer wraps SetFn as `(<fn>)(<srcVar>)`.
type convertOverrideKind int

const (
	convertOverrideSet convertOverrideKind = iota
	convertOverrideSetFn
)

// convertField is one mapping between a target field and its
// computed value.
type convertField struct {
	Name string
	Kind convertFieldKind

	// kindDirect/kindNested: name of the same-named source field to
	// dive into.
	Accessor string

	// kindOverride: the AST expression supplied by q.Set / q.SetFn,
	// plus the kind so the renderer knows whether to wrap it in
	// `(...)(srcVar)`.
	OverrideKind convertOverrideKind
	OverrideExpr ast.Expr

	// kindNested: target sub-struct type spelling + child mappings.
	NestedTy string
	Nested   []convertField
}

// resolveConvert is the entry point invoked by the typecheck pass.
func resolveConvert(fset *token.FileSet, sc *qSubCall, info *types.Info, pkgPath string) (Diagnostic, bool) {
	pos := fset.Position(sc.OuterCall.Pos())
	mkErr := func(msg string) (Diagnostic, bool) {
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  msg,
		}, true
	}

	if sc.AsType == nil || sc.ConvertSrc == nil {
		return mkErr("q.Convert: missing Target type-arg or src expression")
	}

	targetTV, ok := info.Types[sc.AsType]
	if !ok || targetTV.Type == nil {
		return mkErr("q.Convert: cannot resolve Target type")
	}
	srcTV, ok := info.Types[sc.ConvertSrc]
	if !ok || srcTV.Type == nil {
		return mkErr("q.Convert: cannot resolve source expression type")
	}

	qualifier := func(p *types.Package) string {
		if p == nil || p.Path() == pkgPath {
			return ""
		}
		return p.Name()
	}

	tree, d, fail := parseConvertOverrides(fset, sc.ConvertOptArgs, info, targetTV.Type, srcTV.Type, qualifier)
	if fail {
		return d, true
	}

	fields, d, fail := resolveStructConversion(srcTV.Type, targetTV.Type, tree, qualifier, pos, map[string]bool{})
	if fail {
		return d, true
	}

	sc.ConvertTargetTypeText = types.TypeString(targetTV.Type, qualifier)
	sc.ConvertFields = fields
	return Diagnostic{}, false
}

// convertOverride captures one parsed q.Set / q.SetFn entry.
type convertOverride struct {
	kind convertOverrideKind
	expr ast.Expr // value expr for Set; fn expr for SetFn
}

// overrideNode is one node in the recursive override tree. A leaf
// node (`leaf != nil`) means this exact path is overridden; an
// interior node (`children != nil` and `leaf == nil`) means at least
// one descendant is overridden but this path is not. A path can't be
// both — `q.Set(T{}.A, ...)` and `q.Set(T{}.A.B, ...)` together fail
// validation because the first override owns the whole subtree.
type overrideNode struct {
	leaf     *convertOverride
	children map[string]*overrideNode
}

// parseConvertOverrides walks the variadic q.Set / q.SetFn calls,
// validates each, and builds the override tree. Multi-hop paths
// (q.Set(Target{}.A.B.C, v)) become nested children. Conflicts (a
// path that's both an override and an ancestor of another override)
// fail validation.
func parseConvertOverrides(fset *token.FileSet, opts []ast.Expr, info *types.Info, targetType, srcType types.Type, qualifier types.Qualifier) (*overrideNode, Diagnostic, bool) {
	mkErr := func(p token.Position, msg string) (*overrideNode, Diagnostic, bool) {
		return nil, Diagnostic{
			File: p.Filename,
			Line: p.Line,
			Col:  p.Column,
			Msg:  msg,
		}, true
	}

	root := &overrideNode{children: map[string]*overrideNode{}}
	if _, ok := targetType.Underlying().(*types.Struct); !ok {
		return root, Diagnostic{}, false
	}

	for _, opt := range opts {
		callPos := fset.Position(opt.Pos())
		call, ok := opt.(*ast.CallExpr)
		if !ok {
			return mkErr(callPos, "q.Convert: option must be a q.Set / q.SetFn call expression")
		}
		method := convertOptionMethod(call.Fun)
		if method == "" {
			return mkErr(callPos, "q.Convert: option must be a q.Set or q.SetFn call")
		}
		if len(call.Args) != 2 {
			return mkErr(callPos, fmt.Sprintf("q.%s takes exactly 2 arguments (targetField, value/fn); got %d", method, len(call.Args)))
		}
		path, d2, fail := extractTargetFieldPath(call.Args[0], targetType, fset, qualifier, method)
		if fail {
			return nil, d2, true
		}

		// Resolve the path through Target's struct chain to get the
		// leaf field's expected type. Each hop must be an exported
		// struct field.
		leafField, d2, fail := walkTargetPath(targetType, path, qualifier, callPos, method)
		if fail {
			return nil, d2, true
		}

		valueExpr := call.Args[1]
		valueTV, ok := info.Types[valueExpr]
		if !ok || valueTV.Type == nil {
			return mkErr(callPos, fmt.Sprintf("q.%s: cannot resolve type of override value", method))
		}

		var ovKind convertOverrideKind
		switch method {
		case "Set":
			ovKind = convertOverrideSet
			if !types.AssignableTo(valueTV.Type, leafField.Type()) {
				return mkErr(callPos, fmt.Sprintf("q.Set: target field %s (%s) is not assignable from override value (%s)",
					formatPath(targetType, path, qualifier),
					types.TypeString(leafField.Type(), qualifier),
					types.TypeString(valueTV.Type, qualifier)))
			}
		case "SetFn":
			ovKind = convertOverrideSetFn
			sig, ok := valueTV.Type.Underlying().(*types.Signature)
			if !ok || sig.Params().Len() != 1 || sig.Results().Len() != 1 {
				return mkErr(callPos, fmt.Sprintf("q.SetFn: fn must be func(Source) V; got %s",
					types.TypeString(valueTV.Type, qualifier)))
			}
			if !types.AssignableTo(srcType, sig.Params().At(0).Type()) {
				return mkErr(callPos, fmt.Sprintf("q.SetFn: fn parameter %s does not accept source type %s",
					types.TypeString(sig.Params().At(0).Type(), qualifier),
					types.TypeString(srcType, qualifier)))
			}
			ret := sig.Results().At(0).Type()
			if !types.AssignableTo(ret, leafField.Type()) {
				return mkErr(callPos, fmt.Sprintf("q.SetFn: target field %s (%s) is not assignable from fn return type (%s)",
					formatPath(targetType, path, qualifier),
					types.TypeString(leafField.Type(), qualifier),
					types.TypeString(ret, qualifier)))
			}
		}

		// Walk the tree, creating nodes as needed; install the leaf at
		// the path's tip. Conflicts fire if either an ancestor leaf
		// already exists or a descendant has a leaf.
		if d2, fail := installOverride(root, path, &convertOverride{kind: ovKind, expr: valueExpr}, callPos, formatPath(targetType, path, qualifier)); fail {
			return nil, d2, true
		}
	}
	return root, Diagnostic{}, false
}

// walkTargetPath traverses a multi-hop path through Target's struct
// chain and returns the leaf field. Each hop must be exported and
// (except for the last) of struct kind.
func walkTargetPath(targetType types.Type, path []string, qualifier types.Qualifier, pos token.Position, method string) (*types.Var, Diagnostic, bool) {
	mkErr := func(msg string) (*types.Var, Diagnostic, bool) {
		return nil, Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  msg,
		}, true
	}
	cur := targetType
	for i, name := range path {
		st, ok := cur.Underlying().(*types.Struct)
		if !ok {
			return mkErr(fmt.Sprintf("q.%s: cannot descend into %s.%s — %s is not a struct",
				method, formatPath(targetType, path[:i], qualifier), name,
				types.TypeString(cur, qualifier)))
		}
		var f *types.Var
		for j := 0; j < st.NumFields(); j++ {
			cand := st.Field(j)
			if cand.Exported() && cand.Name() == name {
				f = cand
				break
			}
		}
		if f == nil {
			return mkErr(fmt.Sprintf("q.%s: target path %s — field %q not found on %s",
				method, formatPath(targetType, path, qualifier), name,
				types.TypeString(cur, qualifier)))
		}
		if i == len(path)-1 {
			return f, Diagnostic{}, false
		}
		cur = f.Type()
	}
	return mkErr(fmt.Sprintf("q.%s: empty target path", method))
}

// formatPath renders the full target path as `Target.A.B.C` for
// diagnostics.
func formatPath(targetType types.Type, path []string, qualifier types.Qualifier) string {
	out := types.TypeString(targetType, qualifier)
	for _, p := range path {
		out += "." + p
	}
	return out
}

// installOverride attaches an override leaf at the given path in the
// tree, creating intermediate nodes as needed. Fails if an ancestor
// already has a leaf (the ancestor override would shadow this one) or
// if the path is itself an ancestor of an existing override.
func installOverride(root *overrideNode, path []string, ov *convertOverride, pos token.Position, pathLabel string) (Diagnostic, bool) {
	mkErr := func(msg string) (Diagnostic, bool) {
		return Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  msg,
		}, true
	}
	cur := root
	for i, name := range path {
		if cur.leaf != nil {
			return mkErr(fmt.Sprintf("q.Convert: override for %s conflicts with an earlier ancestor override", pathLabel))
		}
		if cur.children == nil {
			cur.children = map[string]*overrideNode{}
		}
		next, exists := cur.children[name]
		if !exists {
			next = &overrideNode{}
			cur.children[name] = next
		}
		if i == len(path)-1 {
			if next.leaf != nil {
				return mkErr(fmt.Sprintf("q.Convert: duplicate override for %s", pathLabel))
			}
			if len(next.children) > 0 {
				return mkErr(fmt.Sprintf("q.Convert: override for %s conflicts with deeper overrides under the same path", pathLabel))
			}
			next.leaf = ov
			return Diagnostic{}, false
		}
		cur = next
	}
	return mkErr("q.Convert: empty override path")
}

// extractTargetFieldPath enforces and extracts the field path from
// the override's first argument. Accepted shapes:
//
//	UserDTO{}.FieldName             — single-hop (the common case)
//	UserDTO{}.Inner.Field           — multi-hop, drills into a nested
//	                                  struct field directly
//	(UserDTO{}).Field                — parenthesised, also fine
//
// Anything else (a runtime selector on an actual variable, a call
// expression, a string literal) fails with a diagnostic that names
// the expected shape.
func extractTargetFieldPath(arg ast.Expr, targetType types.Type, fset *token.FileSet, qualifier types.Qualifier, method string) ([]string, Diagnostic, bool) {
	pos := fset.Position(arg.Pos())
	mkErr := func(msg string) ([]string, Diagnostic, bool) {
		return nil, Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  msg,
		}, true
	}
	expected := fmt.Sprintf("expected %s{}.<FieldName>[.<NestedField>...]", types.TypeString(targetType, qualifier))

	// Strip outer parentheses.
	for {
		paren, ok := arg.(*ast.ParenExpr)
		if !ok {
			break
		}
		arg = paren.X
	}

	// Walk the SelectorExpr chain inward, collecting names. The
	// innermost X must be a CompositeLit (T{} witness).
	var names []string
	cur := arg
	for {
		// Strip parens at each level.
		for {
			paren, ok := cur.(*ast.ParenExpr)
			if !ok {
				break
			}
			cur = paren.X
		}
		sel, ok := cur.(*ast.SelectorExpr)
		if !ok {
			break
		}
		names = append([]string{sel.Sel.Name}, names...)
		cur = sel.X
	}
	if len(names) == 0 {
		return mkErr(fmt.Sprintf("q.%s: targetField must be a struct-literal-selector expression; %s", method, expected))
	}
	if _, ok := cur.(*ast.CompositeLit); !ok {
		return mkErr(fmt.Sprintf("q.%s: targetField must be of the form %s — got %s", method, expected, exprShape(cur)))
	}
	return names, Diagnostic{}, false
}

// exprShape returns a short human label describing the shape of expr,
// used in diagnostics so users see what they wrote vs. the expected
// composite-literal form.
func exprShape(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return fmt.Sprintf("identifier %q", e.Name)
	case *ast.SelectorExpr:
		return "a selector chain (e.g. someVar.Field) — use Target{}.Field instead"
	case *ast.CallExpr:
		return "a call expression"
	case *ast.BasicLit:
		return fmt.Sprintf("a literal of kind %s", e.Kind)
	}
	return fmt.Sprintf("%T", expr)
}

// convertOptionMethod returns "Set" or "SetFn" if fn is a q.Set / q.SetFn
// selector (with or without an explicit type argument), otherwise "".
func convertOptionMethod(fn ast.Expr) string {
	if sel, ok := fn.(*ast.SelectorExpr); ok {
		switch sel.Sel.Name {
		case "Set", "SetFn":
			return sel.Sel.Name
		}
	}
	switch x := fn.(type) {
	case *ast.IndexExpr:
		if sel, ok := x.X.(*ast.SelectorExpr); ok {
			switch sel.Sel.Name {
			case "Set", "SetFn":
				return sel.Sel.Name
			}
		}
	case *ast.IndexListExpr:
		if sel, ok := x.X.(*ast.SelectorExpr); ok {
			switch sel.Sel.Name {
			case "Set", "SetFn":
				return sel.Sel.Name
			}
		}
	}
	return ""
}

// resolveStructConversion is the recursive workhorse: given source
// and target struct types plus the override tree at the current
// depth, build the per-field list. Recurses into nested structs;
// per-target-field overrides at the current depth win, and child
// override sub-trees ride along into the recursive call so deep
// overrides reach their target.
func resolveStructConversion(srcType, targetType types.Type, tree *overrideNode, qualifier types.Qualifier, pos token.Position, seenPair map[string]bool) ([]convertField, Diagnostic, bool) {
	mkErr := func(msg string) ([]convertField, Diagnostic, bool) {
		return nil, Diagnostic{
			File: pos.Filename,
			Line: pos.Line,
			Col:  pos.Column,
			Msg:  msg,
		}, true
	}

	pairKey := types.TypeString(srcType, qualifier) + " => " + types.TypeString(targetType, qualifier)
	if seenPair[pairKey] {
		return mkErr(fmt.Sprintf("q.Convert: recursive nested derivation cycle on %s", pairKey))
	}
	seenPair = withSeen(seenPair, pairKey)

	targetStruct, ok := targetType.Underlying().(*types.Struct)
	if !ok {
		return mkErr(fmt.Sprintf("q.Convert: Target must be a struct type; got %s", types.TypeString(targetType, qualifier)))
	}
	srcStruct, ok := srcType.Underlying().(*types.Struct)
	if !ok {
		return mkErr(fmt.Sprintf("q.Convert: source must be a struct type; got %s", types.TypeString(srcType, qualifier)))
	}

	srcFields := map[string]*types.Var{}
	for i := 0; i < srcStruct.NumFields(); i++ {
		f := srcStruct.Field(i)
		if f.Exported() {
			srcFields[f.Name()] = f
		}
	}

	var fields []convertField
	for i := 0; i < targetStruct.NumFields(); i++ {
		tf := targetStruct.Field(i)
		if !tf.Exported() {
			continue
		}

		var subtree *overrideNode
		if tree != nil {
			subtree = tree.children[tf.Name()]
		}

		// 1. Direct override at this exact path?
		if subtree != nil && subtree.leaf != nil {
			fields = append(fields, convertField{
				Name:         tf.Name(),
				Kind:         convertKindOverride,
				OverrideKind: subtree.leaf.kind,
				OverrideExpr: subtree.leaf.expr,
			})
			continue
		}

		sf, hasSF := srcFields[tf.Name()]

		// 2. Nested overrides at deeper paths? Recurse with the
		//    subtree so the descendant overrides reach their target.
		//    Both source and target sides must be structs; if the
		//    source side isn't (or doesn't exist), the override path
		//    is broken — diagnose.
		if subtree != nil && subtree.children != nil && len(subtree.children) > 0 {
			if !hasSF {
				return mkErr(fmt.Sprintf("q.Convert: nested override targets %s.%s but source has no counterpart",
					types.TypeString(targetType, qualifier), tf.Name()))
			}
			_, sfIsStruct := sf.Type().Underlying().(*types.Struct)
			_, tfIsStruct := tf.Type().Underlying().(*types.Struct)
			if !sfIsStruct || !tfIsStruct {
				return mkErr(fmt.Sprintf("q.Convert: nested override targets %s.%s, but %s is not a struct",
					types.TypeString(targetType, qualifier), tf.Name(),
					types.TypeString(tf.Type(), qualifier)))
			}
			children, d, fail := resolveStructConversion(sf.Type(), tf.Type(), subtree, qualifier, pos, seenPair)
			if fail {
				return nil, d, true
			}
			fields = append(fields, convertField{
				Name:     tf.Name(),
				Kind:     convertKindNested,
				Accessor: sf.Name(),
				NestedTy: types.TypeString(tf.Type(), qualifier),
				Nested:   children,
			})
			continue
		}

		// 3. Direct copy?
		if hasSF && types.AssignableTo(sf.Type(), tf.Type()) {
			fields = append(fields, convertField{
				Name:     tf.Name(),
				Kind:     convertKindDirect,
				Accessor: sf.Name(),
			})
			continue
		}

		// 4. Auto-derived nested struct?
		if hasSF {
			_, sfIsStruct := sf.Type().Underlying().(*types.Struct)
			_, tfIsStruct := tf.Type().Underlying().(*types.Struct)
			if sfIsStruct && tfIsStruct {
				children, d, fail := resolveStructConversion(sf.Type(), tf.Type(), nil, qualifier, pos, seenPair)
				if fail {
					return nil, d, true
				}
				fields = append(fields, convertField{
					Name:     tf.Name(),
					Kind:     convertKindNested,
					Accessor: sf.Name(),
					NestedTy: types.TypeString(tf.Type(), qualifier),
					Nested:   children,
				})
				continue
			}
		}

		// 5. Diagnostic.
		if !hasSF {
			return mkErr(fmt.Sprintf("q.Convert: target field %s.%s has no counterpart on source %s",
				types.TypeString(targetType, qualifier), tf.Name(),
				types.TypeString(srcType, qualifier)))
		}
		return mkErr(fmt.Sprintf("q.Convert: target field %s.%s (%s) is not assignable from source field %s.%s (%s)",
			types.TypeString(targetType, qualifier), tf.Name(),
			types.TypeString(tf.Type(), qualifier),
			types.TypeString(srcType, qualifier), sf.Name(),
			types.TypeString(sf.Type(), qualifier)))
	}
	return fields, Diagnostic{}, false
}

func withSeen(seen map[string]bool, key string) map[string]bool {
	out := make(map[string]bool, len(seen)+1)
	for k, v := range seen {
		out[k] = v
	}
	out[key] = true
	return out
}

// buildConvertReplacement emits the struct literal that replaces the
// q.Convert call.
//
// Bare-identifier source — splice directly:
//
//	q.Convert[Target](user)  →  Target{F: user.F, ...}
//
// Non-trivial source (call, selector chain, etc.) — bind to a temp
// inside an IIFE so the source expression evaluates exactly once:
//
//	q.Convert[Target](getUser())  →
//	    func() Target { _qSrcN := getUser(); return Target{F: _qSrcN.F, ...} }()
//
// The renderer threads two source-variable names through the
// recursion: `accessVar` walks deeper as the tree descends (so direct
// copies at depth read from `srcVar.A.B`), while `topSrcVar` stays
// rooted at the original source so SetFn invocations always receive
// the top-level Source value the user declared in the call.
func buildConvertReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string, counter int) string {
	target := sub.ConvertTargetTypeText
	if target == "" {
		target = "any"
	}
	srcText := exprTextSubst(fset, src, sub.ConvertSrc, subs, subTexts)

	if _, ok := sub.ConvertSrc.(*ast.Ident); ok {
		return renderConvertLiteral(fset, src, target, srcText, srcText, sub.ConvertFields)
	}
	srcVar := fmt.Sprintf("_qSrc%d", counter)
	literal := renderConvertLiteral(fset, src, target, srcVar, srcVar, sub.ConvertFields)
	return fmt.Sprintf("(func() %s { %s := %s; return %s }())", target, srcVar, srcText, literal)
}

func renderConvertLiteral(fset *token.FileSet, src []byte, target, accessVar, topSrcVar string, fields []convertField) string {
	if len(fields) == 0 {
		return target + "{}"
	}
	var b strings.Builder
	b.WriteString(target)
	b.WriteString("{")
	for i, f := range fields {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(f.Name)
		b.WriteString(": ")
		b.WriteString(renderConvertFieldValue(fset, src, f, accessVar, topSrcVar))
	}
	b.WriteString("}")
	return b.String()
}

func renderConvertFieldValue(fset *token.FileSet, src []byte, f convertField, accessVar, topSrcVar string) string {
	switch f.Kind {
	case convertKindOverride:
		text := exprText(fset, src, f.OverrideExpr)
		if f.OverrideKind == convertOverrideSetFn {
			// SetFn always receives the top-level Source, not the
			// nested struct — even when the override sits at a deep
			// path. The fn's signature is func(Source) V where Source
			// is the q.Convert call's source type.
			return "(" + text + ")(" + topSrcVar + ")"
		}
		return text
	case convertKindNested:
		return renderConvertLiteral(fset, src, f.NestedTy, accessVar+"."+f.Accessor, topSrcVar, f.Nested)
	default: // convertKindDirect
		return accessVar + "." + f.Accessor
	}
}
