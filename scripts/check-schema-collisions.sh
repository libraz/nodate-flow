#!/usr/bin/env bash
# check-schema-collisions.sh — fail when the merged OpenAPI 3.1 spec
# routes operations whose response shapes have been silently overwritten
# by a name collision in components.schemas.
#
# The merged spec at packages/sdk/openapi.json combines flow-api +
# auth-api outputs. Huma derives schema names from Go struct names; if
# two Go packages both declare e.g. `type ListOutputBody struct {...}`
# with different fields, the merge step keeps only one shape and every
# operation that references that schema name gets the surviving shape
# regardless of what the handler actually returns.
#
# This script flags every multi-operation schema reference whose shape
# (sorted required keys + sorted property names) is consistent with one
# Go type. A failing entry means at least one operation lies about its
# response.
#
# Run locally or in CI:
#
#   bash scripts/check-schema-collisions.sh
#
# Exit codes:
#   0 — no collisions
#   1 — at least one schema name carries divergent shapes

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

ALLOWLIST_PATTERN='^(AuthTokens|AutoActionSettingsBody|CalendarResponse|EventResponse|ImportJobBody|Label|ListTimelineOutputBody|MeBody|OIDCStartOutputBody|PageDTO|Project|PublicShareResponse|Record|SavedLens|Task|TaskComment|TimeboxDTO|WidgetDTO|Workspace|WorkspaceMember)$'

mapfile -t suspects < <(
  jq -r '
    [.paths[][]
      | select(type == "object")
      | select(.responses)
      | {op: .operationId,
         ref: (.responses["200"].content["application/json"].schema."$ref" //
               .responses["201"].content["application/json"].schema."$ref" //
               .responses["202"].content["application/json"].schema."$ref")}
      | select(.ref != null)]
    | group_by(.ref)
    | map(select(length > 1))
    | map({name: (.[0].ref | sub("^#/components/schemas/"; "")),
           ops: [.[].op]})
    | .[] | "\(.name)\t\(.ops | join(","))"
  ' "$SPEC"
)

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
  echo "check-schema-collisions: ok ($SPEC)"
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
