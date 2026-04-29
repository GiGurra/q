// example/open mirrors docs/api/open.md one-to-one. Each section of
// the doc has a matching function below, named after the snippet it
// demonstrates. Run with:
//
//	go run -toolexec=q ./example/open
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"syscall"

	"github.com/GiGurra/q/pkg/q"
)

// Configure slog to write to stdout with the time attribute stripped
// so the example's output is deterministic — q.Open's err-returning
// cleanup forms log via slog.Error, and we want those lines to appear
// in expected_run.txt at a stable position.
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

// Conn / dial / fallbackConn / process / cleanup / loadValue / makeChan
// are the doc's referents. Kept tiny so each q.Open snippet reads as
// it does in the doc — only structure, no fake-framework cruft.

type Conn struct{ id int }

func (c *Conn) Close() { closeLog = append(closeLog, "conn-"+itoa(c.id)) }

var closeLog []string

func dial(addr string) (*Conn, error) {
	switch addr {
	case "":
		return nil, errors.New("empty addr")
	case "refused":
		return nil, syscall.ECONNREFUSED
	}
	return &Conn{id: len(addr)}, nil
}

func fallbackConn() *Conn { return &Conn{id: 99} }

func process(c *Conn, _ *fakeFile) error { _ = c; return nil }

func cleanup(c *Conn) { c.Close() }

// loadValue stands in for the doc's loadValue(key) — a (T, error) call
// whose T has no Close method, exercising .NoDeferCleanup()'s reason
// for being.
func loadValue(key string) (string, error) {
	if key == "" {
		return "", errors.New("empty key")
	}
	return "value-" + key, nil
}

// makeChan stands in for the doc's makeChan() — a (T, error) call
// returning a channel, so .DeferCleanup() (no args) infers `close(ch)`.
func makeChan() (chan int, error) { return make(chan int, 1), nil }

// fakeFile stands in for *os.File so the LIFO snippet doesn't need
// a real file. (*fakeFile).Close has the same shape (() -> error) as
// (*os.File).Close.
type fakeFile struct{ id int }

func (f *fakeFile) Close() error { closeLog = append(closeLog, "file-"+itoa(f.id)); return nil }

func openFile(path string) (*fakeFile, error) {
	if path == "" {
		return nil, errors.New("empty path")
	}
	return &fakeFile{id: len(path)}, nil
}

// ---------- "What q.Open does" ----------
//
//	conn := q.Open(dial(addr)).DeferCleanup((*Conn).Close)
func whatQOpenDoes(addr string) error {
	conn := q.Open(dial(addr)).DeferCleanup((*Conn).Close)
	_ = conn
	return nil
}

// ---------- ".DeferCleanup() (zero args)" — auto-cleanup dispatch ----------
// Three rows from the doc's table: chan, Close()error, Close().

func autoChan() error {
	ch := q.Open(makeChan()).DeferCleanup()
	ch <- 1 // demonstrate the channel can still be used; the deferred close fires on return
	return nil
}

// `Close() error` shape (auto: defer func(){ _=v.Close() }())
func autoCloseError() error {
	file := q.Open(openFile("cfg")).DeferCleanup()
	_ = file
	return nil
}

// `Close()` no-return — Conn has Close() with no return.
func autoCloseVoid() error {
	conn := q.Open(dial("local")).DeferCleanup()
	_ = conn
	return nil
}

// ---------- Auto-cleanup composes with OpenE shape methods ----------
//
//	file := q.OpenE(os.Open(path)).Wrap("loading config").DeferCleanup()
func autoCleanupWithWrap() error {
	file := q.OpenE(openFile("cfg")).Wrap("loading config").DeferCleanup()
	_ = file
	return nil
}

// ---------- ".NoDeferCleanup() — opt-in no-cleanup terminal" ----------
//
//	val := q.Open(loadValue(key)).NoDeferCleanup()
func noDeferCleanup(key string) (string, error) {
	val := q.Open(loadValue(key)).NoDeferCleanup()
	return val, nil
}

//	val := q.OpenE(loadValue(key)).Wrap("loading").NoDeferCleanup()
func noDeferCleanupWithWrap(key string) (string, error) {
	val := q.OpenE(loadValue(key)).Wrap("loading").NoDeferCleanup()
	return val, nil
}

