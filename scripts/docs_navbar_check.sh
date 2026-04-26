#!/usr/bin/env bash
# docs_navbar_check.sh — verify the docs navbar's API section.
#
# Invariants:
#
#   1. **Sorted (case-insensitive).** Entries inside the API: block
#      must be in case-insensitive alphabetical order by label.
#   2. **One-to-one with docs/api/.** Every docs/api/*.md file has
#      exactly one navbar entry, and every navbar entry points at an
#      existing docs/api/*.md file. Missing entries (a new docs page
#      without nav coverage) and stale entries (a nav entry pointing
#      at a removed page) both fail.
#
# Labels are short (1-3 words) descriptions of what the page covers
# — see the navbar comment in mkdocs.yml. The script only checks
# label-to-page mapping, not label content; renaming a page's nav
# label is a free edit.
#
# Exit status: 0 when both invariants hold, 1 otherwise.

set -euo pipefail

cd "$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"

status=0

# ---- Extract the API navbar block --------------------------------

api_block=$(awk '
    /^  - API:/ { in_api = 1; next }
    in_api && /^  - [A-Z]/ { in_api = 0 }
    in_api { print }
' mkdocs.yml)

# Each navbar entry looks like `    - <label>: <path>`.
labels=$(echo "$api_block" | sed -nE 's/^    - (.+): [^ ]+$/\1/p')
paths=$(echo "$api_block"  | sed -nE 's/^    - .+: ([^ ]+)$/\1/p')

# ---- Invariant 1: sorted (case-insensitive) ----------------------

sorted_labels=$(echo "$labels" | sort -f)
if ! diff <(echo "$labels") <(echo "$sorted_labels") >/dev/null; then
    echo "Navbar API entries are out of order (case-insensitive sort expected):"
    diff <(echo "$labels") <(echo "$sorted_labels") | sed 's/^/  /'
    status=1
fi

# ---- Invariant 2: one-to-one with docs/api/ ----------------------

# Filesystem set: paths to api docs as they would appear in mkdocs.yml.
fs_pages=$(find docs/api -maxdepth 1 -type f -name '*.md' \
    | sed 's|^docs/||' | sort)

# Navbar set: stripped any "#fragment" (none today, defensive).
nav_pages=$(echo "$paths" | sed 's/#.*//' | sort)

# Duplicates: a page referenced more than once.
dupes=$(echo "$nav_pages" | uniq -d)
if [[ -n "$dupes" ]]; then
    [[ $status -ne 0 ]] && echo
    echo "Navbar entries map to the same docs page more than once:"
    while IFS= read -r p; do
        echo "  $p:"
        echo "$api_block" | grep -F "$p" | sed 's/^/    /'
    done <<< "$dupes"
    status=1
fi

missing=$(comm -23 <(echo "$fs_pages") <(echo "$nav_pages" | sort -u))
stale=$(comm -13 <(echo "$fs_pages") <(echo "$nav_pages" | sort -u))

if [[ -n "$missing" ]]; then
    [[ $status -ne 0 ]] && echo
    echo "Missing from navbar (docs/api/ pages without an entry):"
    echo "$missing" | sed 's/^/  /'
    status=1
fi

if [[ -n "$stale" ]]; then
    [[ $status -ne 0 ]] && echo
    echo "Stale in navbar (entries pointing at non-existent docs pages):"
    echo "$stale" | sed 's/^/  /'
    status=1
fi

# ---- Verdict ------------------------------------------------------

if [[ $status -eq 0 ]]; then
    n_entries=$(echo "$labels" | wc -l | tr -d ' ')
    echo "navbar OK — $n_entries entries, one per docs/api/ page"
fi

exit $status
