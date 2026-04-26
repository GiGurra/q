// Fixture: distinct named types brand two values of the same
// underlying type for q.Assemble routing. Plain Go embedding gives
// PrimaryDB and ReplicaDB their own provider keys while *DB's
// methods promote naturally — no q.Tagged or brand-type ceremony.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type DB struct{ name string }

func (d *DB) String() string { return "DB:" + d.name }

// One line per branded type. Distinct named types; *DB methods
// promote via embedding.
type PrimaryDB struct{ *DB }
type ReplicaDB struct{ *DB }

type Server struct {
	primary string
	replica string
}

func newPrimary() PrimaryDB { return PrimaryDB{&DB{name: "primary"}} }
func newReplica() ReplicaDB { return ReplicaDB{&DB{name: "replica"}} }

func newServer(p PrimaryDB, r ReplicaDB) *Server {
	// Methods of *DB promote — no .Value() / UnTag step.
	return &Server{primary: p.String(), replica: r.String()}
}

func main() {
	s := q.Unwrap(q.Assemble[*Server](newPrimary, newReplica, newServer).Release())
	fmt.Println("primary:", s.primary)
	fmt.Println("replica:", s.replica)
}
