// Package main is a small in-memory todo service that exercises a
// large slice of q's surface in one cohesive flow:
//
//   - q.Assemble + q.Scope        — DI graph + lifetime container
//   - q.NewScope                  — app scope owns assembled deps
//   - q.Open                      — resource acquisition (recipe shape)
//   - q.FnParams                  — required-by-default config struct
//   - q.OneOf3 + q.AsOneOf        — discriminated Status sum
//   - q.Match + q.OnType          — value-returning dispatch on a sum
//   - q.ConvertTo + q.SetFn       — internal Todo → DTO
//   - q.Lock                      — mutex sugar in the Store
//   - q.Async + q.AwaitAll        — fan-out / fan-in over futures
//   - q.ParForEach                — parallel iteration over a slice
//   - q.LazyFromThunk             — lazy-initialised feature-flag table
//   - q.RecvRawCtx                — ctx-aware channel receive
//   - q.Timeout + q.CheckCtx      — derived ctx + cancellation check
//   - q.Try / q.TryE / q.Unwrap   — error bubbling
//   - q.NotNil                    — nil-guard
//   - q.Require                   — runtime preconditions
//   - q.OkE                       — comma-ok bubble (with .Wrapf)
//   - q.As                        — type-assertion sugar
//   - q.At                        — nil-safe nested traversal
//   - q.Tern                      — inline ternary
//   - q.F                         — compile-time string interpolation
//   - q.SlogContextHandler        — slog wrapping for ctx attrs
//
// Run with:
//
//	go run -toolexec=q ./example/app
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GiGurra/q/pkg/q"
)

// ---------- q.FnParams: required-by-default config ----------

type Config struct {
	_            q.FnParams
	AppName      string
	MaxTodos     int
	WorkerQueue  int
	DefaultOwner string `q:"optional"`
}

func newConfig() *Config {
	return &Config{
		AppName:      "todoapp",
		MaxTodos:     100,
		WorkerQueue:  16,
		DefaultOwner: "anon",
	}
}

// ---------- q.OneOf3 + q.AsOneOf + q.Match: Status sum ----------

type Pending struct{}
type Done struct{ At string }
type Failed struct{ Reason string }

type Status q.OneOf3[Pending, Done, Failed]

func describeStatus(s Status) string {
	return q.Match(s,
		q.Case(Pending{}, "pending"),
		q.OnType(func(d Done) string { return "done@" + d.At }),
		q.OnType(func(f Failed) string { return "failed:" + f.Reason }),
	)
}

// ---------- Domain ----------

type Todo struct {
	ID     int
	Title  string
	Owner  string
	Status Status
}

type TodoDTO struct {
	ID         int
	Title      string
	Owner      string
	StatusText string
}

// StoreAPI is the interface seen by Service; *MemStore is the
// concrete impl. q.As recovers the concrete from the iface later.
type StoreAPI interface {
	Add(title, owner string) *Todo
	MarkDone(id int, at string) error
	MarkFailed(id int, reason string) error
	Get(id int) (*Todo, bool)
	List() []*Todo
}

// ---------- q.Lock: in-mem store ----------

type MemStore struct {
	mu    sync.Mutex
	next  int
	todos map[int]*Todo
}

func openMemStore() (*MemStore, error) {
	return &MemStore{todos: map[int]*Todo{}}, nil
}

func (s *MemStore) Add(title, owner string) *Todo {
	q.Lock(&s.mu)
	s.next++
	t := &Todo{
		ID:     s.next,
		Title:  title,
		Owner:  owner,
		Status: q.AsOneOf[Status](Pending{}),
	}
	s.todos[t.ID] = t
	return t
}

func (s *MemStore) MarkDone(id int, at string) error {
	q.Lock(&s.mu)
	t, ok := s.todos[id]
	if !ok {
		return errors.New("not found")
	}
	t.Status = q.AsOneOf[Status](Done{At: at})
	return nil
}

func (s *MemStore) MarkFailed(id int, reason string) error {
	q.Lock(&s.mu)
	t, ok := s.todos[id]
	if !ok {
		return errors.New("not found")
	}
	t.Status = q.AsOneOf[Status](Failed{Reason: reason})
	return nil
}

func (s *MemStore) Get(id int) (*Todo, bool) {
	q.Lock(&s.mu)
	t, ok := s.todos[id]
	return t, ok
}

func (s *MemStore) Len() int {
	q.Lock(&s.mu)
	return len(s.todos)
}

