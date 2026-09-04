#!/usr/bin/env bash
# check-hardcoded-strings.sh — detect hardcoded user-facing strings in JSX
# attributes that should route through i18n.
#
# Scans TSX files for `placeholder=`, `aria-label=`, `aria-description=`,
# `title=`, and `alt=` attributes whose value is a literal English-looking
# string instead of a `t('key')` call.
#
# Usage:
#   ./scripts/check-hardcoded-strings.sh
#
# There is one mode: a hit fails. Arguments are rejected rather than
# ignored, so a caller still passing the old --ci flag is visible.
#
# Rationale:
#   No hardcoded UI strings: everything routes through `t('key')`. All UI
#   strings live in `apps/<app>/locales/{en,ja,zh}/` so translators have a
#   single source of truth and the app stays locale-flippable without code
#   changes. An attribute is the easiest place to forget, because it reads
#   as markup rather than as copy.
#
# Excluded:
#   - test / spec / fixture files (literal copy is sometimes required for
#     equality assertions)
#   - node_modules, dist, .git
#   - generated SDK output
#
# Every scan root must reach at least one file. A root that matches
# nothing is reported as a failure rather than as a pass, because a scan
# over zero files is indistinguishable from a clean scan.
#
# Reading files still does not prove the pattern can match: an attribute
# list or a quoting rule that has stopped matching anything prints the
# same success line as a clean tree. The self-verification below runs the
# pattern over known-bad and known-good samples on every invocation,
# before the real scan, and a failure among them stops the run with
# exit 2.

set -euo pipefail

if [[ $# -gt 0 ]]; then
  echo "check-hardcoded-strings: takes no arguments (got: $*)" >&2
  exit 2
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Detects: attr="Word ...", attr='Word ...' where the value contains at
# least one alphabetic run of length >= 4. Both quote forms are matched,
# each against a value that may not contain its own delimiter. Bound the
# inner pattern so we don't match expression-bound attrs like attr={t('x')}.
ATTR_PATTERN="(placeholder|aria-label|aria-description|title|alt)=(\"[^\"{}<>][^\"{}<>]*[A-Za-z]{4,}[^\"{}<>]*\"|'[^'{}<>][^'{}<>]*[A-Za-z]{4,}[^'{}<>]*')"

SCAN_DIRS=(
  "$ROOT/apps/flow-web/src"
  "$ROOT/apps/accounts-web/src"
)

EXCLUDES=(
  --exclude-dir=node_modules
  --exclude-dir=.git
  --exclude-dir=dist
  --exclude-dir=__tests__
  --exclude='*.test.*'
  --exclude='*.spec.*'
  --exclude='*.d.ts'
)

# Offending attributes under $1, as `path:lineno:text` lines. grep exits 1
# when it matches nothing, which under `set -e` with pipefail would end the
# caller with no message; a clean tree is not an error here, so that status
# is absorbed.
scan_root() {
  grep -rnE "$ATTR_PATTERN" "$1" "${EXCLUDES[@]}" --include='*.tsx' 2>/dev/null || true
}

# ---------------------------------------------------------------------------
# Self-verification. Runs before the scan, every time.
# ---------------------------------------------------------------------------

CONTROL_DIR="$(mktemp -d)"
trap 'rm -rf "$CONTROL_DIR"' EXIT

cat >"$CONTROL_DIR/literal.tsx" <<'TSX'
export const Search = () => <input placeholder="Search tasks" />;
TSX

# The fix the message asks for: the value is an expression, not a quoted
# literal. A pattern that stopped requiring the quote would flag this too
# and there would be nothing left to change.
cat >"$CONTROL_DIR/translated.tsx" <<'TSX'
export const Search = () => <input placeholder={t('tasks.search')} />;
TSX

# An attribute outside the list carries markup, not copy, however English
# its value looks.
cat >"$CONTROL_DIR/untargeted.tsx" <<'TSX'
export const Row = () => <div className="flex items-center gap-2" data-testid="task-row" />;
TSX

control_failures=""
control_count=0
fail_case() {
  control_failures="${control_failures}  $1"$'\n'
  control_count=$((control_count + 1))
}

control_out="$(scan_root "$CONTROL_DIR")"

grep -q 'literal\.tsx:1:' <<<"$control_out" \
  || fail_case "a quoted English literal in a placeholder attribute must be reported"
! grep -q 'translated\.tsx' <<<"$control_out" \
  || fail_case "an attribute bound to t('key') must not be reported"
! grep -q 'untargeted\.tsx' <<<"$control_out" \
  || fail_case "an attribute outside the user-facing list must not be reported"

if [[ $control_count -gt 0 ]]; then
  echo "check-hardcoded-strings: $control_count self-verification case(s) failed, so the scan was not run:" >&2
  printf '%s' "$control_failures" >&2
  echo "" >&2
  echo "The pattern itself is wrong. Fix it before trusting anything this" >&2
  echo "check says about the sources." >&2
  exit 2
fi

rm -rf "$CONTROL_DIR"
trap - EXIT

count=0
found_files=""

for dir in "${SCAN_DIRS[@]}"; do
  # `grep -c ''` reports a count for every file it opens, empty ones
  # included, so this is the file set the scan below actually sees.
  scanned=$({ grep -rc '' "$dir" "${EXCLUDES[@]}" --include='*.tsx' 2>/dev/null || true; } | wc -l | tr -d ' ')
  if [[ "$scanned" -eq 0 ]]; then
    echo "check-hardcoded-strings: scan root reached 0 .tsx files: $dir" >&2
    echo "The scan cannot report a violation it never read. Fix the path" >&2
    echo "or the include/exclude filters before trusting this check." >&2
    exit 2
  fi

  while IFS= read -r line; do
    count=$((count + 1))
    found_files="$found_files\n$line"
  done < <(scan_root "$dir")
done

if [[ $count -gt 0 ]]; then
  echo "Found $count hardcoded UI string(s) in JSX attributes:"
  echo -e "$found_files" | sort
  echo ""
  echo "Move the literal to apps/<app>/locales/{en,ja,zh}/<namespace>.json"
  echo "and use t('key') instead. Every UI string routes through i18n."
  exit 1
else
  echo "No hardcoded UI strings found in scanned JSX attributes."
fi
