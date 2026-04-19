# Getting Started

## Install

```bash
# Install the preprocessor binary
go install github.com/GiGurra/q/cmd/q@latest

# Add the runtime package to your module
go get github.com/GiGurra/q
```

The preprocessor binary is the `-toolexec` program the Go toolchain invokes for every compile action in your build. It lives outside your module's dependency graph — it's a build-time tool, not a library.

The runtime package `pkg/q` lives inside the `github.com/GiGurra/q` module. `go get` pulls it in so you can import `github.com/GiGurra/q/pkg/q` in your source.

## First passing build

Create a tiny program and build it under the preprocessor:

```go
// main.go
package main

import (
	"fmt"
	"strconv"

	"github.com/GiGurra/q/pkg/q"
)

func parseAndDouble(s string) (int, error) {
	n := q.Try(strconv.Atoi(s))
	return n * 2, nil
}

func main() {
	n, err := parseAndDouble("21")
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println(n) // 42
}
```

Build it:

```bash
GOFLAGS="-toolexec=q" GOCACHE="$HOME/.cache/q-build" go build -o demo ./...
./demo
```

If the q binary isn't on `$PATH`, pass an absolute path:

```bash
GOFLAGS="-toolexec=$(go env GOPATH)/bin/q" go build ./...
```

What just happened: the preprocessor scanned the `q.Try(strconv.Atoi(s))` call site, rewrote it to the inlined `n, _qErr1 := strconv.Atoi(s); if _qErr1 != nil { return *new(int), _qErr1 }` form, and handed the rewritten file to `cmd/compile`. The `q.Try` body never runs — its only purpose at build time is to satisfy the type checker.

## Verify the link gate

To convince yourself the preprocessor is actually doing work, try building **without** it (in a fresh cache):

```bash
GOCACHE=$(mktemp -d) go build -o demo ./...
# fixture: relocation target _q_atCompileTime not defined
```

The link step fails on the missing `_q_atCompileTime` symbol. That's by design — forgetting `-toolexec=q` is a build failure, not a runtime surprise.

## IDE and cache setup

Go's build cache key does not include toolexec state. A `pkg/q.a` cached from a plain `go build` (no stub) and one from a `-toolexec=q` build (with stub) have the same key. Mixing them produces:

- `relocation target _q_atCompileTime not defined` — toolexec build reused a stub-less artifact.

Keep toolexec on a dedicated GOCACHE.

**Terminal:**

```bash
alias gobuild-q='GOFLAGS="-toolexec=q" GOCACHE="$HOME/.cache/q-build" go build'
alias gotest-q='GOFLAGS="-toolexec=q" GOCACHE="$HOME/.cache/q-build"  go test'
```

**GoLand:** Run → Edit Configurations → Templates → Go Test → Environment variables:

```
GOFLAGS=-toolexec=q
GOCACHE=/Users/<you>/.cache/q-build
```

**VS Code (settings.json):**

```json
"go.buildEnvVars": {
    "GOFLAGS": "-toolexec=q",
    "GOCACHE": "${env:HOME}/.cache/q-build"
},
"go.testEnvVars": {
    "GOFLAGS": "-toolexec=q",
    "GOCACHE": "${env:HOME}/.cache/q-build"
}
```

Clean the q cache specifically:

```bash
GOCACHE="$HOME/.cache/q-build" go clean -cache
```

## CI

A reference workflow lives in [`.github/workflows/ci.yml`](https://github.com/GiGurra/q/blob/main/.github/workflows/ci.yml). The relevant pieces:

```yaml
- name: Install q binary
  run: go install ./cmd/q/

- name: Build with -toolexec=q
  run: |
    GOCACHE="$HOME/.cache/q-build" go clean -cache
    GOFLAGS="-toolexec=q" GOCACHE="$HOME/.cache/q-build" go build ./...
```

The `go clean -cache` line is important: GitHub Actions runners reuse the workspace across job steps, so without it a non-toolexec build earlier in the pipeline could pollute the cache the toolexec build inherits.
