#!/usr/bin/env bash
# check-schema-collisions.sh — fail when the merged OpenAPI 3.1 spec
# routes operations whose request or response shapes have been silently
# overwritten by a name collision in components.schemas.
#
# The merged spec at packages/sdk/openapi.json combines flow-api +
# auth-api outputs. Huma derives schema names from Go struct names; if
# two Go packages both declare e.g. `type ListOutputBody struct {...}`
# with different fields, the merge step keeps only one shape and every
# operation that references that schema name gets the surviving shape
# regardless of what the handler actually returns (response side) or
# actually accepts (request side).
#
# This script flags every schema name referenced by 2+ distinct
# operations, whether that reference comes from a 200/201/202 response
# body or from a requestBody. A failing entry means at least one of
# those operations was renamed away from its own Go type by the merge.
#
# Run locally or in CI:
#
#   bash scripts/check-schema-collisions.sh
#
# Exit codes:
#   0 — no collisions
#   1 — at least one schema name carries divergent shapes
#   2 — the check could not be performed (no spec, no jq, or a spec whose
#       shape the queries below no longer reach into)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

SPEC="${1:-$ROOT_DIR/packages/sdk/openapi.json}"

if [[ ! -f "$SPEC" ]]; then
  echo "ERROR: $SPEC not found. Run 'make gen-openapi' first." >&2
  exit 2
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "ERROR: jq is required" >&2
  exit 2
fi

# A schema name is a "real collision" only when:
#   1. multiple operations reference it under their 200/201/202 responses
#   2. at least two of those operations expect different shapes
#
# Since the spec stores exactly one shape per name, we approximate (2)
# by checking whether the schema's key set looks operation-generic
# (e.g. `ListOutputBody` shared between `notifications-list`,
# `webhooks-list`, `workspaces-list`). The intent of this guard is to
# fail loudly the moment a generic name like `ListOutputBody` appears
# under multiple operations again. We allowlist names that are known
# shared DTOs (e.g. `Task`, `Workspace`, `CalendarResponse`).

# A name belongs here only when every operation carrying it resolves to
# one Go declaration. Two declarations that merely happen to have the same
# fields today are not shared: they drift independently, and the merge
# keeps whichever it saw last.
#
# LoginBody is a single auth-api type (handlers/auth/dto.go) deliberately
# reused as the response Body for every operation that finishes a sign-in:
# POST /auth/login, magic-link verify, and the OIDC google/github/microsoft
# callbacks. They all return the same discriminated login envelope
# (step=complete|totp_required), so the shared schema is intentional, not a
# silent overwrite.
#
# IntegrationMapping is the one resource type handlers/integrationmappings
# returns from both create and patch; PreferencesOutputBody is the one type
# handlers/notifications returns from both the list and the update.
ALLOWLIST_PATTERN='^(AdminDeleteOutputBody|AuthTokens|AutoActionSettingsBody|CalendarResponse|EventResponse|ImportJobBody|IntegrationMapping|Label|ListTimelineOutputBody|LoginBody|MeBody|OIDCStartOutputBody|PageDTO|PreferencesOutputBody|Project|PublicShareResponse|Record|SavedLens|Task|TaskComment|TimeboxDTO|WidgetDTO|Workspace|WorkspaceMember)$'

