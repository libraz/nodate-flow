#!/usr/bin/env bash
# core-self-contained.sh — verify sql/core/ can be read by someone who
# only has sql/core/.
#
# The directory is vendored into other repositories, so a comment that
# cites a design note, a source file, or a table from this repository's
# product layer arrives as a pointer the reader cannot follow. Whatever
# such a comment was explaining has to be stated where it is written.
#
# Exit codes:
#   0 — nothing outside core is referenced
#   1 — a reference was found
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CORE_DIR="$(cd "$SCRIPT_DIR/../sql/core" && pwd)"

# Each entry is "pattern<TAB>what to do instead".
PATTERNS=(
  'ADR [0-9]|docs/adr/	restate the decision inline; the design notes are not published'
  'docs/[a-z]	restate the rule inline; that path does not exist outside this repository'
  'apps/[a-z-]+/	name the behaviour, not the binary that happens to implement it'
  'sql/flow/	core must not depend on a product layer being present'
  '\.sql\)	name the rule, not the query file that applies it'
)

status=0
for entry in "${PATTERNS[@]}"; do
  pattern="${entry%%	*}"
  advice="${entry#*	}"
  # PROTOCOL.md is the contract's own prose and may name core's files.
  if hits="$(grep -rInE "$pattern" "$CORE_DIR" --exclude=PROTOCOL.md 2>/dev/null)"; then
    echo "---- sql/core references something a vendored copy will not have ----"
    echo "$hits"
    echo "  -> $advice"
    echo ""
    status=1
  fi
done

if [[ $status -eq 0 ]]; then
  echo "sql/core is self-contained."
else
  echo "sql/core is not self-contained; see the findings above."
fi
exit $status
