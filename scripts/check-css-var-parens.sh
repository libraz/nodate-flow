#!/usr/bin/env bash
# check-css-var-parens.sh — detect malformed design-token references of the
# form `var(--nf-...))` that carry an extra, unmatched closing paren.
#
# The regression this guards against is an inline-CSS / CSS-module token
# reference that gained a stray closing paren, e.g.
#
#     color: var(--nf-color-fg));            (CSS module)
#     style={{ color: `var(--nf-color-fg))` }}   (inline TSX)
#
# A bare `var(--nf-token))` is NOT inherently wrong: it is perfectly valid
# when the token is nested inside another function call whose own opening
# paren consumes the second `)`, e.g.
#
#     margin: calc(-1 * var(--nf-space-4));
#     background: color-mix(in srgb, var(--nf-a) 50%, var(--nf-b));
#     backdrop-filter: blur(var(--nf-surface-blur));
#
# In every legitimate case the declaration stays paren-balanced. The stray
# paren is caught by a two-part test that is false-positive-free:
#   1. the line must contain the `var(--nf-token))` token shape, and
#   2. the line must have more `)` than `(` (an unmatched close).
# Only lines satisfying both are reported.
#
# Usage:
#   ./scripts/check-css-var-parens.sh
#
# There is one mode: a hit fails. The scan used to exit 0 unless it was
# handed --ci, which made every caller's correctness depend on
# remembering a flag, and a caller that forgot it reported success on a
# tree full of violations. Arguments are rejected rather than ignored so
# a stale `--ci` is visible instead of merely harmless.
#
# Exit codes:
#   0 — every scan root was read and holds no malformed reference
#   1 — at least one malformed reference
#   2 — the scan could not be performed (bad arguments, or a scan root
#       that no longer holds any file this check can read)

set -euo pipefail

if [[ $# -gt 0 ]]; then
  echo "check-css-var-parens: takes no arguments (got: $*)" >&2
  exit 2
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

SCAN_DIRS=(
  "$ROOT/apps/flow-web/src"
  "$ROOT/apps/accounts-web/src"
)

EXCLUDES=(
  --exclude-dir=node_modules
  --exclude-dir=.git
  --exclude-dir=dist
)

INCLUDES=(
  --include='*.css'
  --include='*.module.css'
  --include='*.tsx'
  --include='*.ts'
)

# Token shape: `var(--nf-<token>))`. The token name cannot contain parens,
# so the trailing `))` is always a var close plus one extra close paren.
PATTERN='var\(--nf-[A-Za-z0-9_-]+\)\)'

# A scan root that has been renamed away reads exactly like a clean one:
# grep is handed nothing, finds nothing, and the guard reports success
# over a tree it never opened. Each root is therefore proved to hold at
# least one readable file before anything is scanned, and proved
# individually — a total would stay satisfied by the roots that survived
# while the renamed one silently stopped being checked.
for dir in "${SCAN_DIRS[@]}"; do
  rel="${dir#"$ROOT"/}"
  if [[ ! -d "$dir" ]]; then
    echo "check-css-var-parens: scan root $rel does not exist, so nothing under it was checked." >&2
    echo "  Point SCAN_DIRS at the directory it moved to." >&2
    exit 2
  fi
  # An empty pattern matches every line, so this lists every file the
  # scan below is able to read under this root. `|| true` is what makes
  # the empty case reportable: grep exits 1 when it matches nothing, and
  # under `set -e` with pipefail that status would end the script here,
  # with no message and nothing to read it from.
  scanned="$(grep -rl -e '' "$dir" "${EXCLUDES[@]}" "${INCLUDES[@]}" 2>/dev/null | wc -l | tr -d ' ' || true)"
  if [[ "$scanned" -eq 0 ]]; then
    echo "check-css-var-parens: scan root $rel holds no .css/.ts/.tsx file, so nothing under it was checked." >&2
    echo "  Either the sources moved or the --include filters no longer name the extensions they are written in." >&2
    exit 2
  fi
done

count=0
found_lines=""

for dir in "${SCAN_DIRS[@]}"; do
  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    count=$((count + 1))
    found_lines="$found_lines\n$line"
  done < <(grep -rnE "$PATTERN" "$dir" "${EXCLUDES[@]}" "${INCLUDES[@]}" \
    2>/dev/null \
    | awk '
        {
          # Strip the "path:lineno:" prefix so only the source text is
          # inspected for paren balance.
          content = $0
          sub(/^[^:]*:[0-9]+:/, "", content)
          opens = gsub(/\(/, "(", content)
          closes = gsub(/\)/, ")", content)
          if (closes > opens) print $0
        }
      ' \
    || true)
done

if [[ $count -gt 0 ]]; then
  echo "Found $count malformed var(--nf-...) reference(s) with a stray closing paren:"
  echo -e "$found_lines" | sort
  echo ""
  echo "Remove the extra ')' so the token reference is balanced:"
  echo "  color: var(--nf-color-fg));  ->  color: var(--nf-color-fg);"
  echo "Nested forms such as calc(var(--nf-space-4)) are valid and are not flagged."
  exit 1
else
  echo "No malformed var(--nf-...)) references found."
fi
