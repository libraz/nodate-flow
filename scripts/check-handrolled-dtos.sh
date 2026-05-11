#!/usr/bin/env bash
# check-handrolled-dtos.sh -- detect hand-rolled response DTO interfaces
# in route / feature code that should reuse the generated SDK schemas.
#
# Background:
#   Every API response shape is generated from the Go handlers via
#   openapi-typescript (`make gen-sdk`) and surfaced as
#   `components['schemas']['<Name>']` from `@nodate-flow/sdk`. Re-declaring
#   those shapes locally (e.g. `interface WorkspacesBody { items: ... }`)
#   silently allows the local copy to drift from the wire format -- a real
#   bug took the /workspaces page down because the API returned
#   `{ workspaces: ... }` while the route declared `{ items: ... }`.
#
# What this checks:
#   Bans `interface <Foo>Body`, `interface <Foo>OutputBody`,
#   `interface <Foo>ResponseBody`, and `interface <Foo>Response` declared
#   inside `apps/accounts-web/src/` or `apps/flow-web/src/`. Use one of:
#
#     type Foo = components['schemas']['Foo'];
#     type Foo = components['schemas']['FooOutputBody'];
#     type Foo = Pick<components['schemas']['Foo'], 'a' | 'b'>;
#
#   from `@nodate-flow/sdk` instead. If a local interface is genuinely a
#   normalised view-model (not a 1:1 mirror of the API), give it a name
#   that does not end in Body / OutputBody / ResponseBody / Response (e.g.
#   `NormalisedShareRender`, `UnreadCountResult`).
#
# Excluded:
#   - test / spec / fixture files (`*.test.*`, `*.spec.*`, `__tests__/`,
#     `e2e/`)
#   - `node_modules`, `.git`, `dist`
#   - generated route tree (`routeTree.gen.ts`)
#
# Usage:
#   ./scripts/check-handrolled-dtos.sh        # exit 1 on any hit
#
# Companion to: docs/conventions/frontend.md (SDK type usage),
#               docs/conventions/api-types.md (API contract).

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Matches: `interface FooBody {`, `interface FooOutputBody {`,
#          `interface FooResponseBody {`, `interface FooResponse {`
# Anchored to the start of the line so we don't trip over JSDoc / comments
# that mention the suffix in prose.
PATTERN='^interface +[A-Z][A-Za-z0-9]*(Body|OutputBody|ResponseBody|Response)[[:space:]]*(extends|<|\{)'

SCAN_DIRS=(
  "$ROOT/apps/accounts-web/src"
  "$ROOT/apps/flow-web/src"
)

EXCLUDES=(
  --exclude-dir=node_modules
  --exclude-dir=.git
  --exclude-dir=dist
  --exclude-dir=__tests__
  --exclude-dir=e2e
  --exclude='*.test.*'
  --exclude='*.spec.*'
  --exclude='*.d.ts'
  --exclude='routeTree.gen.ts'
)

hits=""
count=0

for dir in "${SCAN_DIRS[@]}"; do
  [[ -d "$dir" ]] || continue
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    hits="${hits}${line}"$'\n'
    count=$((count + 1))
  done < <(grep -rnE "$PATTERN" "$dir" "${EXCLUDES[@]}" \
    --include='*.ts' \
    --include='*.tsx' \
    2>/dev/null || true)
done

if [[ $count -gt 0 ]]; then
  echo "Found $count hand-rolled response DTO interface(s):"
  printf '%s' "$hits" | sort
  echo ""
  echo "Replace each with a generated SDK type, e.g.:"
  echo ""
  echo "  import type { components } from '@nodate-flow/sdk';"
  echo "  type Foo = components['schemas']['Foo'];"
  echo ""
  echo "If the type is a deliberate view-model (not a 1:1 API mirror),"
  echo "rename it so it does not end in Body / OutputBody /"
  echo "ResponseBody / Response."
  echo ""
  echo "See docs/conventions/frontend.md for the canonical pattern."
  exit 1
fi

echo "No hand-rolled response DTO interfaces found."
