#!/usr/bin/env bash
# check-token-fallbacks.sh — forbid var(--nf-*, fallback) patterns in app CSS.
#
# Per docs/conventions/design-tokens.md every --nf-* token must be defined
# by the active theme. A `var(--nf-foo, <fallback>)` pattern hides theme-
# loading bugs (the fallback silently masks a missing token) and bypasses
# the design-system contract. App CSS must reference tokens directly:
# `var(--nf-foo)`.
#
# Theme files in packages/ui/src/themes/ are intentionally excluded — those
# files DEFINE the tokens, so fallback chains there are part of the token
# contract, not a contract violation.
#
# Usage:
#   ./scripts/check-token-fallbacks.sh          # scan, exit 0
#   ./scripts/check-token-fallbacks.sh --ci     # strict mode (exit 1 on any hit)

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

# Detects `var(--nf-...,` — the comma marks a fallback. `var(--nf-...)`
# without a comma is the desired form and is not matched.
PATTERN='var\(--nf-[A-Za-z0-9_-]+,'

count=0
found_lines=""

for dir in "${SCAN_DIRS[@]}"; do
  [[ -d "$dir" ]] || continue

  while IFS= read -r line; do
    count=$((count + 1))
    found_lines="$found_lines\n$line"
  done < <(grep -rnE "$PATTERN" "$dir" "${EXCLUDES[@]}" \
    --include='*.css' \
    --include='*.module.css' \
    2>/dev/null \
    || true)
done

if [[ $count -gt 0 ]]; then
  echo "Found $count token fallback(s) in app CSS:"
  echo -e "$found_lines" | sort
  echo ""
  echo "Drop the fallback so the active theme's token wins:"
  echo "  var(--nf-color-foo, somecolor)  ->  var(--nf-color-foo)"
  echo "Theme definition files under packages/ui/src/themes/ are exempt."
  echo "See docs/conventions/design-tokens.md."
  if $STRICT; then
    exit 1
  fi
else
  echo "No --nf-* token fallbacks found in app CSS."
fi
