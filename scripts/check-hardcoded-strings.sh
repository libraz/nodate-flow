#!/usr/bin/env bash
# check-hardcoded-strings.sh — detect hardcoded user-facing strings in JSX
# attributes that should route through i18n.
#
# Scans TSX files for `placeholder=`, `aria-label=`, `aria-description=`,
# `title=`, and `alt=` attributes whose value is a literal English-looking
# string instead of a `t('key')` call.
#
# Usage:
#   ./scripts/check-hardcoded-strings.sh
#
# There is one mode: a hit fails. Arguments are rejected rather than
# ignored, so a caller still passing the old --ci flag is visible.
#
# Rationale:
#   No hardcoded UI strings: everything routes through `t('key')`. All UI
#   strings live in `apps/<app>/locales/{en,ja,zh}/` so translators have a
#   single source of truth and the app stays locale-flippable without code
#   changes. An attribute is the easiest place to forget, because it reads
#   as markup rather than as copy.
#
# Excluded:
#   - test / spec / fixture files (literal copy is sometimes required for
#     equality assertions)
#   - node_modules, dist, .git
#   - generated SDK output

set -euo pipefail

if [[ $# -gt 0 ]]; then
  echo "check-hardcoded-strings: takes no arguments (got: $*)" >&2
  exit 2
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Detects: attr="Word ...", attr='Word ...' where the value contains at
# least one alphabetic run of length >= 4. Bound the inner pattern so we
# don't match expression-bound attrs like attr={t('x')}.
ATTR_PATTERN='(placeholder|aria-label|aria-description|title|alt)="[^"{}<>][^"{}<>]*[A-Za-z]{4,}[^"{}<>]*"'

SCAN_DIRS=(
  "$ROOT/apps/flow-web/src"
  "$ROOT/apps/accounts-web/src"
)

EXCLUDES=(
  --exclude-dir=node_modules
  --exclude-dir=.git
  --exclude-dir=dist
  --exclude-dir=__tests__
  --exclude='*.test.*'
  --exclude='*.spec.*'
  --exclude='*.d.ts'
)

count=0
found_files=""

for dir in "${SCAN_DIRS[@]}"; do
  [[ -d "$dir" ]] || continue

  while IFS= read -r line; do
    count=$((count + 1))
    found_files="$found_files\n$line"
  done < <(grep -rnE "$ATTR_PATTERN" "$dir" "${EXCLUDES[@]}" \
    --include='*.tsx' \
    2>/dev/null \
    || true)
done

if [[ $count -gt 0 ]]; then
  echo "Found $count hardcoded UI string(s) in JSX attributes:"
  echo -e "$found_files" | sort
  echo ""
  echo "Move the literal to apps/<app>/locales/{en,ja,zh}/<namespace>.json"
  echo "and use t('key') instead. Every UI string routes through i18n."
  exit 1
else
  echo "No hardcoded UI strings found in scanned JSX attributes."
fi
