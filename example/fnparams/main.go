// example/fnparams mirrors docs/api/fnparams.md one-to-one. Each
// snippet that builds a marked literal exercises the required-by-
// default discipline at compile time. Run with:
//
//	go run -toolexec=q ./example/fnparams
package main

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- Top-of-doc — LoadOptions ----------
type LoadOptions struct {
	_       q.FnParams
	Path    string
	Format  string
	Timeout time.Duration `q:"optional"`
	Logger  *slog.Logger  `q:"opt"`
}

func Load(o LoadOptions) string {
	return fmt.Sprintf("loading %s as %s (timeout=%v, logger=%v)",
		o.Path, o.Format, o.Timeout, o.Logger != nil)
}

// "Mixed required and optional — ConnectOptions":
type ConnectOptions struct {
	_              q.FnParams
	Host           string
	Port           int
	Database       string
	DialTimeout    time.Duration `q:"optional"`
	EnableTLS      bool          `q:"optional"`
	ConnectionPool int           `q:"optional"`
}

type DB struct {
	host string
	port int
	db   string
	tls  bool
}

func connect(o ConnectOptions) DB {
	return DB{host: o.Host, port: o.Port, db: o.Database, tls: o.EnableTLS}
}

// ---------- "Optional-field tag spelling — opt vs optional" ----------
type Mixed struct {
	_ q.ValidatedStruct
	A string
	B int  `q:"opt"`
	C bool `q:"optional"`
}

// ---------- "Nested marked structs" ----------
type DBOptions struct {
	_    q.FnParams
	Host string
	Port int
	TLS  bool `q:"optional"`
}

type ServerOptions struct {
	_        q.FnParams
	Bind     string
	DB       DBOptions
	Backup   *DBOptions           `q:"optional"`
	Replicas []DBOptions          `q:"optional"`
	NamedDBs map[string]DBOptions `q:"optional"`
}

func Server(o ServerOptions) string {
	return fmt.Sprintf("server bound to %s; primary db=%s:%d; %d replicas; %d named",
		o.Bind, o.DB.Host, o.DB.Port, len(o.Replicas), len(o.NamedDBs))
}

func main() {
	fmt.Println(Load(LoadOptions{Path: "/etc", Format: "yaml"}))

	db := connect(ConnectOptions{
		Host: "db.example.com", Port: 5432, Database: "prod",
	})
	fmt.Printf("connect.required: %+v\n", db)

	db = connect(ConnectOptions{
		Host: "db.example.com", Port: 5432, Database: "prod",
		DialTimeout: 10 * time.Second,
		EnableTLS:   true,
	})
	fmt.Printf("connect.with-opt: %+v\n", db)

	// Mixed — required A only.
	m := Mixed{A: "hello"}
	fmt.Printf("mixed: %+v\n", m)

	// Positional literal (every field set; the keyed-field check
	// doesn't apply). Doc shows ConnectOptions as the example, but
	// our struct has more fields than necessary; let's show a smaller
	// positional case using a freshly-defined struct so the example
	// stays terse.
	type Compact struct {
		_   q.FnParams
		X   int
		Y   string
		Tag bool `q:"opt"`
	}
	pos := Compact{q.FnParams{}, 1, "two", false}
	fmt.Printf("positional: %+v\n", pos)

	// Nested.
	srv := Server(ServerOptions{
		Bind: ":8080",
		DB:   DBOptions{Host: "primary", Port: 5432},
		Replicas: []DBOptions{
			{Host: "r1", Port: 5440},
			{Host: "r2", Port: 5441, TLS: true},
		},
		NamedDBs: map[string]DBOptions{
			"prod": {Host: "p", Port: 5500},
		},
	})
	fmt.Println(srv)
}
