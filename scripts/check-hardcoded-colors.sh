#!/usr/bin/env bash
# check-hardcoded-colors.sh — detect hardcoded color values in app source files.
#
# Scans CSS/TSX/TS files for hex colors, rgb(), hsl(), oklch() outside of
# theme definition files. Exits non-zero if any are found.
#
# Usage:
#   ./scripts/check-hardcoded-colors.sh          # scan all apps
#   ./scripts/check-hardcoded-colors.sh --ci      # strict mode (exit 1 on any hit)
#
# Allowed locations (excluded from scan):
#   - packages/ui/src/themes/    (theme definitions must use raw colors)
#   - packages/ui/src/tokens/    (token definitions)
#   - *.test.* / *.spec.*        (test files)
#   - node_modules/

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STRICT=false
[[ "${1:-}" == "--ci" ]] && STRICT=true

# Patterns to detect (extended regex)
# Matches: #abc, #abcdef, #abcdefAA, rgb(), rgba(), hsl(), hsla(), oklch()
COLOR_PATTERN='(#[0-9a-fA-F]{3,8}\b|rgba?\s*\(|hsla?\s*\(|oklch\s*\()'

# Files to scan
SCAN_DIRS=(
  "$ROOT/apps/flow-web/src"
  "$ROOT/apps/time-web/src"
  "$ROOT/apps/accounts-web/src"
  "$ROOT/packages/ui/src/primitives"
)

# Exclusion patterns for grep
EXCLUDES=(
  --exclude-dir=node_modules
  --exclude-dir=.git
  --exclude-dir=dist
  --exclude='*.test.*'
  --exclude='*.spec.*'
)

count=0
found_files=""

for dir in "${SCAN_DIRS[@]}"; do
  [[ -d "$dir" ]] || continue

  # Scan CSS files
  while IFS= read -r line; do
    count=$((count + 1))
    found_files="$found_files\n$line"
  done < <(grep -rnE "$COLOR_PATTERN" "$dir" "${EXCLUDES[@]}" \
    --include='*.css' \
    --exclude='*.module.css.d.ts' \
    2>/dev/null \
    | grep -v 'themes/' \
    | grep -v 'tokens/' \
    | grep -v '\/\*.*\*\/' \
    | grep -v 'var(--' \
    || true)

  # Scan TSX/TS for inline style hardcoded colors
  while IFS= read -r line; do
    # Filter out token references and comments
    if echo "$line" | grep -qE 'var\(--nf-'; then
      continue
    fi
    count=$((count + 1))
    found_files="$found_files\n$line"
  done < <(grep -rnE "(color|background|border-color|fill|stroke).*$COLOR_PATTERN" "$dir" "${EXCLUDES[@]}" \
    --include='*.tsx' \
    --include='*.ts' \
    2>/dev/null \
    | grep -v '\.d\.ts' \
    | grep -v '\/\/' \
    || true)
done

if [[ $count -gt 0 ]]; then
  echo "⚠ Found $count hardcoded color value(s):"
  echo -e "$found_files" | sort
  echo ""
  echo "Use design tokens instead: var(--nf-color-*), var(--nf-cal-*)"
  echo "Theme definitions (packages/ui/src/themes/) are excluded."
  if $STRICT; then
    exit 1
  fi
else
  echo "✓ No hardcoded colors found outside theme definitions."
fi
