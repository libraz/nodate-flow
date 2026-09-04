#!/usr/bin/env bash
# check-handrolled-dtos.sh -- detect hand-rolled response DTO declarations
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
#   Bans a response shape spelled out by hand under
#   `apps/accounts-web/src/` or `apps/flow-web/src/`, in either form a
#   declaration can take:
#
#     interface <Foo>Body / <Foo>OutputBody / <Foo>ResponseBody / <Foo>Response
#     type <Foo>Response = { ... }        (right-hand side an object literal)
#
#   Both describe the shape rather than derive it, so `type` is not an
#   escape hatch from the `interface` ban. Use one of:
#
#     type Foo = components['schemas']['Foo'];
#     type Foo = components['schemas']['FooOutputBody'];
#     type Foo = Pick<components['schemas']['Foo'], 'a' | 'b'>;
#
#   from `@nodate-flow/sdk` instead. An alias whose right-hand side is a
#   `components[...]` lookup, a `Pick` / `Omit` over one, or another type
#   is derived rather than hand-rolled and is not flagged.
#
#   If a local type is genuinely a normalised view-model (not a 1:1 mirror
#   of the API), give it a name that does not end in Body / OutputBody /
#   ResponseBody / Response (e.g. `NormalisedShareRender`,
#   `UnreadCountResult`) -- and still derive its members from the schema
#   where it mirrors one, so a field rename upstream fails the build.
#
# Excluded:
#   - test / spec / fixture files (`*.test.*`, `*.spec.*`, `__tests__/`,
#     `e2e/`)
#   - `node_modules`, `.git`, `dist`
#   - generated route tree (`routeTree.gen.ts`)
#
# Every scan root must reach at least one file. A root that matches
# nothing is reported as a failure rather than as a pass, because a scan
# over zero files is indistinguishable from a clean scan.
#
# Reading files still does not prove the patterns can match: an anchor
# tightened one character too far matches no declaration in the tree and
# prints the same success line as a clean tree. The self-verification
# below runs both patterns over known-bad and known-good samples on every
# invocation, before the real scan, and a failure among them stops the
# run with exit 2.
#
# Usage:
#   ./scripts/check-handrolled-dtos.sh        # exit 1 on any hit
#
# Companion to: docs/conventions/frontend.md (SDK type usage),
#               docs/conventions/api-types.md (API contract).

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Both patterns are anchored to the start of the line so we don't trip over
# JSDoc / comments that mention a suffix in prose: a declaration starts at
# column 0, while prose inside a block comment is indented behind a `*`.
SUFFIXED_NAME='[A-Z][A-Za-z0-9]*(Body|OutputBody|ResponseBody|Response)'

# `interface FooBody {`, `interface FooResponse extends`, and so on,
# optionally prefixed with `export ` or `export default `.
INTERFACE_PATTERN="^(export +(default +)?)?interface +${SUFFIXED_NAME}[[:space:]]*(extends|<|\{)"

# `type FooResponse = {`, i.e. an alias whose right-hand side opens an
# object type literal on the same line. A derived alias is left alone: it
# either names its source (`= components[...]`, `= Pick<...>`) or wraps to
# the next line, and neither puts a `{` after the `=`.
ALIAS_PATTERN="^(export +)?type +${SUFFIXED_NAME}[[:space:]]*(<[^=]*>)?[[:space:]]*=[[:space:]]*\{"

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

INCLUDES=(
  --include='*.ts'
  --include='*.tsx'
)

# Declarations under $1, as `path:lineno:text` lines. grep exits 1 when it
# matches nothing, which under `set -e` with pipefail would end the caller
# with no message; a clean tree is not an error here, so that status is
# absorbed.
scan_root() {
  grep -rnE -e "$INTERFACE_PATTERN" -e "$ALIAS_PATTERN" \
    "$1" "${EXCLUDES[@]}" "${INCLUDES[@]}" \
    2>/dev/null || true
}

# ---------------------------------------------------------------------------
# Self-verification. Runs before the scan, every time.
# ---------------------------------------------------------------------------

CONTROL_DIR="$(mktemp -d)"
trap 'rm -rf "$CONTROL_DIR"' EXIT

cat >"$CONTROL_DIR/interface.ts" <<'TS'
export interface WorkspacesBody {
  workspaces: Workspace[];
}
TS

cat >"$CONTROL_DIR/alias.ts" <<'TS'
export type WorkspacesResponse = {
  workspaces: Workspace[];
};
TS

# Derived from the generated schema rather than spelled out, which is the
# form the message below asks for. An alias branch that stopped drawing
# this distinction would ban the fix along with the defect.
cat >"$CONTROL_DIR/derived.ts" <<'TS'
import type { components } from '@nodate-flow/sdk';

export type MembersResponse = components['schemas']['MembersOutputBody'];
export type MemberSummaryResponse = Pick<components['schemas']['Member'], 'id' | 'role'>;
TS

# Prose, not code. Both lines would match if the patterns lost their
# column-0 anchor, which is what keeps documentation out of the results.
cat >"$CONTROL_DIR/prose.ts" <<'TS'
/**
 * A comment naming `interface WorkspacesBody {` or
 * `type WorkspacesResponse = {` describes the ban; it does not break it.
 */
export const NOTE = 1;
TS

control_failures=""
control_count=0
fail_case() {
  control_failures="${control_failures}  $1"$'\n'
  control_count=$((control_count + 1))
}

control_out="$(scan_root "$CONTROL_DIR")"

grep -q 'interface\.ts:1:' <<<"$control_out" \
  || fail_case "a hand-rolled 'interface FooBody {' declaration must be reported"
grep -q 'alias\.ts:1:' <<<"$control_out" \
  || fail_case "a hand-rolled 'type FooResponse = {' alias must be reported"
! grep -q 'derived\.ts' <<<"$control_out" \
  || fail_case "an alias derived from components['schemas'] must not be reported"
! grep -q 'prose\.ts' <<<"$control_out" \
  || fail_case "a suffixed name mentioned in an indented comment must not be reported"

if [[ $control_count -gt 0 ]]; then
  echo "check-handrolled-dtos: $control_count self-verification case(s) failed, so the scan was not run:" >&2
  printf '%s' "$control_failures" >&2
  echo "" >&2
  echo "The patterns themselves are wrong. Fix them before trusting anything" >&2
  echo "this check says about the sources." >&2
  exit 2
fi

rm -rf "$CONTROL_DIR"
trap - EXIT

hits=""
count=0

for dir in "${SCAN_DIRS[@]}"; do
  # `grep -c ''` reports a count for every file it opens, empty ones
  # included, so this is the file set the scan below actually sees.
  scanned=$({ grep -rc '' "$dir" "${EXCLUDES[@]}" "${INCLUDES[@]}" 2>/dev/null || true; } | wc -l | tr -d ' ')
  if [[ "$scanned" -eq 0 ]]; then
    echo "check-handrolled-dtos: scan root reached 0 files: $dir" >&2
    echo "The scan cannot report a violation it never read. Fix the path" >&2
    echo "or the include/exclude filters before trusting this check." >&2
    exit 2
  fi

  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    hits="${hits}${line}"$'\n'
    count=$((count + 1))
  done < <(scan_root "$dir")
done

if [[ $count -gt 0 ]]; then
  echo "Found $count hand-rolled response DTO declaration(s):"
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

echo "No hand-rolled response DTO declarations found."
