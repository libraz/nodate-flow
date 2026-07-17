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
#   ./scripts/check-css-var-parens.sh          # scan, exit 0 (report only)
#   ./scripts/check-css-var-parens.sh --ci     # strict mode (exit 1 on any hit)

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STRICT=false
[[ "${1:-}" == "--ci" ]] && STRICT=true

SCAN_DIRS=(
  "$ROOT/apps/flow-web/src"
  "$ROOT/apps/accounts-web/src"
)

EXCLUDES=(
  --exclude-dir=node_modules
  --exclude-dir=.git
  --exclude-dir=dist
)

# Token shape: `var(--nf-<token>))`. The token name cannot contain parens,
# so the trailing `))` is always a var close plus one extra close paren.
PATTERN='var\(--nf-[A-Za-z0-9_-]+\)\)'

count=0
found_lines=""

for dir in "${SCAN_DIRS[@]}"; do
  [[ -d "$dir" ]] || continue

  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    count=$((count + 1))
    found_lines="$found_lines\n$line"
  done < <(grep -rnE "$PATTERN" "$dir" "${EXCLUDES[@]}" \
    --include='*.css' \
    --include='*.module.css' \
    --include='*.tsx' \
    --include='*.ts' \
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
  if $STRICT; then
    exit 1
  fi
else
  echo "No malformed var(--nf-...)) references found."
fi