func (s *MemStore) List() []*Todo {
	q.Lock(&s.mu)
	out := make([]*Todo, 0, len(s.todos))
	for _, t := range s.todos {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// newStoreAPI binds StoreAPI to *MemStore in the DI graph.
func newStoreAPI(s *MemStore) StoreAPI { return s }

// ---------- q.LazyFromThunk: feature-flag table loaded on first use ----------

func loadFlags() map[string]bool {
	return map[string]bool{
		"audit":        true,
		"experimental": false,
	}
}

var flags = q.LazyFromThunk(loadFlags)

// ---------- q.SlogContextHandler: structured logger ----------

func newLogger() *slog.Logger {
	// Wrap a JSON handler with q.SlogContextHandler so any attrs
	// added via q.SlogCtx flow through. ReplaceAttr strips the time
	// key so test output is stable.
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	return slog.New(q.SlogContextHandler(base))
}

// ---------- Service ----------

type Service struct {
	cfg   *Config
	store StoreAPI
	log   *slog.Logger
}

func newService(cfg *Config, s StoreAPI, log *slog.Logger) *Service {
	return &Service{cfg: cfg, store: s, log: log}
}

// Add demonstrates q.NotNil / q.Require / q.Tern at one entry point.
func (svc *Service) Add(title, owner string) (*Todo, error) {
	q.NotNil(svc)
	q.Require(len(title) > 0, "title cannot be empty")
	resolvedOwner := q.Tern(owner == "", svc.cfg.DefaultOwner, owner)
	t := svc.store.Add(title, resolvedOwner)
	svc.log.Info("todo.add", slog.Int("id", t.ID), slog.String("owner", resolvedOwner))
	return t, nil
}

// GetOrErr returns the Todo, bubbling a not-found error via q.OkE.
// q.OkE takes (T, bool) and synthesises an error when !ok; .Wrapf
// adds context to the bubbled error.
func (svc *Service) GetOrErr(id int) (*Todo, error) {
	t := q.OkE(svc.store.Get(id)).Wrapf("todo %d", id)
	return t, nil
}

// ---------- q.ConvertTo: internal Todo → TodoDTO ----------

func toDTO(t *Todo) TodoDTO {
	return q.ConvertTo[TodoDTO](*t,
		q.SetFn(TodoDTO{}.StatusText, func(s Todo) string {
			return describeStatus(s.Status)
		}),
	)
}

// ---------- q.Async + q.AwaitAll: fan-out batch ----------

func batchAdd(svc *Service, titles []string) ([]TodoDTO, error) {
	// Phase 1: serial add — keeps IDs in title order so output is
	// deterministic. (Real apps might add inside the parallel block;
	// here we want a stable demo.)
	todos := make([]*Todo, len(titles))
	for i, t := range titles {
		td, err := svc.Add(t, "batch")
		if err != nil {
			return nil, err
		}
		todos[i] = td
	}
	// Phase 2: parallel per-todo work (MarkDone simulates the
	// expensive part) — fan out via q.Async, fan in via q.AwaitAll.
	futures := make([]q.Future[TodoDTO], len(todos))
	for i, td := range todos {
		futures[i] = q.Async(func() (TodoDTO, error) {
			if err := svc.store.MarkDone(td.ID, "12:00"); err != nil {
				return TodoDTO{}, err
			}
			return toDTO(td), nil
		})
	}
	return q.AwaitAll(futures...), nil
}

// ---------- q.ParForEach: parallel mutate over a slice ----------

func parMarkDone(svc *Service, ids []int, at string) {
	ctx := q.WithPar(context.Background(), 4)
	q.ParForEach(ctx, ids, func(id int) {
		_ = svc.store.MarkDone(id, at)
	})
}

// ---------- q.RecvRawCtx + q.CheckCtx + q.Timeout: worker loop ----------

type WorkItem struct {
	ID     int
	Action string
}

// runWorker drains the queue until it closes or ctx is cancelled.
// q.CheckCtx asserts cancellation up front; q.RecvRawCtx returns
// (T, error) — error from a closed channel or a cancelled ctx.
func runWorker(ctx context.Context, queue <-chan WorkItem, svc *Service) error {
	q.CheckCtx(ctx)
	processed := 0
	for {
		item, err := q.RecvRawCtx(ctx, queue)
		if err != nil {
			svc.log.Info("worker.exit", slog.Int("processed", processed), slog.String("reason", err.Error()))
			return nil
		}
		switch item.Action {
		case "done":
			_ = svc.store.MarkDone(item.ID, "worker")
		case "fail":
			_ = svc.store.MarkFailed(item.ID, "worker rejected")
		}
		processed++
	}
}

// ---------- q.At: nil-safe nested traversal ----------

type Lookup struct {
	Cache *MemStore
}

func ownerOrDefault(svc *Service, lk *Lookup, id int) string {
	t, _ := lk.Cache.Get(id)
	return q.At(t.Owner).Or(svc.cfg.DefaultOwner)
}

// ---------- q.As: type-assertion sugar (bubbles on bad assert) ----------

func peekConcreteCount(s StoreAPI) (int, error) {
	mem := q.As[*MemStore](s)
	return mem.Len(), nil
}

func newLookup(s StoreAPI) (*Lookup, error) {
	return &Lookup{Cache: q.As[*MemStore](s)}, nil
}

// ---------- q.F: compile-time string interpolation ----------

func banner(cfg *Config) string {
	// q.F supports any valid Go expression inside {…}, including
	// receiver chains. The rewriter extracts each placeholder, replaces
	// it with %v, and emits fmt.Sprintf(format, exprs...).
	return q.F("=== {cfg.AppName} (max={cfg.MaxTodos}, queue={cfg.WorkerQueue}) ===")
}

func main() {
	appScope := q.NewScope().DeferCleanup()

	// Assemble the *Service graph in one call. Recipes for *Config,
	// *MemStore, the StoreAPI binding, and *slog.Logger compose into
	// the *Service. Cleanups register with appScope.
	svc := q.Unwrap(q.Assemble[*Service](
		newConfig,
		openMemStore,
		newStoreAPI,
		newLogger,
		newService,
	).WithScope(appScope))

	fmt.Println(banner(svc.cfg))

	// q.LazyFromThunk — first .Value() pays the load cost.
	if flags.Value()["audit"] {
		fmt.Println("audit flag: ON")
	}

	// Single add — q.Unwrap pops the bubble at a non-error call site.
	t1 := q.Unwrap(svc.Add("first todo", ""))
	fmt.Printf("Added: id=%d title=%q owner=%s status=%s\n",
		t1.ID, t1.Title, t1.Owner, describeStatus(t1.Status))

	_ = svc.store.MarkDone(t1.ID, "10:00")

	// q.Async + q.AwaitAll — fan-out batch.
	dtos := q.Unwrap(batchAdd(svc, []string{"buy milk", "ship release", "review PR"}))
	for _, d := range dtos {
		fmt.Printf("Batch DTO: id=%d title=%q owner=%s status=%s\n",
			d.ID, d.Title, d.Owner, d.StatusText)
	}

	// q.ParForEach — parallel mutate.
	parMarkDone(svc, []int{2, 3, 4}, "par")

	// Mark one as failed to exercise the Failed arm of Status.
	_ = svc.store.MarkFailed(2, "network down")

	// q.RecvRawCtx + q.Timeout — worker drains the queue.
	queue := make(chan WorkItem, svc.cfg.WorkerQueue)
	queue <- WorkItem{ID: 1, Action: "fail"}
	queue <- WorkItem{ID: 3, Action: "done"}
	close(queue)

	ctx, cancel := context.WithCancel(context.Background())
	ctx = q.Timeout(ctx, 250*time.Millisecond)
	defer cancel()
	if err := runWorker(ctx, queue, svc); err != nil {
		fmt.Printf("worker error: %v\n", err)
	}

	// q.OkE — bubble not-found via comma-ok.
	if t, err := svc.GetOrErr(999); err != nil {
		fmt.Printf("GetOrErr(999): %v\n", err)
	} else {
		fmt.Printf("GetOrErr(999): %d\n", t.ID)
	}
	if t, err := svc.GetOrErr(t1.ID); err != nil {
		fmt.Printf("GetOrErr(%d): %v\n", t1.ID, err)
	} else {
		fmt.Printf("GetOrErr(%d): owner=%s status=%s\n", t.ID, t.Owner, describeStatus(t.Status))
	}

	// q.As — recover the concrete type from the interface.
	count := q.Unwrap(peekConcreteCount(svc.store))
	fmt.Printf("MemStore.Len() via q.As: %d\n", count)

	// q.At — nil-safe lookup with fallback.
	lk := q.Unwrap(newLookup(svc.store))
	fmt.Printf("ownerOrDefault(1)=%s\n", ownerOrDefault(svc, lk, 1))
	fmt.Printf("ownerOrDefault(999)=%s\n", ownerOrDefault(svc, lk, 999))

	// Final list, sorted.
	titles := []string{}
	for _, t := range svc.store.List() {
		d := toDTO(t)
		titles = append(titles, fmt.Sprintf("%d:%s/%s/%s", d.ID, d.Title, d.Owner, d.StatusText))
	}
	fmt.Println("All todos:", strings.Join(titles, " | "))

	fmt.Println("App shutting down (scope closes)")
}
