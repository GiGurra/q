package preprocessor

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Chain captures the optional `--and-then` chain of successor
// preprocessor invocations. A non-empty NextCmd means q was invoked
// as one link in a chain like:
//
//	q [q-args...] --and-then pre2 [pre2-args...] --and-then pre3 ... /abs/go/tool args...
//
// When the chain fires, q still does its normal rewriting, but
// instead of exec'ing the real Go tool directly it exec's NextCmd
// with the tool path (and rewritten tool args) appended. Each link
// in the chain peels off one --and-then and forwards the remainder,
// so the last link sees a classic toolexec invocation and needs no
// --and-then awareness at all.
type Chain struct {
	// NextCmd is the argv that should replace the bare tool
	// invocation. Empty when no --and-then was present.
	NextCmd []string
}

// parseChain splits the q toolexec args (i.e. os.Args[1:]) into its
// components:
//
//	qArgs     — flags intended for q itself (reserved for future use; empty for now)
//	chain     — the successor command, if --and-then was used
//	toolPath  — the absolute path of the Go tool q wraps
//	toolArgs  — the tool's own arguments
//
// Without --and-then the parse is the classic toolexec shape:
// args[0] is the tool, args[1:] its arguments. With --and-then, q
// consumes everything up to the first --and-then as its own args,
// then scans forward to locate the Go tool (by its absolute path
// living under $GOROOT/pkg/tool/$GOOS_$GOARCH/). Everything between
// the --and-then and the Go tool is the successor argv.
//
// ok is false when args look like a broken chain invocation — e.g.
// a --and-then with no identifiable Go tool after it.
func parseChain(args []string) (qArgs []string, chain Chain, toolPath string, toolArgs []string, ok bool) {
	split := -1
	for i, a := range args {
		if a == "--and-then" {
			split = i
			break
		}
	}
	if split == -1 {
		if len(args) == 0 {
			return nil, Chain{}, "", nil, false
		}
		return nil, Chain{}, args[0], args[1:], true
	}
	qArgs = args[:split]
	rest := args[split+1:]
	toolIdx := findGoToolIndex(rest)
	if toolIdx < 0 {
		return qArgs, Chain{}, "", nil, false
	}
	chain = Chain{NextCmd: rest[:toolIdx]}
	toolPath = rest[toolIdx]
	toolArgs = rest[toolIdx+1:]
	return qArgs, chain, toolPath, toolArgs, true
}

// goToolBases is the set of basename identifiers Go's toolchain uses
// for the tools invoked via -toolexec. Locating the Go tool in a
// chain argv requires BOTH:
//
//  1. the basename (minus any .exe suffix) to be in this set, and
//  2. the directory to equal $GOROOT/pkg/tool/$GOOS_$GOARCH/
//
// The directory check is the critical one: without it, a
// preprocessor flag value like `--output /tmp/compile` would be
// misclassified as the `compile` tool, silently truncating the
// chain and feeding garbage to the compiler.
var goToolBases = map[string]bool{
	"compile":   true,
	"link":      true,
	"asm":       true,
	"cgo":       true,
	"vet":       true,
	"nm":        true,
	"objdump":   true,
	"pack":      true,
	"buildid":   true,
	"addr2line": true,
	"covdata":   true,
	"test2json": true,
	"trace":     true,
	"fix":       true,
}

// goToolDir returns $GOROOT/pkg/tool/$GOOS_$GOARCH, the canonical
// directory Go's build system places its tool binaries under. The
// answer is invariant for the life of the process, so we cache it.
var goToolDir = sync.OnceValue(func() string {
	return filepath.Join(runtime.GOROOT(), "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH)
})

func findGoToolIndex(args []string) int {
	dir := goToolDir()
	for i, a := range args {
		if !filepath.IsAbs(a) {
			continue
		}
		if filepath.Dir(a) != dir {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(a), ".exe")
		if goToolBases[base] {
			return i
		}
	}
	return -1
}
