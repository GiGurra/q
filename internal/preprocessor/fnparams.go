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

// fnParamsTypeName matches `(*types.Named).Obj().Name()` for the
// marker type. Combined with the package path, this uniquely
// identifies the marker across recompilations.
const fnParamsTypeName = "FnParams"

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
	required, ok := fnParamsRequiredFields(tv.Type)
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
		Msg:  fmt.Sprintf("q.FnParams: required field(s) %v not set in %s literal (mark optional fields with `q:\"optional\"` to opt them out)", missing, typeName),
	})
	return diags, true
}

// fnParamsRequiredFields returns the list of required field names on
// a type if it has the FnParams marker, plus a bool indicating
// whether the marker was found at all. Required = field is exported
// (named, not blank) AND not tagged `q:"optional"` AND not the marker
// itself.
//
// Cross-package types are handled because go/types resolves their
// fields and tags transparently.
func fnParamsRequiredFields(t types.Type) ([]string, bool) {
	st, ok := unwrapStruct(t)
	if !ok {
		return nil, false
	}
	hasMarker := false
	var required []string
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if isFnParamsMarker(f) {
			hasMarker = true
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
	if !hasMarker {
		return nil, false
	}
	return required, true
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

// isFnParamsMarker reports whether a struct field is the
// `_ q.FnParams` marker. The check matches by package path + type
// name so users can rename their q import alias freely.
func isFnParamsMarker(f *types.Var) bool {
	if f.Name() != "_" {
		return false
	}
	named, ok := f.Type().(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return obj.Pkg().Path() == qPkgImportPath && obj.Name() == fnParamsTypeName
}

// hasOptionalTag reports whether a struct tag carries the
// `q:"optional"` directive. Uses reflect.StructTag.Get for the parse,
// matching how go/types tags are spelled.
func hasOptionalTag(tag string) bool {
	v := reflect.StructTag(tag).Get("q")
	return v == "optional"
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
