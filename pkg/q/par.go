package q

import (
	"context"
	"errors"
	"runtime"
	"sync"
)

// par.go — parallel variants of the data-ops family.
//
// Same shape as Map/FlatMap/Filter/ForEach/Exists/ForAll, but each
// fn invocation runs on a bounded worker pool. Default worker count:
// runtime.NumCPU().
// Override via q.WithPar(ctx, n) — the limit travels on ctx so it
// propagates through call graphs without re-threading at every layer:
//
//     ctx = q.WithPar(ctx, 8)
//     results := q.Try(q.ParMapErr(ctx, urls, fetch))
//     // any nested ParMap call down the stack reads the same limit
//
// Two flavours per op:
//
//   - Bare (ParMap, ParFilter, …) — no error path. ctx is read only
//     for the limit; cancellation is ignored (there's no error
//     return to bubble it through). Workers always run to completion.
//
//   - …Err (ParMapErr, ParFilterErr, …) — error return. First error
//     wins and stops further scheduling; ctx cancellation produces
//     ctx.Err() the same way. In-flight workers continue running but
//     their results are discarded (Go has no goroutine kill — pass
//     ctx into the user fn for true early shutdown).
//
// Compose with q.Try / q.TryE for the bubble path:
//
//     results := q.Try(q.ParMapErr(ctx, urls, fetch))
//     results := q.TryE(q.ParMapErr(ctx, urls, fetch)).Wrap("fetching urls")
//
// Implementation: bounded worker pool of min(limit, len(slice))
// workers reading job indices off an unbuffered channel. Output
// assembled by index so input order is preserved. Inspired by
// github.com/GiGurra/party and samber/lo PR #858; the dispatch
// loop's two-phase select (priority check on err/ctx, then send-or-
// error) is the core idea from the lo PR.

// parKey is the unexported context-key type for the per-request
// parallel limit. Stored as `int`: a value > 0 caps concurrency at
// that count; -1 (q.WithParUnbounded) opts out of the limit; 0 or
// missing falls back to runtime.NumCPU().
type parKey struct{}

// parUnbounded is the sentinel value stored on ctx by
// q.WithParUnbounded. -1 because 0 already means "unset" (q.GetPar
// returns NumCPU for that), and we need a distinct "no limit" value.
const parUnbounded = -1

// WithPar derives a child context that carries a worker-count limit
// for downstream q.Par* calls. limit must be > 0; non-positive values
// fall back to the default (runtime.NumCPU()) — use q.WithParUnbounded
// to opt out of the limit explicitly.
//
//	ctx = q.WithPar(ctx, 8)
//	results := q.Try(q.ParMapErr(ctx, urls, fetch))
func WithPar(ctx context.Context, limit int) context.Context {
	if limit <= 0 {
		// Treat as "unset" — caller is asking for the default.
		return context.WithValue(ctx, parKey{}, 0)
	}
	return context.WithValue(ctx, parKey{}, limit)
}

// WithParUnbounded derives a child context that opts out of the
// worker-count limit — every element of a q.Par* op gets its own
// goroutine. Use sparingly; the bounded form scales better for
// slices large enough to matter.
func WithParUnbounded(ctx context.Context) context.Context {
	return context.WithValue(ctx, parKey{}, parUnbounded)
}

// GetPar reads the worker-count limit from ctx (set by q.WithPar /
// q.WithParUnbounded). Returns runtime.NumCPU() when no limit is set;
// returns -1 (parUnbounded) when q.WithParUnbounded was used.
func GetPar(ctx context.Context) int {
	if ctx == nil {
		return runtime.NumCPU()
	}
	if v, ok := ctx.Value(parKey{}).(int); ok && v != 0 {
		return v
	}
	return runtime.NumCPU()
}

// resolveWorkers turns ctx's limit + slice length into the actual
// worker count to spawn.
func resolveWorkers(ctx context.Context, n int) int {
	if n <= 0 {
		return 0
	}
	limit := GetPar(ctx)
	if limit < 0 || limit > n {
		// Unbounded or limit-exceeds-n: one worker per item.
		return n
	}
	if limit < 1 {
		limit = 1
	}
	return limit
}

// ParMap applies fn to each element in parallel and returns the
// collected results in input order. fn cannot fail; for the fallible
// variant use q.ParMapErr.
//
// ctx is read for the worker-count limit (see q.WithPar) AND for
// cancellation: when ctx fires, dispatch stops immediately and
// in-flight workers run to completion (Go has no goroutine kill).
// The returned slice is the partial set — indices that were never
// dispatched hold the zero value of R. Callers who care about the
// distinction should check ctx.Err() after the call.
func ParMap[T, R any](ctx context.Context, slice []T, fn func(T) R) []R {
	if len(slice) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	out := make([]R, len(slice))
	workers := resolveWorkers(ctx, len(slice))
	work := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range work {
				out[i] = fn(slice[i])
			}
		}()
	}
	for i := range slice {
		select {
		case work <- i:
		case <-ctx.Done():
			close(work)
			wg.Wait()
			return out
		}
	}
	close(work)
	wg.Wait()
	return out
}