# The detector: every schema name that 2+ distinct operations reference
# from a 200/201/202 response body or from a requestBody, as
# `name<TAB>op,op` lines. Held in one place so the samples below are
# judged by the same query the spec is.
SUSPECTS_JQ='
  [.paths[][]
    | select(type == "object")
    | select(.operationId)
    | . as $op
    | (
        ([$op.responses["200"]?, $op.responses["201"]?, $op.responses["202"]?]
          | map(.content["application/json"].schema."$ref"? // empty))
        + ([$op.requestBody?.content["application/json"].schema."$ref"? // empty])
      )
    | map(select(. != null and . != ""))
    | unique
    | .[]
    | {op: $op.operationId, ref: .}]
  | unique_by([.op, .ref])
  | group_by(.ref)
  | map(select((map(.op) | unique | length) > 1))
  | map({name: (.[0].ref | sub("^#/components/schemas/"; "")),
         ops: ([.[].op] | unique)})
  | .[] | "\(.name)\t\(.ops | join(","))"
'

# ---------------------------------------------------------------------------
# Self-verification. Runs before the scan, every time.
#
# The emptiness counts below prove the spec was read; they do not prove
# the query can still single a shared name out of it. A traversal that has
# stopped reaching either body position returns an empty suspect list,
# which is exactly what a collision-free spec returns. So the query is run
# first against a synthetic spec whose collisions are known.
# ---------------------------------------------------------------------------

CONTROL_DIR="$(mktemp -d)"
trap 'rm -rf "$CONTROL_DIR"' EXIT

# a-list and b-list share a response schema (the defect); c-get holds one
# of its own (the legitimate neighbour); d-create and e-replace share a
# request schema, which is the half of the traversal that has no response
# to fall back on.
cat >"$CONTROL_DIR/spec.json" <<'JSON'
{
  "openapi": "3.1.0",
  "paths": {
    "/a": {
      "get": {
        "operationId": "a-list",
        "responses": {
          "200": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/ListOutputBody"}}}}
        }
      }
    },
    "/b": {
      "get": {
        "operationId": "b-list",
        "responses": {
          "200": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/ListOutputBody"}}}}
        }
      }
    },
    "/c": {
      "get": {
        "operationId": "c-get",
        "responses": {
          "200": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/COutputBody"}}}}
        }
      }
    },
    "/d": {
      "post": {
        "operationId": "d-create",
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/SharedInputBody"}}}},
        "responses": {
          "201": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/DOutputBody"}}}}
        }
      }
    },
    "/e": {
      "put": {
        "operationId": "e-replace",
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/SharedInputBody"}}}},
        "responses": {
          "200": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/EOutputBody"}}}}
        }
      }
    }
  },
  "components": {
    "schemas": {
      "ListOutputBody": {"type": "object"},
      "COutputBody": {"type": "object"},
      "DOutputBody": {"type": "object"},
      "EOutputBody": {"type": "object"},
      "SharedInputBody": {"type": "object"}
    }
  }
}
JSON

control_failures=""
control_count=0
fail_case() {
  control_failures="${control_failures}  $1"$'\n'
  control_count=$((control_count + 1))
}

if ! control_out="$(jq -r "$SUSPECTS_JQ" "$CONTROL_DIR/spec.json")"; then
  fail_case "the query did not run at all against the sample spec (jq's error is above)"
  control_out=""
fi

grep -q "^ListOutputBody"$'\t' <<<"$control_out" \
  || fail_case "a schema name under two operations' responses must be reported"
grep -q "^SharedInputBody"$'\t' <<<"$control_out" \
  || fail_case "a schema name under two operations' request bodies must be reported"
! grep -q "^COutputBody"$'\t' <<<"$control_out" \
  || fail_case "a schema name only one operation references must not be reported"

# The allowlist is the other half of the verdict: a pattern that has
# drifted into matching everything silences every collision the query
# finds, and the run still prints ok.
if ! [[ "Task" =~ $ALLOWLIST_PATTERN ]]; then
  fail_case "ALLOWLIST_PATTERN must still match the shared DTOs it names (Task)"
fi
if [[ "TaskDraftOutputBody" =~ $ALLOWLIST_PATTERN ]]; then
  fail_case "ALLOWLIST_PATTERN must match whole names only, not a name that merely starts with one"
fi

if [[ $control_count -gt 0 ]]; then
  echo "check-schema-collisions: $control_count self-verification case(s) failed, so the scan was not run:" >&2
  printf '%s' "$control_failures" >&2
  echo "" >&2
  echo "The query itself is wrong. Fix it before trusting anything this check" >&2
  echo "says about $SPEC." >&2
  exit 2
fi

rm -rf "$CONTROL_DIR"
trap - EXIT

# The whole verdict rests on one jq traversal of a generated document.
# Every way that traversal can stop reaching the spec — a path layout the
# `.paths[][]` walk no longer matches, response bodies moving out from
# under `content["application/json"].schema.$ref`, a truncated
# regeneration — ends in an empty suspect list, which is exactly what a
# clean spec produces. So the inputs to the traversal are counted first
# and each is required to be non-empty in its own right.
if ! counts="$(jq -r '
  ([.paths[]? | .[]? | select(type == "object") | select(.operationId)]) as $ops
  | ([$ops[]
      | (([.responses["200"]?, .responses["201"]?, .responses["202"]?]
          | map(.content["application/json"].schema."$ref"? // empty))
         + [.requestBody?.content["application/json"].schema."$ref"? // empty])
      | .[]]) as $refs
  | "\($ops | length)\t\($refs | length)\t\((.components.schemas? // {}) | length)"
' "$SPEC")"; then
  echo "check-schema-collisions: $SPEC could not be read as JSON (jq's error is above), so no schema name was compared." >&2
  exit 2
fi

op_count="$(cut -f1 <<<"$counts")"
ref_count="$(cut -f2 <<<"$counts")"
schema_count="$(cut -f3 <<<"$counts")"

if [[ "$op_count" -eq 0 ]]; then
  echo "check-schema-collisions: no operation was found in $SPEC, so no schema name was compared." >&2
  echo "  The spec is empty, or operations no longer live under .paths[][] with an operationId." >&2
  exit 2
fi

if [[ "$schema_count" -eq 0 ]]; then
  echo "check-schema-collisions: components.schemas is empty in $SPEC, so there is no name left to collide." >&2
  echo "  Regenerate the merged spec ('make gen-openapi'); this one carries no schemas at all." >&2
  exit 2
fi

if [[ "$ref_count" -eq 0 ]]; then
  echo "check-schema-collisions: $op_count operation(s) were walked and not one request or response \$ref was collected, so nothing was compared." >&2
  echo "  Request and response bodies no longer sit under content[\"application/json\"].schema.\$ref," >&2
  echo "  so the query below can never see a shared name however many there are." >&2
  exit 2
fi

mapfile -t suspects < <(jq -r "$SUSPECTS_JQ" "$SPEC")

violations=()
for line in "${suspects[@]}"; do
  name="${line%%$'\t'*}"
  ops="${line#*$'\t'}"
  if [[ "$name" =~ $ALLOWLIST_PATTERN ]]; then
    continue
  fi
  violations+=("$name -> $ops")
done

if [[ ${#violations[@]} -eq 0 ]]; then
  echo "check-schema-collisions: ok — $ref_count body reference(s) across $op_count operation(s), $schema_count schema(s) ($SPEC)"
  exit 0
fi

echo "check-schema-collisions: found ${#violations[@]} colliding schema name(s) in $SPEC" >&2
echo "" >&2
echo "Each entry below is referenced by 2+ operations but is NOT on the allowlist" >&2
echo "of intentionally shared DTOs. Rename the Go output struct(s) so each" >&2
echo "operation gets its own component schema (e.g. \`type ListOutputBody\` ->" >&2
echo "\`type WorkspacesListOutputBody\`)." >&2
echo "" >&2
for v in "${violations[@]}"; do
  echo "  - $v" >&2
done
echo "" >&2
echo "If a name is genuinely a shared DTO, add it to ALLOWLIST_PATTERN in" >&2
echo "$0." >&2
exit 1
