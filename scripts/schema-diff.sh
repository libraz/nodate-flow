#!/usr/bin/env bash
# schema-diff.sh — verify the committed sql/schema.sql matches what
# `bash sql/build-schema.sh` produces from sql/tables/ and sql/views/.
# Fails with a non-zero exit when the regenerated schema differs from
# the committed copy.
#
# Run locally or in CI before merging a schema change:
#
#   bash scripts/schema-diff.sh
#
# Exit codes:
#   0 — committed schema matches regenerated output
#   1 — drift detected (committed schema is stale)
#   2 — regeneration itself failed
#
# On drift, `make db-schema` regenerates sql/schema.sql, which is
# usually what you want to commit.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

SCHEMA_FILE="$ROOT_DIR/sql/schema.sql"
BUILD_SCRIPT="$ROOT_DIR/sql/build-schema.sh"

if [[ ! -f "$SCHEMA_FILE" ]]; then
  echo "ERROR: $SCHEMA_FILE not found. Run 'make db-schema' first." >&2
  exit 2
fi

if [[ ! -f "$BUILD_SCRIPT" ]]; then
  echo "ERROR: $BUILD_SCRIPT not found." >&2
  exit 2
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

# Regenerate into a temp file so we never touch the committed copy.
if ! bash "$BUILD_SCRIPT" >"$TMP_DIR/schema.regen.sql"; then
  echo "ERROR: sql/build-schema.sh failed." >&2
  exit 2
fi

exit_code=0

if ! diff -u "$SCHEMA_FILE" "$TMP_DIR/schema.regen.sql" >"$TMP_DIR/schema.diff"; then
  echo "---- Schema drift: sql/schema.sql ----"
  # Show up to 120 lines so CI logs stay readable.
  head -n 120 "$TMP_DIR/schema.diff"
  if [[ "$(wc -l < "$TMP_DIR/schema.diff")" -gt 120 ]]; then
    echo "... (truncated; run 'make db-schema && git diff sql/schema.sql' to see full diff)"
  fi
  exit_code=1
fi

if [[ $exit_code -eq 0 ]]; then
  echo "sql/schema.sql is in sync with sql/tables/ and sql/views/."
else
  echo ""
  echo "Schema drift detected. Run 'make db-schema' and commit the updated sql/schema.sql."
fi

exit $exit_code
