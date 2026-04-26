package q

// sealed.go — interface-based sealed sum types.
//
// q.Sealed is the interface-based companion of q.OneOfN: each
// variant lives as its own type at runtime (no Tag/Value boxing),
// and the carrier is a marker interface that the variants implement
// via auto-synthesised marker methods. The closed set is registered
// at package level via a directive call, and the q preprocessor
// synthesises one `func (Variant) markerName() {}` per variant in
// a companion file.
//
//	type Message interface{ message() }   // 1-line marker — name is yours
//
//	type Ping       struct{ ID int }
//	type Pong       struct{ ID int }
//	type Disconnect struct{ Reason string }
//
//	var _ = q.Sealed[Message](Ping{}, Pong{}, Disconnect{})
//
// After the preprocessor runs, Ping / Pong / Disconnect each have a
// synthesised `func (X) message() {}` so they implement Message.
// User code passes them through any Message-typed channel / param /
// field as themselves — no wrapper, no unwrap.
//
// Producer:
//
//	ch <- Ping{ID: 1}                     // Ping implements Message — flows as itself
//
// Consumer (statement form, the standard for handlers):
//
//	for m := range ch {
//	    switch v := q.Exhaustive(m).(type) {
//	    case Ping:       handlePing(v)
//	    case Pong:       handlePong(v)
//	    case Disconnect: handleDisconnect(v)
//	    }
//	}
//
// Coverage on the type switch is enforced by q.Exhaustive's
// integration with q.Sealed: every variant in the closed set must
// have a case clause (or `default:` opts out).
//
// For the value-returning expression form, q.Match + q.OnType works
// the same way it does on q.OneOfN values:
//
//	desc := q.Match(m,
//	    q.OnType(func(p Ping) string       { return fmt.Sprintf("ping %d", p.ID) }),
//	    q.OnType(func(p Pong) string       { return fmt.Sprintf("pong %d", p.ID) }),
//	    q.OnType(func(d Disconnect) string { return fmt.Sprintf("dc: %s", d.Reason) }),
//	)
//
// Constraints:
//
//   - The marker interface (I) MUST have exactly one method with no
//     arguments and no return values — the marker. Multi-method
//     interfaces are rejected (q.Sealed is the marker pattern; for
//     richer interfaces, write the impls yourself on each variant).
//   - The variants MUST live in the same package as the q.Sealed
//     declaration. Go disallows method declarations on types defined
//     in another package, so the preprocessor cannot synthesise the
//     marker on a foreign type. Cross-package variants need an
//     explicit marker method written by the user — fall back to
//     plain Go in that case.
//   - The variadic args ARE zero-value type carriers — only their
//     types matter, the values are throwaway. The preprocessor
//     reads their static types via go/types and ignores any runtime
//     value.

// Sealed declares the closed-set of variant types for the marker
// interface I. The preprocessor extracts I's single marker method
// from go/types, then synthesises a `func (V) markerName() {}` on
// each variadic variant V in a companion file, so each V satisfies
// I via the synthesised marker.
//
// I must be an interface with exactly one method (no args, no
// results). Each variant must be a defined named type in the same
// package as the q.Sealed call. Returns a GenMarker so the call can
// sit in `var _ = ...` at package level (same shape as
// q.GenStringer).
//
// The Sealed declaration also registers the closed set with the
// preprocessor, enabling q.Exhaustive coverage on type switches over
// I values and q.Match + q.OnType integration.
//
// Example:
//
//	type Message interface{ message() }
//
//	type Ping       struct{ ID int }
//	type Pong       struct{ ID int }
//	type Disconnect struct{ Reason string }
//
//	var _ = q.Sealed[Message](Ping{}, Pong{}, Disconnect{})
//
// After preprocessing, each variant satisfies Message:
//
//	var m Message = Ping{ID: 1}    // OK — synthesised Ping.message()
func Sealed[I any](variants ...any) GenMarker {
	panicUnrewritten("q.Sealed")
	return GenMarker{}
}
