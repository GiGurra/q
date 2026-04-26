package preprocessor

// from.go — typecheck + rewriter for q.Convert[Target](src).
//
// Detection lives in scanner.go (familyConvert branch). This file
// owns:
//
//   - convertField — the per-target-field record the rewriter emits.
//   - resolveConvert — pairs Target's exported fields with Source's
//     by exact name, asserts each pair is types.AssignableTo, and
//     populates sub.ConvertFields + sub.ConvertTargetTypeText.
//     Returns a diagnostic on the first missing or mismatched field.
//   - buildConvertReplacement — emits the struct-literal expression
//     (or an IIFE binding the source to a temp first when src is a
//     non-trivial expression).
//
// v1 scope (see pkg/q/from.go for the surface contract):
//
//   - Source AND Target must both reduce to *types.Struct via their
//     Underlying. Pointers, scalars, interfaces are rejected with a
//     diagnostic.
//   - Field matching is exported-name-only.
//   - Each (Target.F, Source.F) pair must satisfy types.AssignableTo.
//   - Source fields with no Target counterpart are silently dropped
//     (target-driven, like Chimney).

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
)

// convertField is one target-field-mapping the rewriter splices into
// the emitted Target{...} struct literal. Name is the target field's
// name; SrcAccessor is the source-side selector text appended after
// "<srcVar>." in the literal.
type convertField struct {
	Name        string
	SrcAccessor string
}

// resolveConvert validates the source and target types, walks Target's
// exported fields, pairs each with the same-named exported source
// field, and populates sub.ConvertFields + sub.ConvertTargetTypeText.
//
// Returns a fatal diagnostic on:
//   - Source or Target not a struct type.
//   - A target field with no source counterpart.
//   - A target field whose source counterpart isn't types.AssignableTo.
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

	targetStruct, ok := targetTV.Type.Underlying().(*types.Struct)
	if !ok {
		return mkErr(fmt.Sprintf("q.Convert: Target must be a struct type; got %s", types.TypeString(targetTV.Type, nil)))
	}
	srcStruct, ok := srcTV.Type.Underlying().(*types.Struct)
	if !ok {
		return mkErr(fmt.Sprintf("q.Convert: source must be a struct type; got %s", types.TypeString(srcTV.Type, nil)))
	}

	qualifier := func(p *types.Package) string {
		if p == nil || p.Path() == pkgPath {
			return ""
		}
		return p.Name()
	}
	targetText := types.TypeString(targetTV.Type, qualifier)
	srcText := types.TypeString(srcTV.Type, qualifier)

	srcFields := map[string]*types.Var{}
	for i := 0; i < srcStruct.NumFields(); i++ {
		f := srcStruct.Field(i)
		if !f.Exported() {
			continue
		}
		srcFields[f.Name()] = f
	}

	var fields []convertField
	for i := 0; i < targetStruct.NumFields(); i++ {
		tf := targetStruct.Field(i)
		if !tf.Exported() {
			continue
		}
		sf, ok := srcFields[tf.Name()]
		if !ok {
			return mkErr(fmt.Sprintf("q.Convert: target field %s.%s has no counterpart on source %s",
				targetText, tf.Name(), srcText))
		}
		if !types.AssignableTo(sf.Type(), tf.Type()) {
			return mkErr(fmt.Sprintf("q.Convert: target field %s.%s (%s) is not assignable from source field %s.%s (%s)",
				targetText, tf.Name(),
				types.TypeString(tf.Type(), qualifier),
				srcText, sf.Name(),
				types.TypeString(sf.Type(), qualifier)))
		}
		fields = append(fields, convertField{Name: tf.Name(), SrcAccessor: sf.Name()})
	}

	sc.ConvertTargetTypeText = targetText
	sc.ConvertFields = fields
	return Diagnostic{}, false
}

// buildConvertReplacement emits the struct literal that replaces the
// q.Convert call.
//
// Simple identifier source — emit a bare literal:
//
//	q.Convert[Target](user)  →  Target{F1: user.F1, F2: user.F2, ...}
//
// Non-trivial source (call, selector chain, parenthesised, etc.) —
// bind to a temp inside an IIFE so the source expression evaluates
// exactly once:
//
//	q.Convert[Target](getUser())  →
//	    func() Target { _qSrcN := getUser(); return Target{F1: _qSrcN.F1, ...} }()
func buildConvertReplacement(fset *token.FileSet, src []byte, sub qSubCall, subs []qSubCall, subTexts []string, counter int) string {
	target := sub.ConvertTargetTypeText
	if target == "" {
		target = "any"
	}
	srcText := exprTextSubst(fset, src, sub.ConvertSrc, subs, subTexts)

	// If the source is a bare identifier, splice it directly — no IIFE
	// needed and no side-effect surprise.
	if _, ok := sub.ConvertSrc.(*ast.Ident); ok {
		return renderConvertLiteral(target, srcText, sub.ConvertFields)
	}

	// Otherwise, bind to a temp inside an IIFE.
	srcVar := fmt.Sprintf("_qSrc%d", counter)
	literal := renderConvertLiteral(target, srcVar, sub.ConvertFields)
	return fmt.Sprintf("(func() %s { %s := %s; return %s }())", target, srcVar, srcText, literal)
}

// renderConvertLiteral emits Target{F1: srcVar.F1, F2: srcVar.F2, ...}.
// Empty-field-list case (target struct has no exported fields)
// degenerates to Target{}.
func renderConvertLiteral(target, srcVar string, fields []convertField) string {
	if len(fields) == 0 {
		return target + "{}"
	}
	out := target + "{"
	for i, f := range fields {
		if i > 0 {
			out += ", "
		}
		out += f.Name + ": " + srcVar + "." + f.SrcAccessor
	}
	out += "}"
	return out
}
