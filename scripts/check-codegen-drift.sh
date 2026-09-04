#!/usr/bin/env bash
# check-codegen-drift.sh — verify the generated data layer still matches the
# schema and queries it is generated from.
#
# The schema and the code generated from it are two representations of the
# same thing, and only one of them is edited by hand. When a column is added
# and sqlc is not re-run, the generated struct simply lacks that field: the
# build still succeeds, the tests still pass, and the gap only surfaces when
# somebody tries to use the column and cannot. Nothing else in the pipeline
# notices, which is why this check exists.
#
# Column comments count. sqlc copies them into the generated Go doc comments,
# so a comment-only schema edit still leaves the generated files stale.
#
# The check never writes into the working tree: sqlc is pointed at a scratch
# directory and the result is compared against what is committed. A failure
# therefore tells you to run the generator; it does not half-run it for you.
#
# Reaching the generated files still does not prove the comparison can see
# drift: a comparison that opened nothing reports no drift for the same
# reason an up-to-date tree does. The self-verification below runs the
# comparison over sample trees whose drift is known, before the real one,
# and a failure among those stops the run.
#
# Usage:
#   bash scripts/check-codegen-drift.sh            # check everything
#   bash scripts/check-codegen-drift.sh --staged   # skip when no input is staged
#
# Exit codes:
#   0 — schema and generated code agree with their sources
#   1 — drift detected
#   2 — the check could not be performed

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
SQLC_CONFIG="$ROOT_DIR/sql/sqlc.yaml"

# Pinned because two sqlc versions can emit different output from identical
# input; comparing across versions would report drift that is not there.
SQLC_VERSION="v1.30.0"

staged_only=0
tool_only=0
for arg in "$@"; do
  case "$arg" in
    --staged) staged_only=1 ;;
    --check-tool) tool_only=1 ;;
    -h | --help)
      sed -n '2,32p' "${BASH_SOURCE[0]}"
      exit 0
      ;;
    *)
      echo "unknown argument: $arg" >&2
      exit 2
      ;;
  esac
done

# Generating with the wrong version rewrites every generated file, which
# reads as a huge legitimate-looking diff rather than as a mistake. The
# generate targets assert this before running, so the mistake is refused
# instead of reviewed.
assert_sqlc_version() {
  if ! command -v sqlc >/dev/null 2>&1; then
    echo "ERROR: sqlc is not installed." >&2
    echo "  go install github.com/sqlc-dev/sqlc/cmd/sqlc@$SQLC_VERSION" >&2
    exit 2
  fi
  local installed
  installed="$(sqlc version 2>/dev/null | tr -d '[:space:]')"
  if [[ "$installed" != "$SQLC_VERSION" ]]; then
    echo "ERROR: sqlc $installed is installed but this repository generates with $SQLC_VERSION." >&2
    echo "  Different versions emit different code from identical input." >&2
    echo "    go install github.com/sqlc-dev/sqlc/cmd/sqlc@$SQLC_VERSION" >&2
    exit 2
  fi
}

if [[ $tool_only -eq 1 ]]; then
  assert_sqlc_version
  echo "sqlc $SQLC_VERSION"
  exit 0
fi

# ── staged mode: only run when an input to the generators is staged ──

if [[ $staged_only -eq 1 ]]; then
  if ! git -C "$ROOT_DIR" rev-parse --git-dir >/dev/null 2>&1; then
    exit 0
  fi
  if ! git -C "$ROOT_DIR" diff --cached --name-only --diff-filter=ACMR \
    -- 'sql/**' | grep -q .; then
    exit 0
  fi
fi

# Generated output shares a directory with hand-written files (tests,
# .gitattributes), so the comparison is limited to what sqlc actually emits:
# every file it wrote into the scratch tree, plus any file left in the
# repository that carries a generated name but is no longer produced.
#
# Reads: <committed root> <scratch root> <output directory>...
# Exits: 0 in agreement, 1 on drift, 2 when nothing was compared.
COMPARE_PY='
import difflib, os, sys