// ParMapErr is ParMap with a fallible fn. First error wins (returned
// directly) and stops scheduling further work; in-flight workers
// continue running but their results / errors are discarded. ctx
// cancellation produces the same first-bubble-then-stop behaviour
// (returns ctx.Err()).
//
//	results := q.Try(q.ParMapErr(ctx, urls, fetchURL))
func ParMapErr[T, R any](ctx context.Context, slice []T, fn func(context.Context, T) (R, error)) ([]R, error) {
	out := make([]R, len(slice))
	err := parRun(ctx, len(slice), func(c context.Context, i int) error {
		r, err := fn(c, slice[i])
		if err != nil {
			return err
		}
		out[i] = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ParFlatMap applies fn to each element in parallel, then concatenates
// the per-element slices in input order.
func ParFlatMap[T, R any](ctx context.Context, slice []T, fn func(T) []R) []R {
	if len(slice) == 0 {
		return nil
	}
	nested := ParMap(ctx, slice, fn)
	total := 0
	for _, s := range nested {
		total += len(s)
	}
	out := make([]R, 0, total)
	for _, s := range nested {
		out = append(out, s...)
	}
	return out
}

// ParFlatMapErr is ParFlatMap with a fallible fn.
func ParFlatMapErr[T, R any](ctx context.Context, slice []T, fn func(context.Context, T) ([]R, error)) ([]R, error) {
	nested, err := ParMapErr(ctx, slice, fn)
	if err != nil {
		return nil, err
	}
	total := 0
	for _, s := range nested {
		total += len(s)
	}
	out := make([]R, 0, total)
	for _, s := range nested {
		out = append(out, s...)
	}
	return out, nil
}

// ParFilter applies pred to each element in parallel and returns the
// matching elements in input order. Useful for IO-bound predicates
// (fetch-and-filter); for cheap predicates the sequential q.Filter is
// faster (no goroutine overhead).
//
// ctx cancellation stops dispatch; in-flight workers run to
// completion. Un-dispatched indices stay false in the mask, so the
// returned slice is naturally a partial filter result. Check
// ctx.Err() after the call to distinguish cancel from "no matches."
func ParFilter[T any](ctx context.Context, slice []T, pred func(T) bool) []T {
	if len(slice) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	mask := make([]bool, len(slice))
	workers := resolveWorkers(ctx, len(slice))
	work := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range work {
				mask[i] = pred(slice[i])
			}
		}()
	}
	cancelled := false
	for i := range slice {
		select {
		case work <- i:
		case <-ctx.Done():
			cancelled = true
		}
		if cancelled {
			break
		}
	}
	close(work)
	wg.Wait()
	out := make([]T, 0, len(slice))
	for i, ok := range mask {
		if ok {
			out = append(out, slice[i])
		}
	}
	return out
}

// ParFilterErr is ParFilter with a fallible predicate.
func ParFilterErr[T any](ctx context.Context, slice []T, pred func(context.Context, T) (bool, error)) ([]T, error) {
	mask := make([]bool, len(slice))
	err := parRun(ctx, len(slice), func(c context.Context, i int) error {
		ok, err := pred(c, slice[i])
		if err != nil {
			return err
		}
		mask[i] = ok
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(slice))
	for i, ok := range mask {
		if ok {
			out = append(out, slice[i])
		}
	}
	return out, nil
}

// ParForEach runs fn on every element in parallel; no result is
// collected. The fan-out form of "do X to each item, ignore the
// values." Symmetric with the sequential q.ForEach — swap to
// parallel by adding the Par prefix and a ctx.
//
// ctx cancellation stops dispatch; in-flight workers run to
// completion. There's no return path to bubble cancel — callers
// who care should check ctx.Err() after the call.
func ParForEach[T any](ctx context.Context, slice []T, fn func(T)) {
	if len(slice) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	workers := resolveWorkers(ctx, len(slice))
	work := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range work {
				fn(slice[i])
			}
		}()
	}
	cancelled := false
	for i := range slice {
		select {
		case work <- i:
		case <-ctx.Done():
			cancelled = true
		}
		if cancelled {
			break
		}
	}
	close(work)
	wg.Wait()
}

// ParForEachErr is ParForEach with a fallible fn. First error wins
// and stops further scheduling. Compose with q.Check / q.CheckE for
// the bubble path:
//
//	q.Check(q.ParForEachErr(ctx, urls, postURL))
func ParForEachErr[T any](ctx context.Context, slice []T, fn func(context.Context, T) error) error {
	return parRun(ctx, len(slice), func(c context.Context, i int) error {
		return fn(c, slice[i])
	})
}

