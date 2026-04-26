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
	"strconv"
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

	overrides, d, fail := parseConvertOverrides(fset, sc.ConvertOptArgs, info, targetTV.Type, srcTV.Type, qualifier)
	if fail {
		return d, true
	}

	fields, d, fail := resolveStructConversion(srcTV.Type, targetTV.Type, overrides, qualifier, pos, map[string]bool{})
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

// parseConvertOverrides walks the variadic q.Set / q.SetFn calls,
// validates each (target field name is a string literal naming an
// exported field; value/fn type is assignable), and builds an
// override map keyed by target field name.
func parseConvertOverrides(fset *token.FileSet, opts []ast.Expr, info *types.Info, targetType, srcType types.Type, qualifier types.Qualifier) (map[string]convertOverride, Diagnostic, bool) {
	mkErr := func(p token.Position, msg string) (map[string]convertOverride, Diagnostic, bool) {
		return nil, Diagnostic{
			File: p.Filename,
			Line: p.Line,
			Col:  p.Column,
			Msg:  msg,
		}, true
	}

	targetStruct, _ := targetType.Underlying().(*types.Struct)
	if targetStruct == nil {
		return map[string]convertOverride{}, Diagnostic{}, false
	}
	targetFields := map[string]*types.Var{}
	for i := 0; i < targetStruct.NumFields(); i++ {
		f := targetStruct.Field(i)
		if f.Exported() {
			targetFields[f.Name()] = f
		}
	}

	out := map[string]convertOverride{}
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
		nameLit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || nameLit.Kind != token.STRING {
			return mkErr(callPos, fmt.Sprintf("q.%s: targetField must be a string literal", method))
		}
		fieldName, err := strconv.Unquote(nameLit.Value)
		if err != nil {
			return mkErr(callPos, fmt.Sprintf("q.%s: cannot decode field name literal: %v", method, err))
		}
		tf, ok := targetFields[fieldName]
		if !ok {
			return mkErr(callPos, fmt.Sprintf("q.%s: target field %q is not an exported field of %s",
				method, fieldName, types.TypeString(targetType, qualifier)))
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
			if !types.AssignableTo(valueTV.Type, tf.Type()) {
				return mkErr(callPos, fmt.Sprintf("q.Set: target field %s.%s (%s) is not assignable from override value (%s)",
					types.TypeString(targetType, qualifier), tf.Name(),
					types.TypeString(tf.Type(), qualifier),
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
			if !types.AssignableTo(ret, tf.Type()) {
				return mkErr(callPos, fmt.Sprintf("q.SetFn: target field %s.%s (%s) is not assignable from fn return type (%s)",
					types.TypeString(targetType, qualifier), tf.Name(),
					types.TypeString(tf.Type(), qualifier),
					types.TypeString(ret, qualifier)))
			}
		}
		if _, dup := out[fieldName]; dup {
			return mkErr(callPos, fmt.Sprintf("q.Convert: duplicate override for target field %q", fieldName))
		}
		out[fieldName] = convertOverride{kind: ovKind, expr: valueExpr}
	}
	return out, Diagnostic{}, false
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
// and target struct types and an override map, build the per-field
// list. Recurses into nested structs (overrides do NOT propagate
// into nested calls — they apply only at the call's top-level
// Target).
func resolveStructConversion(srcType, targetType types.Type, overrides map[string]convertOverride, qualifier types.Qualifier, pos token.Position, seenPair map[string]bool) ([]convertField, Diagnostic, bool) {
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

		// 1. Override?
		if ov, ok := overrides[tf.Name()]; ok {
			fields = append(fields, convertField{
				Name:         tf.Name(),
				Kind:         convertKindOverride,
				OverrideKind: ov.kind,
				OverrideExpr: ov.expr,
			})
			continue
		}

		// 2. Direct copy?
		sf, hasSF := srcFields[tf.Name()]
		if hasSF && types.AssignableTo(sf.Type(), tf.Type()) {
			fields = append(fields, convertField{
				Name:     tf.Name(),
				Kind:     convertKindDirect,
				Accessor: sf.Name(),
			})
			continue
		}

		// 3. Nested struct derivation?
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

		// 4. Diagnostic.
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
func buildConvertReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string, counter int) string {
	target := sub.ConvertTargetTypeText
	if target == "" {
		target = "any"
	}
	srcText := exprTextSubst(fset, src, sub.ConvertSrc, subs, subTexts)

	if _, ok := sub.ConvertSrc.(*ast.Ident); ok {
		return renderConvertLiteral(fset, src, target, srcText, sub.ConvertFields)
	}
	srcVar := fmt.Sprintf("_qSrc%d", counter)
	literal := renderConvertLiteral(fset, src, target, srcVar, sub.ConvertFields)
	return fmt.Sprintf("(func() %s { %s := %s; return %s }())", target, srcVar, srcText, literal)
}

func renderConvertLiteral(fset *token.FileSet, src []byte, target, srcVar string, fields []convertField) string {
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
		b.WriteString(renderConvertFieldValue(fset, src, f, srcVar))
	}
	b.WriteString("}")
	return b.String()
}

func renderConvertFieldValue(fset *token.FileSet, src []byte, f convertField, srcVar string) string {
	switch f.Kind {
	case convertKindOverride:
		text := exprText(fset, src, f.OverrideExpr)
		if f.OverrideKind == convertOverrideSetFn {
			return "(" + text + ")(" + srcVar + ")"
		}
		return text
	case convertKindNested:
		return renderConvertLiteral(fset, src, f.NestedTy, srcVar+"."+f.Accessor, f.Nested)
	default: // convertKindDirect
		return srcVar + "." + f.Accessor
	}
}