root, scratch = sys.argv[1], sys.argv[2]
outs = sys.argv[3:]

GENERATED_NAMES = {"models.go", "db.go", "querier.go", "copyfrom.go", "batch.go"}


def is_generated_name(name):
    return name.endswith(".sql.go") or name in GENERATED_NAMES


problems = []
checked = set()
# Files compared per output directory. A comparison that opened nothing
# reports no drift, which is what an up-to-date tree reports too, so each
# output directory has to be shown to have produced something. Per
# directory rather than in total: with one of several redirected wrongly,
# a total stays satisfied by the others while that one stops being
# compared at all.
compared_per_out = {}

if not outs:
    print("No output directory reached the comparison, so no generated file was compared at all.")
    print("")
    print("The out: entries are read from sql/sqlc.yaml by the step above; this one received none.")
    sys.exit(2)

for rel in outs:
    produced = set()
    for dirpath, _, filenames in os.walk(os.path.join(scratch, rel)):
        for name in sorted(filenames):
            full = os.path.join(dirpath, name)
            key = os.path.relpath(full, scratch)
            produced.add(key)
            if key in checked:
                continue
            checked.add(key)
            committed = os.path.join(root, key)
            if not os.path.exists(committed):
                problems.append((key, ["  the generator produces this file, but it is not committed"]))
                continue
            new = open(full, encoding="utf-8").read().splitlines(keepends=True)
            old = open(committed, encoding="utf-8").read().splitlines(keepends=True)
            if new != old:
                diff = list(difflib.unified_diff(old, new, "committed", "regenerated", n=2))
                problems.append((key, ["  " + line.rstrip("\n") for line in diff[:40]]))

    for dirpath, _, filenames in os.walk(os.path.join(root, rel)):
        for name in sorted(filenames):
            if not is_generated_name(name):
                continue
            key = os.path.relpath(os.path.join(dirpath, name), root)
            if key not in produced and key not in checked:
                checked.add(key)
                problems.append((key, ["  no longer produced by the generator; it is stale"]))

    compared_per_out[rel] = len(produced)

empty = [rel for rel, n in compared_per_out.items() if n == 0]
if empty:
    print("The generator wrote no file into these output directories, so nothing under them was compared:")
    print("")
    for rel in empty:
        print(f"  {rel}")
    print("")
    print("An empty comparison reports no drift for the same reason an up-to-date one does.")
    print("Check that the out: entries in sql/sqlc.yaml still name directories sqlc emits into.")
    sys.exit(2)

if problems:
    print("The generated data layer no longer matches the schema it comes from.")
    print("")
    for key, lines in problems:
        print(f"---- {key} ----")
        print("\n".join(lines))
    sys.exit(1)
'

# ---------------------------------------------------------------------------
# Self-verification. Runs before the scan, every time.
#
# The comparison is the only thing standing between a stale generated tree
# and a green run, and its silent failure mode looks exactly like success.
# So it is first pointed at sample trees whose answer is known.
# ---------------------------------------------------------------------------

CONTROL_DIR="$(mktemp -d)"
trap 'rm -rf "$CONTROL_DIR"' EXIT

control_root="$CONTROL_DIR/root/internal/db/generated"
control_scratch="$CONTROL_DIR/scratch/internal/db/generated"
mkdir -p "$control_root" "$control_scratch"

control_failures=""
control_count=0
fail_case() {
  control_failures="${control_failures}  $1"$'\n'
  control_count=$((control_count + 1))
}

# Runs the comparison exactly as the real one below runs it, and checks the
# verdict rather than the wording.
expect_compare() {
  local expected="$1" description="$2" status=0
  python3 -c "$COMPARE_PY" "$CONTROL_DIR/root" "$CONTROL_DIR/scratch" \
    internal/db/generated >/dev/null 2>&1 || status=$?
  if [[ "$status" -ne "$expected" ]]; then
    fail_case "$description (expected exit $expected, got $status)"
  fi
}

