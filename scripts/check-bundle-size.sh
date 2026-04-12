#!/usr/bin/env bash
#
# Bundle size performance budget for apps/web.
#
# Budgets:
#   - Single JS chunk: max 600 KB (614400 bytes)
#   - Total JS bundle:  max 2 MB  (2097152 bytes)
#
# Usage:
#   scripts/check-bundle-size.sh          # build then check
#   scripts/check-bundle-size.sh --skip-build  # check existing dist/

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WEB_DIR="$REPO_ROOT/apps/web"
DIST_ASSETS="$WEB_DIR/dist/assets"

MAX_CHUNK_BYTES=614400    # 600 KB
MAX_TOTAL_BYTES=2097152   # 2 MB

# ---------------------------------------------------------------------------
# Build (unless --skip-build)
# ---------------------------------------------------------------------------
if [[ "${1:-}" != "--skip-build" ]]; then
  echo "==> Building apps/web ..."
  (cd "$WEB_DIR" && npx vite build --mode production)
  echo ""
fi

# ---------------------------------------------------------------------------
# Verify dist/assets exists
# ---------------------------------------------------------------------------
if [[ ! -d "$DIST_ASSETS" ]]; then
  echo "FAIL: $DIST_ASSETS does not exist. Did the build succeed?"
  exit 1
fi

# ---------------------------------------------------------------------------
# Collect JS files
# ---------------------------------------------------------------------------
js_files=()
while IFS= read -r -d '' f; do
  js_files+=("$f")
done < <(find "$DIST_ASSETS" -maxdepth 1 -name '*.js' -print0)

if [[ ${#js_files[@]} -eq 0 ]]; then
  echo "FAIL: No JS files found in $DIST_ASSETS"
  exit 1
fi

# ---------------------------------------------------------------------------
# Check each chunk + accumulate total
# ---------------------------------------------------------------------------
total_bytes=0
failures=0

for f in "${js_files[@]}"; do
  # stat works differently on macOS vs Linux
  if stat --version >/dev/null 2>&1; then
    size=$(stat -c%s "$f")        # GNU (Linux)
  else
    size=$(stat -f%z "$f")        # BSD (macOS)
  fi
  total_bytes=$((total_bytes + size))
  name=$(basename "$f")
  size_kb=$((size / 1024))

  if [[ $size -gt $MAX_CHUNK_BYTES ]]; then
    echo "FAIL: $name is ${size_kb} KB (${size} bytes) — exceeds ${MAX_CHUNK_BYTES} byte limit"
    failures=$((failures + 1))
  else
    echo "  OK: $name — ${size_kb} KB"
  fi
done

echo ""

# ---------------------------------------------------------------------------
# Check total
# ---------------------------------------------------------------------------
total_kb=$((total_bytes / 1024))
max_total_kb=$((MAX_TOTAL_BYTES / 1024))

if [[ $total_bytes -gt $MAX_TOTAL_BYTES ]]; then
  echo "FAIL: Total JS bundle is ${total_kb} KB (${total_bytes} bytes) — exceeds ${max_total_kb} KB limit"
  failures=$((failures + 1))
else
  echo "  OK: Total JS bundle — ${total_kb} KB / ${max_total_kb} KB budget"
fi

echo ""

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
if [[ $failures -gt 0 ]]; then
  echo "Bundle size check FAILED ($failures violation(s))"
  exit 1
fi

echo "Bundle size check PASSED"
exit 0
