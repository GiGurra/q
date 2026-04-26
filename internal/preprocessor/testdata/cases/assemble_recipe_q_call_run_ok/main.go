// Fixture: q.Assemble accepts q.* calls as recipes.
//
// Recipe values are normally bare identifiers (`newDB`) or func
// literals. This fixture verifies that q.* calls returning function
// values (e.g., q.Try(loadFactory())) also work — the preprocessor
// hoists the q.* call into a pre-statement bind and uses the temp as
// the recipe.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ DB string }
type DB struct{ cfg *Config }
type Server struct {
	db  *DB
	cfg *Config
}

func loadConfigFactory() (func() *Config, error) {
	return func() *Config { return &Config{DB: "factory-loaded"} }, nil
}

func newDB(c *Config) *DB              { return &DB{cfg: c} }
func newServer(d *DB, c *Config) *Server { return &Server{db: d, cfg: c} }

func run() error {
	mkConfig := q.Try(loadConfigFactory()) // q.* in value position — already worked.
	srv, _, err := q.Assemble[*Server](mkConfig, newDB, newServer).NoDeferCleanup()
	if err != nil {
		return err
	}
	fmt.Println("server cfg:", srv.cfg.DB)
	return nil
}

func runDirect() error {
	// q.* call in function-reference position — this is the limitation
	// the fix lifts.
	srv, _, err := q.Assemble[*Server](
		q.Try(loadConfigFactory()),
		newDB,
		newServer,
	).NoDeferCleanup()
	if err != nil {
		return err
	}
	fmt.Println("direct server cfg:", srv.cfg.DB)
	return nil
}

// runChain — q.* call in recipe position, chained with .Wrap to test
// that the chain form also flows through the recipe slot.
func runChain() error {
	srv, _, err := q.Assemble[*Server](
		q.TryE(loadConfigFactory()).Wrap("loading config factory"),
		newDB,
		newServer,
	).NoDeferCleanup()
	if err != nil {
		return err
	}
	fmt.Println("chain server cfg:", srv.cfg.DB)
	return nil
}

// runMultiple — multiple recipes in q.* call position.
func runMultiple() error {
	srv, _, err := q.Assemble[*Server](
		q.Try(loadConfigFactory()),
		q.Try(loadDBFactory()),
		newServer,
	).NoDeferCleanup()
	if err != nil {
		return err
	}
	fmt.Println("multi server cfg:", srv.cfg.DB)
	return nil
}

func loadDBFactory() (func(*Config) *DB, error) {
	return func(c *Config) *DB { return &DB{cfg: c} }, nil
}

// runTern — q.Tern returning a function value in recipe position.
func runTern(useDev bool) error {
	devFactory := func() *Config { return &Config{DB: "dev"} }
	prodFactory := func() *Config { return &Config{DB: "prod"} }
	srv, _, err := q.Assemble[*Server](
		q.Tern(useDev, devFactory, prodFactory),
		newDB,
		newServer,
	).NoDeferCleanup()
	if err != nil {
		return err
	}
	fmt.Println("tern server cfg:", srv.cfg.DB)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Println("run err:", err)
	}
	if err := runDirect(); err != nil {
		fmt.Println("runDirect err:", err)
	}
	if err := runChain(); err != nil {
		fmt.Println("runChain err:", err)
	}
	if err := runMultiple(); err != nil {
		fmt.Println("runMultiple err:", err)
	}
	if err := runTern(true); err != nil {
		fmt.Println("runTern err:", err)
	}
}
