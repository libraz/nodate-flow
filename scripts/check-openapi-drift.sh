#!/usr/bin/env bash
# check-openapi-drift.sh — verify the committed API document, and the SDK
# types generated from it, still describe what the Go services declare.
#
# packages/sdk/openapi.json is generated from the handler declarations and
# edited by nobody. When a declaration changes and the dump is not re-run,
# the services keep serving the new shape while the document keeps
# describing the old one: everything builds, every test passes, and the
# gap only surfaces in whatever reads the document instead of the API.
#
# One of those readers is a gate. scripts/check-web-bounds.ts takes the
# document as its only account of what the API accepts, so a stale
# document leaves that guard comparing the web forms against limits the
# API no longer has — reporting green over exactly the drift it exists to
# catch.
#
# packages/sdk/src/openapi.ts is generated from the document in turn, and
# goes stale the same way one step further along: every SDK consumer
# type-checks against a shape nothing serves.
#
# The check never writes into the working tree: the generators are pointed
# at a scratch directory and the results are compared against what is
# committed. A failure therefore tells you to run the generator; it does
# not half-run it for you.
#
# The formatting step is part of the artefact. The committed document is
# biome-formatted, so the scratch document is put through the same
# formatter before the comparison; comparing against a raw dump reports
# drift on every run.
#
# Reaching the generated files still does not prove the comparison can see
# drift: a comparison that opened nothing reports no drift for the same
# reason an up-to-date tree does. The self-verification below runs both
# comparisons over sample files whose drift is known, before the real
# ones, and a failure among those stops the run.
#
# Usage:
#   bash scripts/check-openapi-drift.sh
#
# Exit codes:
#   0 — the document and the SDK types agree with their sources
#   1 — drift detected
#   2 — the check could not be performed

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

DOCUMENT="$ROOT_DIR/packages/sdk/openapi.json"
TYPES="$ROOT_DIR/packages/sdk/src/openapi.ts"

for arg in "$@"; do
  case "$arg" in
    -h | --help)
      sed -n '2,43p' "${BASH_SOURCE[0]}"
      exit 0
      ;;
    *)
      echo "unknown argument: $arg" >&2
      exit 2
      ;;
  esac
done

# The two comparisons, in one program so the self-verification exercises
# the same code the real run uses.
#
# Reads: document|types <committed> <regenerated>
# Exits: 0 in agreement, 1 on drift, 2 when nothing meaningful was compared.
COMPARE_PY='
import difflib, json, os, sys

mode, committed_path, regenerated_path = sys.argv[1], sys.argv[2], sys.argv[3]

LIMIT = 20


def unusable(what, detail):
    print(f"{what} could not be compared: {detail}")
    print("")
    print("An empty or unreadable side reports no drift for the same reason an")
    print("up-to-date one does, so this is not a pass.")
    sys.exit(2)


def read(path, what):
    if not os.path.exists(path):
        unusable(what, f"{path} does not exist")
    text = open(path, encoding="utf-8").read()
    if not text.strip():
        unusable(what, f"{path} is empty")
    return text


def listing(title, items):
    if not items:
        return
    print(f"  {title}:")
    for item in items[:LIMIT]:
        print(f"    {item}")
    if len(items) > LIMIT:
        print(f"    ... and {len(items) - LIMIT} more")


def buckets(old, new):
    """Names added, removed and changed between two name-keyed mappings."""
    old_keys, new_keys = set(old), set(new)
    added = sorted(new_keys - old_keys)
    removed = sorted(old_keys - new_keys)
    changed = sorted(k for k in old_keys & new_keys if old[k] != new[k])
    return added, removed, changed


if mode == "document":
    old_text = read(committed_path, "The committed API document")
    new_text = read(regenerated_path, "The regenerated API document")
    try:
        old = json.loads(old_text)
        new = json.loads(new_text)
    except ValueError as exc:
        unusable("The API document", f"it is not valid JSON ({exc})")

    # A document with no paths is what a generator that produced nothing
    # looks like, and it would compare clean against a second one.
    if not new.get("paths"):
        unusable("The regenerated API document", "it declares no paths at all")
    if not old.get("paths"):
        unusable("The committed API document", "it declares no paths at all")

    if old == new:
        sys.exit(0)

    print("The committed API document no longer matches what the services declare.")
    print("")

    added, removed, changed = buckets(old.get("paths", {}), new.get("paths", {}))
    listing("paths the services declare that the document does not have", added)
    listing("paths the document has that nothing declares any more", removed)
    listing("paths whose declaration changed", changed)

    old_schemas = old.get("components", {}).get("schemas", {})
    new_schemas = new.get("components", {}).get("schemas", {})
    s_added, s_removed, s_changed = buckets(old_schemas, new_schemas)
    listing("schemas the services declare that the document does not have", s_added)
    listing("schemas the document has that nothing declares any more", s_removed)
    listing("schemas whose shape changed", s_changed)

    if not any([added, removed, changed, s_added, s_removed, s_changed]):
        # Something outside paths and schemas moved — metadata, servers,
        # security schemes. Name the top-level keys rather than the file.
        top = sorted(k for k in set(old) | set(new) if old.get(k) != new.get(k))
        listing("top-level sections that differ", top)

    sys.exit(1)

