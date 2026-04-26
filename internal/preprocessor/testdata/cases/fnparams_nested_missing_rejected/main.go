// Fixture: q.FnParams negative — nested literals must be checked.
//
// Each nested literal below omits at least one required field on the
// inner marked struct. The preprocessor must surface every missing
// field across nesting shapes (value, pointer, slice, map).
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type DBOptions struct {
	_    q.FnParams
	Host string
	Port int
}

type ServerOptions struct {
	_         q.FnParams
	Bind      string
	DB        DBOptions
	Backup    *DBOptions           `q:"optional"`
	Replicas  []DBOptions          `q:"optional"`
	NamedDBs  map[string]DBOptions `q:"optional"`
}

func main() {
	// MISSING: Port in nested DB literal (value-nested).
	a := ServerOptions{
		Bind: ":8080",
		DB:   DBOptions{Host: "primary"},
	}
	fmt.Println(a)

	// MISSING: Host in nested Backup literal (pointer-nested).
	b := ServerOptions{
		Bind:   ":8080",
		DB:     DBOptions{Host: "h", Port: 1},
		Backup: &DBOptions{Port: 5433},
	}
	fmt.Println(b)

	// MISSING: Port in slice element [1].
	c := ServerOptions{
		Bind: ":8080",
		DB:   DBOptions{Host: "h", Port: 1},
		Replicas: []DBOptions{
			{Host: "r1", Port: 5440},
			{Host: "r2"},
		},
	}
	fmt.Println(c)

	// MISSING: Host in map value "stage".
	d := ServerOptions{
		Bind: ":8080",
		DB:   DBOptions{Host: "h", Port: 1},
		NamedDBs: map[string]DBOptions{
			"prod":  {Host: "p", Port: 5500},
			"stage": {Port: 5501},
		},
	}
	fmt.Println(d)
}
