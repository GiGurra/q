#!/usr/bin/env bash
# docs_navbar_check.sh — verify the docs navbar's API section.
#
# Two invariants:
#
#   1. **Sorted (case-insensitive).** Entries inside the API: block
#      must be in case-insensitive alphabetical order by label.
#   2. **Function completeness.** Every exported function in pkg/q
#      (and pkg/q/either) must be listed in the navbar. The only
#      collapsing allowed is `Foo` + `FooE` pairs into a single
#      entry labelled `q.Foo / q.FooE`. Anything else gets its own
#      entry.
#
# Multiple navbar entries pointing at the same .md page are fine;
# the docs pages themselves don't have to be split per-function.
# Exported types are not required in the navbar (they're returned
# by chain-style fluent calls; users rarely name them directly).
#
# Exit status: 0 when both invariants hold, 1 otherwise.

set -euo pipefail

cd "$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"

status=0

# ---- Extract the API navbar block --------------------------------

# Pull the lines between `- API:` and the next top-level navbar entry.
api_block=$(awk '
    /^  - API:/ { in_api = 1; next }
    in_api && /^  - [A-Z]/ { in_api = 0 }
    in_api { print }
' mkdocs.yml)

# Each navbar entry looks like `    - <label>: <path>`. Pull labels
# in source order.
labels=$(echo "$api_block" | sed -nE 's/^    - (.+): [^ ]+$/\1/p')

# ---- Invariant 1: sorted (case-insensitive) ----------------------

sorted_labels=$(echo "$labels" | sort -f)
if ! diff <(echo "$labels") <(echo "$sorted_labels") >/dev/null; then
    echo "Navbar API entries are out of order (case-insensitive sort expected):"
    diff <(echo "$labels") <(echo "$sorted_labels") | sed 's/^/  /'
    status=1
fi

# ---- Invariant 2: function completeness --------------------------

# Non-test source files for each package.
q_srcs=$(find pkg/q -maxdepth 1 -name '*.go' -not -name '*_test.go')
either_srcs=$(find pkg/q/either -maxdepth 1 -name '*.go' -not -name '*_test.go' 2>/dev/null)

# Exported funcs in pkg/q, prefixed `q.`.
api_q_funcs=$(grep -h '^func [A-Z]' $q_srcs 2>/dev/null \
    | sed -E 's/[(\[].*//;s/^func //' \
    | sort -u \
    | grep -vE '^panicUnrewritten$' \
    | sed 's/^/q./')

# Exported funcs in pkg/q/either, prefixed `q.either.` so they sort
# naturally under the q.E* range rather than ahead of all q.* entries.
api_either_funcs=$(grep -h '^func [A-Z]' $either_srcs 2>/dev/null \
    | sed -E 's/[(\[].*//;s/^func //' \
    | sort -u \
    | sed 's/^/q.either./')

api_funcs=$(printf "%s\n%s\n" "$api_q_funcs" "$api_either_funcs" \
    | sort -u | grep -v '^$')

# Compute expected entry labels: collapse `Foo` + `FooE` pairs into
# one `Foo / FooE` entry; emit everything else standalone. The
# collapse only triggers when both halves exist as exported funcs
# in the same package.
expected=$(awk '
    { funcs[$0] = 1 }
    END {
        for (f in funcs) {
            if (f ~ /E$/) {
                bare = substr(f, 1, length(f) - 1)
                if (bare in funcs) {
                    label[bare] = bare " / " f
                    skip[f] = 1
                }
            }
        }
        for (f in funcs) {
            if (skip[f]) continue
            if (!(f in label)) label[f] = f
        }
        for (k in label) print label[k]
    }
' <<< "$api_funcs" | sort -f)

actual=$(echo "$labels" | sort -f)

# Compare.
missing=$(comm -23 <(echo "$expected") <(echo "$actual"))
stale=$(comm -13 <(echo "$expected") <(echo "$actual"))

if [[ -n "$missing" ]]; then
    [[ $status -ne 0 ]] && echo
    echo "Missing from navbar (exported funcs without an entry):"
    echo "$missing" | sed 's/^/  /'
    status=1
fi

if [[ -n "$stale" ]]; then
    [[ -n "$missing" ]] && echo
    echo "Stale in navbar (entries that don't match any exported func or Foo/FooE pair):"
    echo "$stale" | sed 's/^/  /'
    status=1
fi

# ---- Verdict ------------------------------------------------------

if [[ $status -eq 0 ]]; then
    n_entries=$(echo "$labels" | wc -l | tr -d ' ')
    n_funcs=$(echo "$api_funcs" | wc -l | tr -d ' ')
    echo "navbar OK — $n_entries entries cover $n_funcs exported funcs"
fi

exit $status