if mode == "types":
    old_lines = read(committed_path, "The committed SDK types").splitlines(keepends=True)
    new_lines = read(regenerated_path, "The regenerated SDK types").splitlines(keepends=True)

    if old_lines == new_lines:
        sys.exit(0)

    print("The committed SDK types no longer match the API document they come from.")
    print("")
    diff = list(difflib.unified_diff(old_lines, new_lines, "committed", "regenerated", n=2))
    for line in diff[:40]:
        print("  " + line.rstrip("\n"))
    if len(diff) > 40:
        print(f"  ... and {len(diff) - 40} more diff lines")
    sys.exit(1)

print(f"unknown comparison mode: {mode}")
sys.exit(2)
'

# ---------------------------------------------------------------------------
# Self-verification. Runs before the real comparisons, every time.
#
# The comparisons are the only thing standing between a stale document and
# a green run, and their silent failure mode looks exactly like success.
# So each is first pointed at sample files whose answer is known.
# ---------------------------------------------------------------------------

CONTROL_DIR="$(mktemp -d)"
trap 'rm -rf "$CONTROL_DIR"' EXIT

control_failures=""
control_count=0
fail_case() {
  control_failures="${control_failures}  $1"$'\n'
  control_count=$((control_count + 1))
}

# Runs a comparison exactly as the real ones below run it, and checks the
# verdict rather than the wording.
expect_compare() {
  local expected="$1" mode="$2" committed="$3" regenerated="$4" description="$5" status=0
  python3 -c "$COMPARE_PY" "$mode" "$CONTROL_DIR/$committed" "$CONTROL_DIR/$regenerated" \
    >/dev/null 2>&1 || status=$?
  if [[ "$status" -ne "$expected" ]]; then
    fail_case "$description (expected exit $expected, got $status)"
  fi
}

cat >"$CONTROL_DIR/doc-committed.json" <<'JSON'
{
  "openapi": "3.1.0",
  "paths": { "/v1/tasks": { "get": { "operationId": "listTasks" } } },
  "components": { "schemas": { "Task": { "properties": { "title": { "maxLength": 200 } } } } }
}
JSON
cp "$CONTROL_DIR/doc-committed.json" "$CONTROL_DIR/doc-same.json"
python3 - "$CONTROL_DIR" <<'PY'
import json, os, sys

control = sys.argv[1]
base = json.load(open(os.path.join(control, "doc-committed.json")))

added = json.loads(json.dumps(base))
added["paths"]["/v1/tasks/{taskId}"] = {"get": {"operationId": "getTask"}}
json.dump(added, open(os.path.join(control, "doc-added-path.json"), "w"))

narrowed = json.loads(json.dumps(base))
narrowed["components"]["schemas"]["Task"]["properties"]["title"]["maxLength"] = 120
json.dump(narrowed, open(os.path.join(control, "doc-changed-schema.json"), "w"))

json.dump({"openapi": "3.1.0", "paths": {}}, open(os.path.join(control, "doc-no-paths.json"), "w"))
PY

expect_compare 0 document doc-committed.json doc-same.json \
  "a regenerated document identical to the committed one must not be reported as drift"
expect_compare 1 document doc-committed.json doc-added-path.json \
  "a path the services declare and the document lacks must be reported"
expect_compare 1 document doc-committed.json doc-changed-schema.json \
  "a schema constraint that changed must be reported"
expect_compare 2 document doc-committed.json doc-no-paths.json \
  "a regenerated document with no paths must not read as agreement"
expect_compare 2 document doc-committed.json doc-missing.json \
  "a regenerated document that was never written must not read as agreement"

printf 'export interface paths {\n  "/v1/tasks": { get: never };\n}\n' >"$CONTROL_DIR/types-committed.ts"
cp "$CONTROL_DIR/types-committed.ts" "$CONTROL_DIR/types-same.ts"
printf 'export interface paths {\n  "/v1/tasks": { get: never };\n  "/v1/tasks/{taskId}": { get: never };\n}\n' >"$CONTROL_DIR/types-changed.ts"
: >"$CONTROL_DIR/types-empty.ts"

expect_compare 0 types types-committed.ts types-same.ts \
  "regenerated SDK types identical to the committed ones must not be reported as drift"
expect_compare 1 types types-committed.ts types-changed.ts \
  "SDK types missing an operation the document describes must be reported"
expect_compare 2 types types-committed.ts types-empty.ts \
  "empty regenerated SDK types must not read as agreement"

if [[ $control_count -gt 0 ]]; then
  echo "check-openapi-drift: $control_count self-verification case(s) failed, so nothing was compared:" >&2
  printf '%s' "$control_failures" >&2
  echo "" >&2
  echo "The comparison itself is wrong. Fix it before trusting anything this" >&2
  echo "check says about the API document." >&2
  exit 2
fi

rm -rf "$CONTROL_DIR"
trap - EXIT

# ── regenerate into scratch ──

