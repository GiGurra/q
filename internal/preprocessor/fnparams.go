package preprocessor

// fnparams.go — required-by-default parameter struct validation.
//
// Opt-in marker: `_ q.FnParams` field anywhere in a struct flips it
// to required-by-default. Per-field opt-out: `q:"optional"` tag.
//
// The validation pass walks every *ast.CompositeLit in each file,
// resolves its type via go/types, and — if the type carries the
// FnParams marker — checks the literal's keyed fields against the
// struct's required-fields set. Missing required fields produce a
// preprocess-time diagnostic.
//
// No data-flow analysis: only struct literals at their construction
// site are checked. The user said skip it; we honour that.

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"sort"
)

// validationMarkerNames are the q.* type names that flip a struct
// to required-by-default. Both spellings have identical validation
// semantics; the names exist so users can pick the one that reads
// best at the use site (FnParams when the struct is a function
// parameter, ValidatedStruct for any other required-by-default
// struct).
var validationMarkerNames = map[string]bool{
	"FnParams":        true,
	"ValidatedStruct": true,
}

// packageImportsQ reports whether any file in the package imports
// pkg/q. Used by the typecheck pass to decide whether running the
// full type-check + FnParams validation is justified — packages that
// don't import q skip the entire pass.
func packageImportsQ(files []*ast.File) bool {
	for _, file := range files {
		for _, imp := range file.Imports {
			if imp.Path != nil {
				v := imp.Path.Value
				// Strip surrounding quotes; v is always a quoted string
				// literal in valid Go AST.
				if len(v) >= 2 && v[1:len(v)-1] == qPkgImportPath {
					return true
				}
			}
		}
	}
	return false
}

// validateFnParams walks every CompositeLit in the supplied files and
// emits a diagnostic for each marked struct literal that omits a
// required field. Returns nil when no marker types appear or every
// literal is well-formed.
func validateFnParams(fset *token.FileSet, files []*ast.File, info *types.Info) []Diagnostic {
	var diags []Diagnostic
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			d, ok := checkFnParamsLit(fset, lit, info)
			if ok {
				diags = append(diags, d...)
			}
			return true
		})
	}
	return diags
}

// checkFnParamsLit type-checks a single CompositeLit. Returns a list
// of diagnostics (one per missing required field) plus a bool
// indicating whether the literal's type was marker-tagged. Returns
// (nil, false) when the literal isn't a marked struct.
func checkFnParamsLit(fset *token.FileSet, lit *ast.CompositeLit, info *types.Info) ([]Diagnostic, bool) {
	tv, ok := info.Types[lit]
	if !ok || tv.Type == nil {
		return nil, false
	}
	required, markerName, ok := fnParamsRequiredFields(tv.Type)
	if !ok {
		return nil, false
	}

	// Empty required set means every field is optional — nothing to
	// check. Common case for marker-only structs.
	if len(required) == 0 {
		return nil, true
	}

	// Positional literal — every field is set by construction.
	if len(lit.Elts) > 0 {
		if _, isKV := lit.Elts[0].(*ast.KeyValueExpr); !isKV {
			return nil, true
		}
	}

	present := make(map[string]bool, len(lit.Elts))
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		id, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		present[id.Name] = true
	}

	var diags []Diagnostic
	missing := make([]string, 0, len(required))
	for _, name := range required {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil, true
	}
	sort.Strings(missing)
	pos := fset.Position(lit.Pos())
	typeName := fnParamsLiteralTypeName(lit, tv.Type)
	diags = append(diags, Diagnostic{
		File: pos.Filename,
		Line: pos.Line,
		Col:  pos.Column,
		Msg:  fmt.Sprintf("q.%s: required field(s) %v not set in %s literal (mark optional fields with `q:\"optional\"` or `q:\"opt\"` to opt them out)", markerName, missing, typeName),
	})
	return diags, true
}

// fnParamsRequiredFields returns the list of required field names on
// a type, plus the name of the marker found (e.g. "FnParams" or
// "ValidatedStruct"), plus a bool indicating whether any marker was
// found at all. Required = field is exported (named, not blank) AND
// not tagged `q:"optional"`/`q:"opt"` AND not the marker itself.
//
// Cross-package types are handled because go/types resolves their
// fields and tags transparently.
func fnParamsRequiredFields(t types.Type) ([]string, string, bool) {
	st, ok := unwrapStruct(t)
	if !ok {
		return nil, "", false
	}
	markerName := ""
	var required []string
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if name, ok := fnParamsMarkerName(f); ok {
			markerName = name
			continue
		}
		if f.Name() == "_" {
			continue
		}
		if hasOptionalTag(st.Tag(i)) {
			continue
		}
		required = append(required, f.Name())
	}
	if markerName == "" {
		return nil, "", false
	}
	return required, markerName, true
}

// fnParamsMarkerName returns the marker type's bare name (e.g.
// "FnParams" or "ValidatedStruct") when the field is a recognised
// blank marker, or ("", false) otherwise.
func fnParamsMarkerName(f *types.Var) (string, bool) {
	if f.Name() != "_" {
		return "", false
	}
	named, ok := f.Type().(*types.Named)
	if !ok {
		return "", false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return "", false
	}
	if obj.Pkg().Path() != qPkgImportPath {
		return "", false
	}
	if !validationMarkerNames[obj.Name()] {
		return "", false
	}
	return obj.Name(), true
}

// unwrapStruct returns the *types.Struct underlying a type if any.
// Pointer / named / interface wrappers fall through to their
// underlying struct; non-struct types return false.
func unwrapStruct(t types.Type) (*types.Struct, bool) {
	for {
		switch u := t.(type) {
		case *types.Named:
			t = u.Underlying()
		case *types.Pointer:
			t = u.Elem()
		case *types.Struct:
			return u, true
		default:
			return nil, false
		}
	}
}

// hasOptionalTag reports whether a struct tag carries the
// `q:"optional"` (or short alias `q:"opt"`) directive. Uses
// reflect.StructTag.Get for the parse, matching how go/types tags
// are spelled.
func hasOptionalTag(tag string) bool {
	v := reflect.StructTag(tag).Get("q")
	return v == "optional" || v == "opt"
}

// fnParamsLiteralTypeName returns a human-friendly type name for the
// diagnostic. Uses the literal's Type AST when available (preserves
// the spelling the user wrote — package qualifier and all); falls
// back to the resolved type's String form.
func fnParamsLiteralTypeName(lit *ast.CompositeLit, t types.Type) string {
	if lit.Type != nil {
		switch tt := lit.Type.(type) {
		case *ast.Ident:
			return tt.Name
		case *ast.SelectorExpr:
			if x, ok := tt.X.(*ast.Ident); ok {
				return x.Name + "." + tt.Sel.Name
			}
		}
	}
	return t.String()
}