// ---------- "Chain methods on q.OpenE" ----------

func openEErr(addr string) error {
	conn := q.OpenE(dial(addr)).Err(errors.New("dialing failed")).DeferCleanup((*Conn).Close)
	_ = conn
	return nil
}

func openEErrF(addr string) error {
	conn := q.OpenE(dial(addr)).ErrF(func(e error) error { return fmt.Errorf("transformed: %w", e) }).DeferCleanup((*Conn).Close)
	_ = conn
	return nil
}

func openEWrap(addr string) error {
	conn := q.OpenE(dial(addr)).Wrap("dialing").DeferCleanup((*Conn).Close)
	_ = conn
	return nil
}

func openEWrapf(addr string) error {
	conn := q.OpenE(dial(addr)).Wrapf("dialing %q", addr).DeferCleanup((*Conn).Close)
	_ = conn
	return nil
}

// "Example chain" — Wrap then DeferCleanup with explicit cleanup.
func exampleChain(addr string) error {
	conn := q.OpenE(dial(addr)).Wrap("dialing").DeferCleanup((*Conn).Close)
	_ = conn
	return nil
}

// "and with recovery" — Catch on ECONNREFUSED, fallbackConn feeds
// DeferCleanup's cleanup.
func recoveryChain(addr string) error {
	conn := q.OpenE(dial(addr)).Catch(func(e error) (*Conn, error) {
		if errors.Is(e, syscall.ECONNREFUSED) {
			return fallbackConn(), nil
		}
		return nil, e
	}).DeferCleanup((*Conn).Close)
	_ = conn
	return nil
}

// ---------- "LIFO cleanup across multiple Opens" ----------
//
//	func work(addr, path string) error {
//	    conn := q.Open(dial(addr)).DeferCleanup((*Conn).Close)
//	    f    := q.Open(os.Open(path)).DeferCleanup((*os.File).Close)
//	    return process(conn, f)
//	}
// `Conn.Close` is void and `*fakeFile.Close` returns error — the
// preprocessor accepts either shape on .DeferCleanup(cleanup), so
// both can be passed directly as method values.
func work(addr, path string) error {
	conn := q.Open(dial(addr)).DeferCleanup((*Conn).Close)
	f := q.Open(openFile(path)).DeferCleanup((*fakeFile).Close)
	return process(conn, f)
}

// ---------- "Statement forms" ----------

func formDefine(addr string) error {
	conn := q.Open(dial(addr)).DeferCleanup(cleanup)
	_ = conn
	return nil
}

func formAssign(addr string) error {
	var arr [1]*Conn
	arr[0] = q.Open(dial(addr)).DeferCleanup(cleanup)
	_ = arr[0]
	return nil
}

func formDiscard(addr string) error {
	q.Open(dial(addr)).DeferCleanup(cleanup)
	return nil
}

func formReturnPosition(addr string) (*Conn, error) {
	return q.Open(dial(addr)).NoDeferCleanup(), nil // return-position; defer would be useless
}

func formHoist(addr string) (int, error) {
	id := identify(q.Open(dial(addr)).DeferCleanup(cleanup))
	return id, nil
}

func identify(c *Conn) int { return c.id }

// ---------- ".WithScope(scope) — hand the lifetime to a *q.Scope" ----------
//
//	conn := q.Open(dial(addr)).WithScope(scope)
//
// Auto-detects the cleanup the same way .DeferCleanup() does (chan
// close, Close(), Close() error). The scope owns the resource; the
// caller can return / pass it freely. If the scope is already closed
// at attach time, the cleanup fires eagerly and q.ErrScopeClosed is
// bubbled.

func openWithScopeAuto(scope *q.Scope, addr string) (*Conn, error) {
	conn := q.Open(dial(addr)).WithScope(scope)
	return conn, nil
}

// .WithScope(cleanup, scope) — explicit cleanup + scope.
func openWithScopeExplicit(scope *q.Scope, addr string) (*Conn, error) {
	conn := q.Open(dial(addr)).WithScope(cleanup, scope)
	return conn, nil
}

// q.OpenE shape methods compose with .WithScope as the terminal.
func openWithScopeWrapped(scope *q.Scope, addr string) (*Conn, error) {
	conn := q.OpenE(dial(addr)).Wrap("dialing").WithScope(scope)
	return conn, nil
}

