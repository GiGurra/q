#!/usr/bin/env bash
# docs_navbar_check.sh — diff the docs navbar's API entry list against
# the actual exported function surface in pkg/q (and subpackages).
#
# Lists every exported func name as `q.<Name>` (top-level) or
# `<subpkg>.<Name>` (e.g. `either.Left`), then extracts the same
# pattern from mkdocs.yml's nav:.API section. Reports the symmetric
# difference: API symbols missing from the navbar (= undocumented
# in the index) and navbar symbols not found in the API (= stale
# entries).
#
# Many-to-one is fine — multiple navbar entries can link to the
# same docs page (e.g. q.Try and q.TryE both point at api/try.md).
# This script only checks the *index*, not the page bodies.
#
# Exit status: 0 when the lists match exactly, 1 when there is any
# missing or stale entry.

set -euo pipefail

cd "$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"

# ---- API surface --------------------------------------------------

# Top-level pkg/q exported funcs. Skip _test files and panicUnrewritten.
api_q=$(grep -h '^func [A-Z]' pkg/q/*.go 2>/dev/null \
    | sed -E 's/[(\[].*//;s/^func //' \
    | sort -u \
    | grep -vE '^(Test|panicUnrewritten)' \
    | sed 's/^/q./')

# Subpackages — for now, just pkg/q/either. Extend when more land.
api_either=$(grep -h '^func [A-Z]' pkg/q/either/*.go 2>/dev/null \
    | sed -E 's/[(\[].*//;s/^func //' \
    | sort -u \
    | sed 's/^/either./')

api=$(printf "%s\n%s\n" "$api_q" "$api_either" | sort -u | grep -v '^$')

# ---- Navbar -------------------------------------------------------

# Extract every `<word>.<Name>` pattern from the API navbar block of
# mkdocs.yml. The API block runs from the `- API:` line through the
# next top-level navbar entry.
navbar=$(awk '
    /^  - API:/ { in_api = 1; next }
    in_api && /^  - [A-Z]/ { in_api = 0 }
    in_api { print }
' mkdocs.yml \
    | grep -oE '(q|either)\.[A-Z][A-Za-z0-9]*' \
    | sort -u)

# ---- Diff ---------------------------------------------------------

missing=$(comm -23 <(echo "$api") <(echo "$navbar"))
stale=$(comm -13 <(echo "$api") <(echo "$navbar"))

status=0
if [[ -n "$missing" ]]; then
    echo "Missing from docs navbar (defined in pkg/q, no entry in mkdocs.yml nav):"
    echo "$missing" | sed 's/^/  /'
    status=1
fi

if [[ -n "$stale" ]]; then
    [[ -n "$missing" ]] && echo
    echo "Stale in docs navbar (mkdocs.yml lists, not in pkg/q):"
    echo "$stale" | sed 's/^/  /'
    status=1
fi

if [[ $status -eq 0 ]]; then
    n=$(echo "$api" | wc -l | tr -d ' ')
    echo "navbar API list matches code ($n entries)"
fi

exit $status
