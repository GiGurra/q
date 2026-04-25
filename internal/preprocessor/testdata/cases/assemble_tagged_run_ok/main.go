// Fixture: q.Tagged[U, T] brands two values of the same underlying
// type as distinct types. q.Assemble's provider map keys on the full
// (branded) type, so two providers of *DB tagged differently are
// treated as distinct dep slots.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type DB struct{ name string }

type _primary struct{}
type _replica struct{}

type PrimaryDB = q.Tagged[*DB, _primary]
type ReplicaDB = q.Tagged[*DB, _replica]

type Server struct {
	primary *DB
	replica *DB
}

func newPrimary() PrimaryDB { return q.MkTag[_primary](&DB{name: "primary"}) }
func newReplica() ReplicaDB { return q.MkTag[_replica](&DB{name: "replica"}) }

func newServer(p PrimaryDB, r ReplicaDB) *Server {
	return &Server{primary: p.Value(), replica: r.Value()}
}

func main() {
	s := q.Unwrap(q.Assemble[*Server](newPrimary, newReplica, newServer))
	fmt.Println("primary:", s.primary.name)
	fmt.Println("replica:", s.replica.name)
}
