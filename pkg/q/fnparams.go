package q

// fnparams.go — q.FnParams: the opt-in marker for required-by-default
// parameter structs.
//
// Surface:
//
//	type FnParams struct{}
//
// Add `_ q.FnParams` as the first (or any) field of a parameter
// struct to declare it required-by-default. The preprocessor then
// validates every struct literal of that type at compile time: every
// field must be named in the literal unless it carries a
// `q:"optional"` tag.
//
//	type LoadOptions struct {
//	    _       q.FnParams                                  // marker
//	    Path    string                                      // required
//	    Format  string                                      // required
//	    Timeout time.Duration `q:"optional"`                // optional
//	}
//
//	Load(LoadOptions{Path: "/etc", Format: "yaml"})         // OK
//	Load(LoadOptions{Path: "/etc"})                          // build error: Format is required
//	Load(LoadOptions{Path: "/etc", Format: "yaml",
//	     Timeout: 5*time.Second})                           // OK — optional set explicitly
//
// Properties.
//
//   - Zero size; the field adds no bytes to the struct layout.
//   - Marker presence is detected at compile time via go/types; no
//     runtime cost.
//   - Limit: only struct literals at their construction site are
//     checked. `p := Params{}; Foo(p)` is validated at the literal,
//     not at the Foo call.
//
// Plain runtime type — NOT rewritten by the preprocessor. The type
// itself is just an empty struct; all the work happens in the
// preprocessor's validation pass.
type FnParams struct{}

// ValidatedStruct is the general-purpose sibling of FnParams. The
// validation semantics are identical — every named field must be
// keyed in literals unless tagged `q:"optional"` (or `q:"opt"`).
// The two markers exist so users can pick the name that reads best
// at the use site:
//
//   - FnParams        — for function-parameter structs (typical use case).
//   - ValidatedStruct — for any other struct where literal
//                       construction should fail-loud on missing
//                       fields (DTOs, configuration objects, model
//                       structs, builder internals).
//
// Pick whichever name reads better; the preprocessor accepts both.
//
// Plain runtime type — NOT rewritten by the preprocessor.
type ValidatedStruct struct{}
