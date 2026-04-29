// example/assemble mirrors docs/api/assemble.md one-to-one. Each
// section of the doc has a matching function below, named after the
// snippet it demonstrates. Run with:
//
//	go run -toolexec=q ./example/assemble
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/GiGurra/q/pkg/q"
)

// Configure slog deterministically — q.WithAssemblyDebug and
// auto-cleanup paths can both write through slog.
func init() {
	h := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	slog.SetDefault(slog.New(h))
}

// ---------- Doc's running types ----------

type Config struct{ DB string }
type DB struct{ cfg *Config }
type Cache struct{ db *DB }
type Server struct {
	db    *DB
	cache *Cache
	cfg   *Config
}

func newConfig() *Config                                  { return &Config{DB: "primary"} }
func newDB(c *Config) *DB                                 { return &DB{cfg: c} }
func newCache(d *DB) *Cache                               { return &Cache{db: d} }
func newServer(d *DB, c *Cache, cfg *Config) *Server      { return &Server{db: d, cache: c, cfg: cfg} }

// ---------- "What q.Assemble does" / Basic — function-reference recipes ----------
//
//	server := q.Unwrap(q.Assemble[*Server](newServer, newCache, newDB, newConfig))
func basicAssemble() *Server {
	return q.Unwrap(q.Assemble[*Server](newServer, newCache, newDB, newConfig).DeferCleanup())
}

// ---------- ".DeferCleanup() — auto-defer (the fast path)" ----------
//
// .DeferCleanup() ties the assembled value's lifetime to the enclosing
// function. The shape is consume-in-function: don't return the value.
// For factory shapes that return the value, take a *q.Scope and use
// .WithScope(scope) — see boot() below.
//
//	func main() {
//	    server, err := q.Assemble[*Server](newConfig, openDB, newServer).DeferCleanup()
//	    if err != nil { log.Fatal(err) }
//	    server.Run()
//	}
func deferCleanupDemo() string {
	server, err := q.Assemble[*Server](newConfig, newDB, newCache, newServer).DeferCleanup()
	if err != nil {
		return "err: " + err.Error()
	}
	return "ok cfg=" + server.cfg.DB
}

// boot() is the factory shape — caller passes in a scope, the
// assembled server's lifetime is the scope's. The caller gets a live
// instance back and decides when to close the scope.
//
//	func boot(scope *q.Scope) (*Server, error) {
//	    return q.Assemble[*Server](newConfig, openDB, newServer).WithScope(scope)
//	}
func boot(scope *q.Scope) (*Server, error) {
	return q.Assemble[*Server](newConfig, newDB, newCache, newServer).WithScope(scope)
}

// ---------- ".NoDeferCleanup() — caller-managed shutdown" ----------
//
//	server, shutdown, err := q.Assemble[*Server](recipes...).NoDeferCleanup()
func nodeferShutdown() (*Server, error) {
	server, shutdown, err := q.Assemble[*Server](newConfig, newDB, newCache, newServer).NoDeferCleanup()
	if err != nil {
		return nil, err
	}
	defer shutdown()
	return server, nil
}

// ---------- ".WithScope(scope)" ----------
//
//	scope := q.NewScope().DeferCleanup()
//	server := q.Try(q.Assemble[*Server](newConfig, newDB, newServer).WithScope(scope))
func withScopeDemo() (*Server, *Cache, error) {
	scope := q.NewScope().DeferCleanup()
	server, err := q.Assemble[*Server](newConfig, newDB, newCache, newServer).WithScope(scope)
	if err != nil {
		return nil, nil, err
	}
	// Same scope, second assembly. newConfig / newDB / newCache hit the cache.
	cache, err := q.Assemble[*Cache](newConfig, newDB, newCache).WithScope(scope)
	if err != nil {
		return nil, nil, err
	}
	return server, cache, nil
}

// ---------- "Errored recipes" ----------

type ErrConfig struct{ URL string }
type ErrDB struct{ cfg *ErrConfig }

func newErrConfig() *ErrConfig { return &ErrConfig{URL: ""} } // fail-trigger

func newErrDB(c *ErrConfig) (*ErrDB, error) {
	if c.URL == "" {
		return nil, errors.New("missing db url")
	}
	return &ErrDB{cfg: c}, nil
}

func bootErrored() (*ErrDB, error) {
	db, err := q.Assemble[*ErrDB](newErrConfig, newErrDB).DeferCleanup()
	return db, err
}

// ---------- "Inline values as recipes" ----------
//
//	customCfg := &Config{DB: "override"}
//	server := q.Unwrap(q.Assemble[*Server](customCfg, newDB, newCache, newServer))
func inlineValueDemo() *Server {
	customCfg := &Config{DB: "override"}
	return q.Unwrap(q.Assemble[*Server](customCfg, newDB, newCache, newServer).DeferCleanup())
}

// ---------- "context.Context — just another dependency" ----------

type CtxConfig struct{ Region string }

func newCtxConfig(_ context.Context) *CtxConfig { return &CtxConfig{Region: "eu-west-1"} }

func ctxAsRecipe() *CtxConfig {
	ctx := context.Background()
	return q.Unwrap(q.Assemble[*CtxConfig](ctx, newCtxConfig).DeferCleanup())
}

// ---------- "Branded variants — two databases, no special code" ----------

type BrandedDB struct{ name string }

func (d *BrandedDB) Query() string { return "Q@" + d.name }

type PrimaryDB struct{ *BrandedDB }
type ReplicaDB struct{ *BrandedDB }

func newPrimary() PrimaryDB { return PrimaryDB{&BrandedDB{name: "primary"}} }
func newReplica() ReplicaDB { return ReplicaDB{&BrandedDB{name: "replica"}} }

