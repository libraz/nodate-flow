#!/usr/bin/env bash
# openapi-diff.sh — verify the committed OpenAPI spec matches what the
# Go service currently serves. Fails with a non-zero exit when the
# regenerated spec differs from the committed copy.
#
# Run locally or in CI before merging an API change:
#
#   bash scripts/openapi-diff.sh
#
# Exit codes:
#   0 — committed spec matches regenerated output
#   1 — drift detected (committed spec is stale or tool produced diff)
#   2 — regeneration itself failed
#
# On drift, `make gen-sdk` regenerates the spec AND the TS SDK, which
# is usually what you want to commit.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

FLOW_SPEC="$ROOT_DIR/packages/sdk/openapi.json"

if [[ ! -f "$FLOW_SPEC" ]]; then
  echo "ERROR: $FLOW_SPEC not found. Run 'make gen-openapi' first." >&2
  exit 2
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

cp "$FLOW_SPEC" "$TMP_DIR/flow-committed.json"

# Regenerate in place; the committed file will be overwritten.
if ! make -C "$ROOT_DIR" gen-openapi >/dev/null; then
  # Restore the pre-run copy so a failed regen doesn't leave a dirty tree.
  cp "$TMP_DIR/flow-committed.json" "$FLOW_SPEC"
  echo "ERROR: flow/auth OpenAPI regeneration failed." >&2
  exit 2
fi

exit_code=0

if ! diff -u "$TMP_DIR/flow-committed.json" "$FLOW_SPEC" >"$TMP_DIR/flow.diff"; then
  echo "---- OpenAPI drift: packages/sdk/openapi.json ----"
  # Show up to 120 lines so CI logs stay readable.
  head -n 120 "$TMP_DIR/flow.diff"
  if [[ "$(wc -l < "$TMP_DIR/flow.diff")" -gt 120 ]]; then
    echo "... (truncated; run 'git diff packages/sdk/openapi.json' to see full diff)"
  fi
  exit_code=1
fi

if [[ $exit_code -eq 0 ]]; then
  echo "OpenAPI spec is in sync."
else
  echo ""
  echo "OpenAPI drift detected. Run 'make gen-sdk' and commit the updated spec + SDK."
fi

exit $exit_code