// errParFound is the internal sentinel used by ParExists /
// ParForAll to signal "match found / non-match found" through
// parRun's first-error-wins channel. It is never surfaced to the
// caller — both helpers translate it back into a bool.
var errParFound = errors.New("q: parallel-helper sentinel — should never escape")

// ParExists reports whether any element satisfies pred — the parallel
// counterpart of q.Exists. Workers run in parallel; the first match
// causes the rest to wind down (subsequent dispatches see the sentinel
// and bail). Useful for IO-bound predicates ("does ANY of these URLs
// return 200?") where serial Exists would be slow.
//
// ctx is read for the limit; cancellation is honoured (cancellation
// produces false — workers are torn down without resolving the
// question). For the cancellation-aware shape that surfaces the
// reason, use ParExistsErr.
func ParExists[T any](ctx context.Context, slice []T, pred func(T) bool) bool {
	if len(slice) == 0 {
		return false
	}
	err := parRun(ctx, len(slice), func(_ context.Context, i int) error {
		if pred(slice[i]) {
			return errParFound
		}
		return nil
	})
	return errors.Is(err, errParFound)
}

// ParExistsErr is ParExists with a fallible pred. Returns
// (found bool, err error). The first true wins; the first non-nil
// err short-circuits with that err. ctx cancellation produces
// (false, ctx.Err()).
func ParExistsErr[T any](ctx context.Context, slice []T, pred func(context.Context, T) (bool, error)) (bool, error) {
	if len(slice) == 0 {
		return false, nil
	}
	err := parRun(ctx, len(slice), func(c context.Context, i int) error {
		ok, err := pred(c, slice[i])
		if err != nil {
			return err
		}
		if ok {
			return errParFound
		}
		return nil
	})
	if errors.Is(err, errParFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

// ParForAll reports whether every element satisfies pred — the
// parallel counterpart of q.ForAll. Vacuously true on an empty slice.
// Workers run in parallel; the first non-match causes the rest to
// wind down. Useful for IO-bound predicates ("do ALL of these URLs
// return 200?") where serial ForAll would be slow.
//
// ctx cancellation produces false (the question can't be resolved
// without all elements). For the cancellation-aware shape, use
// ParForAllErr.
func ParForAll[T any](ctx context.Context, slice []T, pred func(T) bool) bool {
	if len(slice) == 0 {
		return true
	}
	err := parRun(ctx, len(slice), func(_ context.Context, i int) error {
		if !pred(slice[i]) {
			return errParFound
		}
		return nil
	})
	return err == nil
}

// ParForAllErr is ParForAll with a fallible pred. Returns
// (allMatch bool, err error). First non-match wins (returns
// (false, nil)); first non-nil err wins (returns (false, err)).
// ctx cancellation produces (false, ctx.Err()).
func ParForAllErr[T any](ctx context.Context, slice []T, pred func(context.Context, T) (bool, error)) (bool, error) {
	if len(slice) == 0 {
		return true, nil
	}
	err := parRun(ctx, len(slice), func(c context.Context, i int) error {
		ok, err := pred(c, slice[i])
		if err != nil {
			return err
		}
		if !ok {
			return errParFound
		}
		return nil
	})
	if err == nil {
		return true, nil
	}
	if errors.Is(err, errParFound) {
		return false, nil
	}
	return false, err
}

// parRun is the bounded worker-pool core for the …Err variants.
//
// Returns the first error from fn or the first ctx cancellation,
// whichever fires first. On error / cancel, no further work is
// dispatched; in-flight workers continue and their results are
// discarded. Workers receive the (possibly cancelled) ctx so they
// can short-circuit on it themselves.
//
// The two-phase select in the dispatcher (priority check on err/ctx,
// then a send-or-err select) is borrowed from samber/lo PR #858 — it
// catches cancellation and errors before competing with work sends in
// a flat select.
func parRun(ctx context.Context, n int, fn func(context.Context, int) error) error {
	if n == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	workers := resolveWorkers(ctx, n)
	work := make(chan int)
	// 1-buffered: only the first push wins; subsequent errors hit the
	// default branch in the worker's send-or-skip select.
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range work {
				if err := fn(ctx, i); err != nil {
					select {
					case errCh <- err:
					default:
					}
				}
			}
		}()
	}
	dispatchErr := func() error {
		for i := range n {
			// Priority check before each send: errors and ctx cancel
			// short-circuit dispatch immediately rather than competing
			// with the work-channel send.
			select {
			case err := <-errCh:
				return err
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			select {
			case work <- i:
			case err := <-errCh:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}()
	close(work)
	wg.Wait()
	// All writers (workers) are done after wg.Wait(); reading from a
	// closed buffered channel returns the buffered value or nil.
	close(errCh)
	if dispatchErr != nil {
		return dispatchErr
	}
	return <-errCh
}
