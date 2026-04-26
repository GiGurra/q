package q

// FnParams is the opt-in marker for required-by-default *function
// parameter* structs — Go's stand-in for named arguments to a
// function. Add `_ q.FnParams` as a blank field, and the preprocessor
// validates every struct literal of that type at compile time: every
// named field must be keyed in the literal unless it carries a
// `q:"optional"` (or `q:"opt"`) tag.
//
// Build-failure example:
//
//	type LoadOptions struct {
//	    _       q.FnParams
//	    Path    string                                      // required
//	    Format  string                                      // required
//	    Timeout time.Duration `q:"opt"`                     // optional
//	}
//
//	func main() {
//	    Load(LoadOptions{Path: "/etc", Format: "yaml"})     // OK
//	    Load(LoadOptions{Path: "/etc"})                      // ✗ build error:
//	    //                                                   //   q.FnParams: required field(s) [Format] not set in
//	    //                                                   //   LoadOptions literal (mark optional fields with
//	    //                                                   //   `q:"optional"` to opt them out)
//	    Load(LoadOptions{})                                  // ✗ build error: required field(s) [Format Path] not set
//	}
//
// Properties.
//
//   - Zero size; the field adds no bytes to the struct layout.
//   - Marker presence is detected at compile time via go/types; no
//     runtime cost.
//   - Limit: only struct literals at their construction site are
//     checked. `p := LoadOptions{}; Load(p)` is validated at the
//     literal — not at the Load call.
//
// Plain runtime type — NOT rewritten by the preprocessor. The type
// itself is just an empty struct; all the work happens in the
// preprocessor's validation pass.
type FnParams struct{}

// ValidatedStruct is the general-purpose sibling of FnParams. The
// validation semantics are identical — every named field must be
// keyed in literals unless tagged `q:"optional"` (or `q:"opt"`). The
// two markers exist so users can pick the name that reads best at
// the use site:
//
//   - FnParams        — for function-parameter structs (typical use case).
//   - ValidatedStruct — for any other struct where literal
//                       construction should fail-loud on missing
//                       fields (DTOs, configuration objects, model
//                       structs, builder internals).
//
// Build-failure example:
//
//	type Config struct {
//	    _       q.ValidatedStruct
//	    Name    string                  // required
//	    Version int                     // required
//	    Logger  any `q:"opt"`           // optional
//	}
//
//	func main() {
//	    cfg1 := Config{Name: "app", Version: 1}             // OK
//	    cfg2 := Config{Name: "app"}                          // ✗ build error:
//	    //                                                   //   q.ValidatedStruct: required field(s) [Version]
//	    //                                                   //   not set in Config literal
//	    use(cfg1, cfg2)
//	}
//
// Pick whichever name reads better at your call sites; the
// preprocessor accepts both.
//
// Plain runtime type — NOT rewritten by the preprocessor.
type ValidatedStruct struct{}
