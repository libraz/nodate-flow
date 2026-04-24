#!/usr/bin/env bash
# check-token-usage.sh — detect CSS variable references outside the --nf-* namespace.
#
# Scans CSS/TSX/TS files for var(--*) that don't match var(--nf-*).
# Theme/token definition files are excluded.
#
# Usage:
#   ./scripts/check-token-usage.sh          # scan all apps
#   ./scripts/check-token-usage.sh --ci     # strict mode (exit 1 on any hit)
#
# Allowed custom property namespaces:
#   --nf-*       shared design tokens
#   --nf-cal-*   calendar-specific tokens
#   --font-*     font-family custom properties (body, display, mono)
#   --col-*      CSS vars set via JS (board column count, data-grid column sizes)
#
# Allowed locations (excluded from scan):
#   - packages/ui/src/themes/    (theme definitions)
#   - packages/ui/src/tokens/    (token definitions)
#   - *.test.* / *.spec.*
#   - node_modules/

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STRICT=false
[[ "${1:-}" == "--ci" ]] && STRICT=true

# Files to scan
SCAN_DIRS=(
  "$ROOT/apps/flow-web/src"
  "$ROOT/apps/accounts-web/src"
  "$ROOT/packages/ui/src/primitives"
  "$ROOT/packages/ui/src/providers"
)

EXCLUDES=(
  --exclude-dir=node_modules
  --exclude-dir=.git
  --exclude-dir=dist
  --exclude='*.test.*'
  --exclude='*.spec.*'
  --exclude='*.d.ts'
)

count=0
found_files=""

for dir in "${SCAN_DIRS[@]}"; do
  [[ -d "$dir" ]] || continue

  while IFS= read -r line; do
    # Allow --nf-* (includes --nf-cal-*), --font-* (font families), --col-* (JS-driven)
    # Extract all var(--*) refs and check if any are outside allowed namespaces
    all_vars=$(echo "$line" | grep -oE 'var\(--[a-zA-Z0-9_-]+' || true)
    if [[ -z "$all_vars" ]]; then
      continue
    fi
    other=$(echo "$all_vars" | grep -vE 'var\(--nf-|var\(--font-|var\(--col-' || true)
    if [[ -z "$other" ]]; then
      continue
    fi
    count=$((count + 1))
    found_files="$found_files\n$line"
  done < <(grep -rnE 'var\(--[a-zA-Z]' "$dir" "${EXCLUDES[@]}" \
    --include='*.css' \
    --include='*.tsx' \
    --include='*.ts' \
    2>/dev/null \
    | grep -v 'themes/' \
    | grep -v 'tokens/' \
    | grep -vE 'var\(--nf-|var\(--font-|var\(--col-' \
    || true)
done

if [[ $count -gt 0 ]]; then
  echo "⚠ Found $count non-standard CSS variable reference(s):"
  echo -e "$found_files" | sort
  echo ""
  echo "Use --nf-* tokens instead. See docs/conventions/design-tokens.md"
  if $STRICT; then
    exit 1
  fi
else
  echo "✓ All CSS variable references use the --nf-* namespace."
fi
