// Fixture: q.FnParams happy paths across nesting shapes.
//
// Validates that nested marked struct literals are checked the same
// way as top-level ones — the preprocessor walks every CompositeLit,
// so nesting falls out of `ast.Inspect`'s recursion. Covers value
// nesting, pointer nesting, slice elements, and map values.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type DBOptions struct {
	_    q.FnParams
	Host string
	Port int
	TLS  bool `q:"optional"`
}

type ServerOptions struct {
	_         q.FnParams
	Bind      string
	DB        DBOptions             // value-nested marked struct
	Backup    *DBOptions            `q:"optional"` // pointer-nested, optional
	Replicas  []DBOptions           `q:"optional"` // slice of marked struct
	NamedDBs  map[string]DBOptions  `q:"optional"` // map values are marked struct
}

func main() {
	// (1) Value-nested: inner literal supplies every required field.
	s1 := ServerOptions{
		Bind: ":8080",
		DB:   DBOptions{Host: "primary", Port: 5432},
	}
	fmt.Println("s1.DB:", s1.DB)

	// (2) Pointer-nested: &DBOptions{...} — inner literal is still a
	//     CompositeLit; the rewriter walks it.
	s2 := ServerOptions{
		Bind:   ":8080",
		DB:     DBOptions{Host: "primary", Port: 5432},
		Backup: &DBOptions{Host: "backup", Port: 5433},
	}
	fmt.Println("s2.Backup.Host:", s2.Backup.Host)

	// (3) Slice element: each element is its own CompositeLit.
	s3 := ServerOptions{
		Bind: ":8080",
		DB:   DBOptions{Host: "primary", Port: 5432},
		Replicas: []DBOptions{
			{Host: "r1", Port: 5440},
			{Host: "r2", Port: 5441, TLS: true},
		},
	}
	fmt.Println("s3.Replicas[0]:", s3.Replicas[0])

	// (4) Map value: values are CompositeLits with elided type.
	s4 := ServerOptions{
		Bind: ":8080",
		DB:   DBOptions{Host: "primary", Port: 5432},
		NamedDBs: map[string]DBOptions{
			"prod":  {Host: "p", Port: 5500},
			"stage": {Host: "s", Port: 5501},
		},
	}
	fmt.Println("s4.NamedDBs[prod]:", s4.NamedDBs["prod"])

	// (5) Optional-tagged nested marker: when the field is set, the
	//     inner literal is still validated.
	s5 := ServerOptions{
		Bind:   ":8080",
		DB:     DBOptions{Host: "primary", Port: 5432},
		Backup: &DBOptions{Host: "backup", Port: 5433, TLS: true},
	}
	fmt.Println("s5.Backup.TLS:", s5.Backup.TLS)
}
