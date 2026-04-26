// Fixture: q.AssembleStruct[App] — field-decomposition multi-output.
// T's underlying must be a struct; each field becomes a separate dep
// target. Shared transitive deps (here *Config and *DB) build only
// once even though three fields consume them.
package main

import (
	"fmt"

	"github.com/GiGurra/q/pkg/q"
)

type Config struct{ Region string }
type DB struct{ cfg *Config }
type Server struct{ db *DB }
type Worker struct{ db *DB }
type Stats struct{ cfg *Config }

type App struct {
	Server *Server
	Worker *Worker
	Stats  *Stats
}

func newConfig() *Config       { return &Config{Region: "eu-west-1"} }
func newDB(c *Config) *DB      { return &DB{cfg: c} }
func newServer(d *DB) *Server  { return &Server{db: d} }
func newWorker(d *DB) *Worker  { return &Worker{db: d} }
func newStats(c *Config) *Stats { return &Stats{cfg: c} }

func main() {
	app := q.Unwrap(q.AssembleStruct[App](newConfig, newDB, newServer, newWorker, newStats).DeferCleanup())
	fmt.Println("server.db.cfg.Region:", app.Server.db.cfg.Region)
	fmt.Println("worker.db.cfg.Region:", app.Worker.db.cfg.Region)
	fmt.Println("stats.cfg.Region:", app.Stats.cfg.Region)
}
