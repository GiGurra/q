// sub package — defines its own Status atom with the same bare name
// as main's Status. The fully-qualified atom value (import-path +
// type-name) keeps them distinct at the q.Atom level.
package sub

import "github.com/GiGurra/q/pkg/q"

type Status q.Atom

func Get() q.Atom { return q.AtomOf[Status]() }