// ---------- "//q:no-escape-check opt-out" ----------
//
//	//q:no-escape-check
//	func channelAutoInner() (chan int, error) {
//	    ch := q.Open(makeChan()).DeferCleanup()
//	    ch <- 7
//	    return ch, nil
//	}

//q:no-escape-check
func channelAutoInner() (chan int, error) {
	ch := q.Open(makeChan()).DeferCleanup()
	ch <- 7
	return ch, nil
}

func main() {
	report := func(label string, err error) {
		closes := closeLog
		closeLog = nil
		if err != nil {
			fmt.Printf("%s: err=%s closes=%v\n", label, err, closes)
			return
		}
		fmt.Printf("%s: ok closes=%v\n", label, closes)
	}

	// What q.Open does
	report("whatQOpenDoes(local)", whatQOpenDoes("local"))
	report("whatQOpenDoes(empty)", whatQOpenDoes(""))

	// Auto-cleanup dispatches
	report("autoChan", autoChan())
	report("autoCloseError", autoCloseError())
	report("autoCloseVoid", autoCloseVoid())

	report("autoCleanupWithWrap", autoCleanupWithWrap())

	// NoDeferCleanup
	v, err := noDeferCleanup("k")
	report("noDeferCleanup(k)="+v, err)
	_, err = noDeferCleanup("")
	report("noDeferCleanup(empty)", err)
	v, err = noDeferCleanupWithWrap("")
	report("noDeferCleanupWithWrap(empty)="+v, err)

	// OpenE chain methods (failing addr)
	report("openEErr(empty)", openEErr(""))
	report("openEErrF(empty)", openEErrF(""))
	report("openEWrap(empty)", openEWrap(""))
	report("openEWrapf(empty)", openEWrapf(""))

	// Example chain — happy path
	report("exampleChain(local)", exampleChain("local"))

	// Recovery chain — refused triggers fallback; non-refused bubbles.
	report("recoveryChain(refused)", recoveryChain("refused"))
	report("recoveryChain(empty)", recoveryChain(""))

	// LIFO across two Opens
	report("work(local, cfg)", work("local", "cfg"))
	report("work(refused, cfg)", work("refused", "cfg")) // dial fails → no opens registered
	report("work(local, empty)", work("local", ""))      // openFile fails → conn registered, file not

	// Statement forms
	report("formDefine(local)", formDefine("local"))
	report("formAssign(local)", formAssign("local"))
	report("formDiscard(local)", formDiscard("local"))
	c, err := formReturnPosition("local")
	report("formReturnPosition(local)="+itoa(c.id), err)
	id, err := formHoist("local")
	report("formHoist(local)="+itoa(id), err)

	// no-escape-check opt-out — ch escapes deliberately.
	ch, _ := channelAutoInner()
	v2 := <-ch
	fmt.Printf("channelAutoInner -> received=%d\n", v2)

	// .WithScope: scope owns the lifetime, resource may escape.
	scope := q.NewScope()
	if c, err := openWithScopeAuto(scope, "remote"); err != nil {
		report("openWithScopeAuto(remote)", err)
	} else {
		fmt.Printf("openWithScopeAuto(remote)=%d (scope owns)\n", c.id)
	}
	scope.Close()
	report("openWithScopeAuto.afterScopeClose", nil)

	scope2 := q.NewScope()
	if c, err := openWithScopeExplicit(scope2, "remote"); err != nil {
		report("openWithScopeExplicit(remote)", err)
	} else {
		fmt.Printf("openWithScopeExplicit(remote)=%d (scope owns)\n", c.id)
	}
	scope2.Close()
	report("openWithScopeExplicit.afterScopeClose", nil)

	scope3 := q.NewScope()
	if _, err := openWithScopeWrapped(scope3, ""); err != nil {
		report("openWithScopeWrapped(empty)", err)
	}
	scope3.Close()

	scope4 := q.NewScope()
	scope4.Close()
	if _, err := openWithScopeAuto(scope4, "remote"); err != nil {
		fmt.Printf("openWithScopeAuto(closed-scope): err=%s is(ErrScopeClosed)=%v\n", err, errors.Is(err, q.ErrScopeClosed))
	}
	report("openWithScopeAuto.eagerClose", nil)
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