for artifact in "$DOCUMENT" "$TYPES"; do
  if [[ ! -f "$artifact" ]]; then
    echo "ERROR: $artifact is missing; there is nothing to compare against." >&2
    echo "  make gen-sdk" >&2
    exit 2
  fi
done

SCRATCH="$(mktemp -d)"
trap 'rm -rf "$SCRATCH"' EXIT

# Each service is dumped separately and merged, so each dump is checked on
# its own: a service that contributed nothing would otherwise be covered by
# the other one's paths in the merged total.
#
# The generators log to stderr on a successful run too, so their output is
# held and shown only when the run they came from failed.
dump_service() {
  local service="$1" out="$2"
  if ! (cd "$ROOT_DIR/apps/$service" && go run ./cmd/dump-openapi -o "$out") >"$SCRATCH/$service.log" 2>&1; then
    echo "ERROR: dumping the $service API document failed." >&2
    cat "$SCRATCH/$service.log" >&2
    return 2
  fi
  local paths
  paths="$(python3 -c '
import json, sys
print(len(json.load(open(sys.argv[1])).get("paths", {})))
' "$out" 2>/dev/null || echo 0)"
  if [[ "$paths" -eq 0 ]]; then
    echo "ERROR: $service dumped a document with no paths, so it contributed nothing to compare." >&2
    return 2
  fi
  echo "$paths"
}

flow_paths="$(dump_service flow-api "$SCRATCH/openapi-flow.json")" || exit 2
auth_paths="$(dump_service auth-api "$SCRATCH/openapi-auth.json")" || exit 2

if ! (cd "$ROOT_DIR/scripts" && go run merge-openapi.go -o "$SCRATCH/openapi.json" \
  "$SCRATCH/openapi-flow.json" "$SCRATCH/openapi-auth.json") >"$SCRATCH/merge.log" 2>&1; then
  echo "ERROR: merging the two API documents failed." >&2
  cat "$SCRATCH/merge.log" >&2
  exit 2
fi

# The merge combines the two documents, so it cannot end up with fewer
# paths than either input carried; if it did, most of the surface stopped
# being compared while the run still looked complete.
merged_paths="$(python3 -c '
import json, sys
print(len(json.load(open(sys.argv[1])).get("paths", {})))
' "$SCRATCH/openapi.json" 2>/dev/null || echo 0)"
if [[ "$merged_paths" -lt "$flow_paths" || "$merged_paths" -lt "$auth_paths" ]]; then
  echo "ERROR: the merged document has $merged_paths paths, fewer than the $flow_paths + $auth_paths dumped." >&2
  echo "  Part of the API surface did not reach the comparison." >&2
  exit 2
fi

# The committed document is formatted, so the scratch one has to be too.
# The formatter is pointed at the repository config explicitly: a file
# outside the tree finds no config by discovery and would be formatted to
# the tool's own defaults instead of this repository's.
if ! (cd "$ROOT_DIR" && bunx biome format --config-path="$ROOT_DIR" --write "$SCRATCH/openapi.json") >/dev/null; then
  echo "ERROR: formatting the regenerated API document failed." >&2
  exit 2
fi

# The types are generated from the committed document, not from the
# regenerated one: this comparison answers whether the types were
# regenerated after the document last changed, which is a separate
# staleness from the document's own.
#
# openapi-typescript emits through the TypeScript compiler's AST factory,
# which only the 5.x JavaScript implementation ships, so it runs behind
# the same resolver shim the generate target uses.
if ! (cd "$ROOT_DIR/packages/sdk" && node --import ../../scripts/openapi-typescript-runtime.mjs \
  ../../node_modules/openapi-typescript/bin/cli.js openapi.json -o "$SCRATCH/openapi.ts") \
  >"$SCRATCH/types.log" 2>&1; then
  echo "ERROR: regenerating the SDK types failed." >&2
  cat "$SCRATCH/types.log" >&2
  exit 2
fi

# ── compare ──

# Both comparisons run even when the first finds drift, so one stale half
# does not hide the state of the other.
drift=0

document_status=0
python3 -c "$COMPARE_PY" document "$DOCUMENT" "$SCRATCH/openapi.json" || document_status=$?
if [[ $document_status -eq 2 ]]; then
  exit 2
fi
if [[ $document_status -ne 0 ]]; then
  echo ""
  echo "Run the generator and commit the document alongside the handler change:"
  echo "  make gen-openapi   (or make gen-sdk to refresh the SDK types with it)"
  echo ""
  drift=1
fi

types_status=0
python3 -c "$COMPARE_PY" types "$TYPES" "$SCRATCH/openapi.ts" || types_status=$?
if [[ $types_status -eq 2 ]]; then
  exit 2
fi
if [[ $types_status -ne 0 ]]; then
  echo ""
  echo "Run the generator and commit the SDK types alongside the document:"
  echo "  make gen-sdk-types"
  echo ""
  drift=1
fi

if [[ $drift -ne 0 ]]; then
  exit 1
fi

echo "API document matches the services ($merged_paths paths from flow-api and auth-api), and the SDK types match the document"
