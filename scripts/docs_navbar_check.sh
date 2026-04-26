#!/usr/bin/env bash
# docs_navbar_check.sh — verify the docs navbar's API section.
#
# Three invariants:
#
#   1. **Sorted (case-insensitive).** Entries inside the API: block
#      must be in case-insensitive alphabetical order by label.
#   2. **No duplicate destinations.** No two entries may point at the
#      same .md file. Each docs page gets exactly one nav entry.
#   3. **Completeness.** Every exported function and type in pkg/q
#      (and pkg/q/either) must be mentioned by name in some docs page
#      (any of docs/api/*.md or docs/typed-nil-guard.md). Names not
#      currently covered surface as "missing".
#
# Exit status: 0 when all invariants hold, 1 otherwise.

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
# and paths; preserve order.
labels=$(echo "$api_block" | sed -nE 's/^    - (.+): [^ ]+$/\1/p')
paths=$(echo "$api_block"  | sed -nE 's/^    - .+: ([^ ]+)$/\1/p')

# ---- Invariant 1: sorted (case-insensitive) ----------------------

sorted_labels=$(echo "$labels" | sort -f)
if ! diff <(echo "$labels") <(echo "$sorted_labels") >/dev/null; then
    echo "Navbar API entries are out of order (case-insensitive sort expected):"
    diff <(echo "$labels") <(echo "$sorted_labels") | sed 's/^/  /'
    status=1
fi

# ---- Invariant 2: no duplicate destinations ----------------------

dupes=$(echo "$paths" | sort | uniq -d)
if [[ -n "$dupes" ]]; then
    [[ $status -ne 0 ]] && echo
    echo "Navbar has multiple entries pointing at the same docs page:"
    while IFS= read -r p; do
        echo "  $p:"
        echo "$api_block" | grep -F ": $p" | sed 's/^/    /'
    done <<< "$dupes"
    status=1
fi

# ---- Invariant 3: completeness ------------------------------------

# Non-test source files for each package.
q_srcs=$(find pkg/q -maxdepth 1 -name '*.go' -not -name '*_test.go')
either_srcs=$(find pkg/q/either -maxdepth 1 -name '*.go' -not -name '*_test.go' 2>/dev/null)

# Exported funcs in pkg/q (top-level).
api_q_funcs=$(grep -h '^func [A-Z]' $q_srcs 2>/dev/null \
    | sed -E 's/[(\[].*//;s/^func //' \
    | sort -u \
    | grep -vE '^panicUnrewritten$' \
    | sed 's/^/q./')

# Exported types in pkg/q.
api_q_types=$(grep -hE '^type [A-Z]' $q_srcs 2>/dev/null \
    | awk '{print $2}' | sed 's/\[.*//' | sort -u | sed 's/^/q./')

# Exported funcs + types in pkg/q/either.
api_either_funcs=$(grep -h '^func [A-Z]' $either_srcs 2>/dev/null \
    | sed -E 's/[(\[].*//;s/^func //' \
    | sort -u \
    | sed 's/^/either./')
api_either_types=$(grep -hE '^type [A-Z]' $either_srcs 2>/dev/null \
    | awk '{print $2}' | sed 's/\[.*//' | sort -u | sed 's/^/either./')

api=$(printf "%s\n%s\n%s\n%s\n" \
    "$api_q_funcs" "$api_q_types" "$api_either_funcs" "$api_either_types" \
    | sort -u | grep -v '^$')

# A symbol counts as "mentioned" if any docs page references it as
# either the qualified form (`q.FlatMap`) OR the bare name in a
# word-bounded context. Code blocks inside pkg/q's own examples
# use the bare form (`func FlatMap[…]`); narrative uses the
# qualified form. Two-char-and-shorter bare names (`A`, `F`,
# `At`, `Ok`) require the qualified form to avoid spurious matches.
docs_files=$(find docs -maxdepth 2 -name '*.md' \
    -not -path 'docs/planning/*' -not -name 'index.md')

missing=""
for sym in $api; do
    bare="${sym#*.}"
    qualified_pat="${sym//./\\.}"
    if grep -hEq "$qualified_pat" $docs_files 2>/dev/null; then
        continue
    fi
    if (( ${#bare} >= 3 )) && grep -hwq "$bare" $docs_files 2>/dev/null; then
        continue
    fi
    missing+="$sym"$'\n'
done

if [[ -n "$missing" ]]; then
    [[ $status -ne 0 ]] && echo
    echo "Exported symbols not mentioned in any docs page:"
    printf "%s" "$missing" | sed 's/^/  /'
    status=1
fi

# ---- Verdict ------------------------------------------------------

if [[ $status -eq 0 ]]; then
    n_entries=$(echo "$labels" | wc -l | tr -d ' ')
    n_symbols=$(echo "$api" | wc -l | tr -d ' ')
    echo "navbar OK — $n_entries entries, $n_symbols exported symbols all mentioned"
fi

exit $status