type BrandedServer struct {
	primary, replica string
}

func newBrandedServer(p PrimaryDB, r ReplicaDB) *BrandedServer {
	return &BrandedServer{primary: p.Query(), replica: r.Query()}
}

func brandedDemo() *BrandedServer {
	return q.Unwrap(q.Assemble[*BrandedServer](newPrimary, newReplica, newBrandedServer).DeferCleanup())
}

// ---------- "Interface inputs satisfied by concrete providers" ----------

type Greeter interface{ Greet() string }

type EnglishGreeter struct{}

func (EnglishGreeter) Greet() string { return "hello" }

type App struct{ g Greeter }

func newGreeter() *EnglishGreeter { return &EnglishGreeter{} }
func newApp(g Greeter) *App       { return &App{g: g} }

func interfaceDemo() *App {
	return q.Unwrap(q.Assemble[*App](newGreeter, newApp).DeferCleanup())
}

// ---------- "q.PermitNil" ----------

type OptCache struct{}

// newOptionalCache returns nil — "no cache configured".
func newOptionalCache() *OptCache { return nil }

type ServerWithOptCache struct{ cache *OptCache }

func newServerWithOpt(c *OptCache) *ServerWithOptCache {
	return &ServerWithOptCache{cache: c}
}

func permitNilDemo() (*ServerWithOptCache, error) {
	return q.Assemble[*ServerWithOptCache](
		q.PermitNil(newOptionalCache),
		newServerWithOpt,
	).DeferCleanup()
}

// ---------- "q.AssembleAll" ----------

type Plugin interface{ Name() string }

type AuthPlugin struct{}
type LogPlugin struct{}
type MetricsPlugin struct{}

func (AuthPlugin) Name() string    { return "auth" }
func (LogPlugin) Name() string     { return "log" }
func (MetricsPlugin) Name() string { return "metrics" }

func newAuth() Plugin    { return AuthPlugin{} }
func newLog() Plugin     { return LogPlugin{} }
func newMetrics() Plugin { return MetricsPlugin{} }

func assembleAllDemo() []Plugin {
	return q.Unwrap(q.AssembleAll[Plugin](newAuth, newLog, newMetrics).DeferCleanup())
}

// ---------- "q.AssembleStruct" ----------

type Worker struct{ db *DB }
type Stats struct{ cfg *Config }

func newWorker(d *DB) *Worker  { return &Worker{db: d} }
func newStats(c *Config) *Stats { return &Stats{cfg: c} }

type AppBundle struct {
	Server *Server
	Worker *Worker
	Stats  *Stats
}

func assembleStructDemo() (AppBundle, error) {
	return q.AssembleStruct[AppBundle](newConfig, newDB, newCache, newServer, newWorker, newStats).DeferCleanup()
}

// ---------- "q.WithAssemblyDebug" ----------

func debugDemo() *Server {
	ctx := q.WithAssemblyDebug(context.Background())
	return q.Unwrap(q.Assemble[*Server](ctx, newConfig, newDB, newCache, newServer).DeferCleanup())
}

func main() {
	s := basicAssemble()
	fmt.Printf("basicAssemble: server.cfg.DB=%s\n", s.cfg.DB)

	fmt.Printf("deferCleanupDemo: %s\n", deferCleanupDemo())

	bootScope := q.NewScope().DeferCleanup()
	if s, err := boot(bootScope); err != nil {
		fmt.Printf("boot: err=%s\n", err)
	} else {
		fmt.Printf("boot: ok cfg=%s\n", s.cfg.DB)
	}

	if s, err := nodeferShutdown(); err != nil {
		fmt.Printf("nodeferShutdown: err=%s\n", err)
	} else {
		fmt.Printf("nodeferShutdown: ok cfg=%s\n", s.cfg.DB)
	}

	if s, c, err := withScopeDemo(); err != nil {
		fmt.Printf("withScopeDemo: err=%s\n", err)
	} else {
		fmt.Printf("withScopeDemo: ok server.cfg=%s cache.db.cfg=%s\n", s.cfg.DB, c.db.cfg.DB)
	}

	if _, err := bootErrored(); err != nil {
		fmt.Printf("bootErrored: err=%s\n", err)
	}

	s2 := inlineValueDemo()
	fmt.Printf("inlineValueDemo: cfg=%s\n", s2.cfg.DB)

	cc := ctxAsRecipe()
	fmt.Printf("ctxAsRecipe: region=%s\n", cc.Region)

	bs := brandedDemo()
	fmt.Printf("brandedDemo: primary=%s replica=%s\n", bs.primary, bs.replica)

	a := interfaceDemo()
	fmt.Printf("interfaceDemo: greet=%s\n", a.g.Greet())

	if s3, err := permitNilDemo(); err != nil {
		fmt.Printf("permitNilDemo: err=%s\n", err)
	} else {
		fmt.Printf("permitNilDemo: cache=%v\n", s3.cache)
	}

	plugins := assembleAllDemo()
	names := make([]string, len(plugins))
	for i, p := range plugins {
		names[i] = p.Name()
	}
	fmt.Printf("assembleAllDemo: %v\n", names)

	if ab, err := assembleStructDemo(); err != nil {
		fmt.Printf("assembleStructDemo: err=%s\n", err)
	} else {
		fmt.Printf("assembleStructDemo: server.cfg=%s worker.db.cfg=%s stats.cfg=%s\n",
			ab.Server.cfg.DB, ab.Worker.db.cfg.DB, ab.Stats.cfg.DB)
	}

	ds := debugDemo()
	fmt.Printf("debugDemo: cfg=%s\n", ds.cfg.DB)
}
