#!/usr/bin/env bash
# openapi-diff.sh — verify the committed OpenAPI spec and the generated
# SDK types match what the Go services currently serve. Fails with a
# non-zero exit when either regenerated artifact differs from its
# committed copy.
#
# Both halves matter: the spec catches a handler change that was never
# dumped, and the types catch a spec change that was dumped but never
# run through openapi-typescript. A stale src/openapi.ts is the quieter
# of the two — every consumer keeps type-checking against a shape the
# API no longer serves.
#
# Run locally or in CI before merging an API change:
#
#   bash scripts/openapi-diff.sh
#
# Exit codes:
#   0 — committed artifacts match regenerated output
#   1 — drift detected (a committed artifact is stale)
#   2 — regeneration itself failed
#
# On drift, `make gen-sdk` regenerates the spec AND the TS SDK, which
# is usually what you want to commit.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

SPEC="$ROOT_DIR/packages/sdk/openapi.json"
TYPES="$ROOT_DIR/packages/sdk/src/openapi.ts"

for artifact in "$SPEC" "$TYPES"; do
  if [[ ! -f "$artifact" ]]; then
    echo "ERROR: $artifact not found. Run 'make gen-sdk' first." >&2
    exit 2
  fi
done

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

cp "$SPEC" "$TMP_DIR/committed-openapi.json"
cp "$TYPES" "$TMP_DIR/committed-openapi.ts"

# Regenerate in place; the committed files will be overwritten.
if ! make -C "$ROOT_DIR" gen-sdk >/dev/null; then
  # Restore the pre-run copies so a failed regen doesn't leave a dirty tree.
  cp "$TMP_DIR/committed-openapi.json" "$SPEC"
  cp "$TMP_DIR/committed-openapi.ts" "$TYPES"
  echo "ERROR: OpenAPI / SDK regeneration failed." >&2
  exit 2
fi

exit_code=0

# report_drift <label> <committed copy> <regenerated path> <diff file>
report_drift() {
  local label="$1" committed="$2" current="$3" diff_file="$4"
  if diff -u "$committed" "$current" >"$diff_file"; then
    return 0
  fi
  echo "---- drift: $label ----"
  # Show up to 120 lines so CI logs stay readable.
  head -n 120 "$diff_file"
  if [[ "$(wc -l <"$diff_file")" -gt 120 ]]; then
    echo "... (truncated; run 'git diff $label' to see the full diff)"
  fi
  return 1
}

if ! report_drift "packages/sdk/openapi.json" \
  "$TMP_DIR/committed-openapi.json" "$SPEC" "$TMP_DIR/spec.diff"; then
  exit_code=1
fi

if ! report_drift "packages/sdk/src/openapi.ts" \
  "$TMP_DIR/committed-openapi.ts" "$TYPES" "$TMP_DIR/types.diff"; then
  exit_code=1
fi

if [[ $exit_code -eq 0 ]]; then
  echo "OpenAPI spec and SDK types are in sync."
else
  echo ""
  echo "Drift detected. Run 'make gen-sdk' and commit the updated spec + SDK types."
fi

exit $exit_code