printf 'package generated\n\ntype Task struct {\n\tID uint32\n}\n' >"$control_root/models.go"
cp "$control_root/models.go" "$control_scratch/models.go"
expect_compare 0 "a generated file identical to the committed one must not be reported as drift"

printf 'package generated\n\ntype Task struct {\n\tID uint32\n\tTitle string\n}\n' >"$control_scratch/models.go"
expect_compare 1 "a committed file missing a field the generator emits must be reported"

cp "$control_scratch/models.go" "$control_root/models.go"
printf 'package generated\n' >"$control_root/retired.sql.go"
expect_compare 1 "a committed file the generator no longer produces must be reported"

rm -f "$control_root/retired.sql.go" "$control_scratch/models.go"
expect_compare 2 "an output directory the generator wrote nothing into must not read as agreement"

if [[ $control_count -gt 0 ]]; then
  echo "check-codegen-drift: $control_count self-verification case(s) failed, so nothing was compared:" >&2
  printf '%s' "$control_failures" >&2
  echo "" >&2
  echo "The comparison itself is wrong. Fix it before trusting anything this" >&2
  echo "check says about the generated tree." >&2
  exit 2
fi

rm -rf "$CONTROL_DIR"
trap - EXIT

# ── the composed schema matches the layered sources ──

bash "$ROOT_DIR/scripts/schema-diff.sh"

# ── the generated Go matches the schema and queries ──

# A comparison across versions would report drift that is not there, so
# this must hold before the scratch generation means anything.
assert_sqlc_version

SCRATCH="$(mktemp -d)"
# The config lives beside the schema and queries it names by relative path,
# so the copy has to stay in the same directory for those to resolve.
SCRATCH_CONFIG="$(mktemp "$ROOT_DIR/sql/.sqlc-drift-XXXXXX.yaml")"
trap 'rm -rf "$SCRATCH" "$SCRATCH_CONFIG"' EXIT

# Redirect every output directory into the scratch tree, keeping the same
# relative layout so nested outputs stay nested and a recursive diff lines up.
outs="$(python3 - "$SQLC_CONFIG" "$SCRATCH_CONFIG" "$SCRATCH" "$ROOT_DIR" <<'PY'
import os, re, sys

config_path, scratch_config, scratch, root = sys.argv[1:5]
sql_dir = os.path.dirname(config_path)

rewritten = []
outs = []


def repoint(match):
    prefix, quote, value = match.group(1), match.group(2), match.group(3)
    rel = os.path.relpath(os.path.normpath(os.path.join(sql_dir, value)), root)
    outs.append(rel)
    # sqlc joins out: onto the config's directory, so an absolute path would
    # land under sql/ rather than at the root of the filesystem. Express the
    # scratch destination as a path relative to that directory instead.
    dest = os.path.relpath(os.path.join(scratch, rel), sql_dir)
    return f"{prefix}{quote}{dest}{quote}"


with open(config_path) as fh:
    for line in fh:
        rewritten.append(re.sub(r'^(\s*out:\s*)(["\']?)(.*?)\2\s*$', repoint, line.rstrip("\n")) + "\n")

if not outs:
    sys.stderr.write("no out: entries found in the sqlc config\n")
    sys.exit(2)

with open(scratch_config, "w") as fh:
    fh.writelines(rewritten)

print("\n".join(outs))
PY
)"

sqlc generate -f "$SCRATCH_CONFIG" >/dev/null

# The out: entries carry no whitespace, so splitting the newline-separated
# list on IFS is enough to hand them over as separate arguments.
# shellcheck disable=SC2206
out_dirs=($outs)

compare_status=0
python3 -c "$COMPARE_PY" "$ROOT_DIR" "$SCRATCH" "${out_dirs[@]}" || compare_status=$?

if [[ $compare_status -eq 2 ]]; then
  exit 2
fi

if [[ $compare_status -ne 0 ]]; then
  echo ""
  echo "Run the generator and commit its output alongside the schema change:"
  echo "  make gen-sqlc"
  exit 1
fi

echo "generated data layer matches the schema and queries"
