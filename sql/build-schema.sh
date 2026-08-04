#!/usr/bin/env bash
# build-schema.sh
# Concatenate the layered schema into a single stream.
# Pre-release: no migrations. Intended usage is drop & recreate.
#
#   bash sql/build-schema.sh | mysql -h 127.0.0.1 -u root -p nodate_flow
#   bash sql/build-schema.sh core | mysql ...   # core layer only
#
# Layers:
#
#   core/   tables shared with any other product implementing the contract:
#           workspaces, users/identities/sessions, calendars, calendar_events
#           and friends, and the append-only `events` log.
#   flow/   this product's own tables, views, triggers, plus the cross-layer
#           foreign keys that core cannot declare because their targets
#           (tasks, ai_agents, signals) live here.
#
# `core` emits a schema a calendar-only deployment can load on its own.
# `all` (the default) emits core followed by flow, which is what this
# repository runs.
#
# FK checks are toggled OFF at the top and ON at the bottom so tables can be
# loaded in plain alphabetical order regardless of dependency direction.
# Cross-layer constraints are emitted after every CREATE TABLE of both
# layers, so their targets always exist by the time they run.

set -euo pipefail

# Pin the collation so filename glob expansion is byte-ordered and identical
# across platforms. Without this, the default locale (e.g. UTF-8 on macOS vs C
# on Linux CI) reorders sibling view files, producing a schema.sql that differs
# between a local run and the CI drift guard.
export LC_ALL=C

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

LAYER_ARG="${1:-all}"
case "${LAYER_ARG}" in
  all)  LAYERS=(core flow) ;;
  core) LAYERS=(core) ;;
  *)
    echo "usage: build-schema.sh [all|core]" >&2
    exit 2
    ;;
esac

# ---------------------------------------------------------------------------
# Helpers. Each takes a directory and is a no-op when it does not exist, so a
# layer may omit any of tables/ views/ triggers/ constraints/.
# ---------------------------------------------------------------------------

# list_tables_merged prints "path" lines for every table file across the
# given layer directories, ordered by FILENAME rather than by layer.
#
# The merge is load-bearing, not cosmetic. InnoDB evaluates a DELETE's
# cascade chain in table creation order, and workspace teardown relies on
# `attachments` rows going away before the `storage_objects` rows they
# reference via fk_attachments_storage_object (ON DELETE RESTRICT).
# Emitting layer-by-layer would create storage_objects (core) before
# attachments (flow) and turn workspace deletion into a 1451 error. Sorting
# by filename reproduces the single-directory order this schema was built
# under. Workspace teardown should not depend on InnoDB cascade ordering at
# all; until it stops doing so, this ordering is a correctness requirement.
list_tables_merged() {
  local d f
  for d in "$@"; do
    [[ -d "${d}" ]] || continue
    for f in "${d}"/*.sql; do
      [[ -e "${f}" ]] || continue
      printf '%s\t%s\n' "$(basename "${f}")" "${f}"
    done
  done | sort -k1,1 | cut -f2-
}

emit_drop_tables_merged() {
  local f
  while IFS= read -r f; do
    [[ -n "${f}" ]] || continue
    echo "DROP TABLE IF EXISTS \`$(basename "${f}" .sql)\`;"
  done < <(list_tables_merged "$@")
}

emit_tables_merged() {
  local f
  while IFS= read -r f; do
    [[ -n "${f}" ]] || continue
    echo "-- >>> $(basename "${f}")"
    cat "${f}"
    echo
  done < <(list_tables_merged "$@")
}

emit_files() {
  local dir="$1"
  [[ -d "${dir}" ]] || return 0
  for f in "${dir}"/*.sql; do
    [[ -e "${f}" ]] || continue
    echo "-- >>> $(basename "${f}")"
    cat "${f}"
    echo
  done
}

emit_views() {
  local dir="$1"
  [[ -d "${dir}" ]] || return 0

  # Base views (suffix `_all.sql`) are loaded before their dependants so that
  # filtered child views (e.g. v_task_list, v_task_list_archived) can reference
  # them. Default glob ordering would otherwise put `v_task_list.sql` before
  # `v_task_list_all.sql` because '.' (0x2E) sorts before '_' (0x5F).
  local base_views=() leaf_views=() f
  for f in "${dir}"/*.sql; do
    [[ -e "${f}" ]] || continue
    if [[ "$(basename "${f}")" == *_all.sql ]]; then
      base_views+=("${f}")
    else
      leaf_views+=("${f}")
    fi
  done

  # Drop in reverse dependency order: leaves first, then bases.
  for f in "${leaf_views[@]:-}" "${base_views[@]:-}"; do
    [[ -n "${f:-}" && -e "${f}" ]] || continue
    echo "DROP VIEW IF EXISTS \`$(basename "${f}" .sql)\`;"
  done
  echo

  # Create in dependency order: bases first, then leaves.
  for f in "${base_views[@]:-}" "${leaf_views[@]:-}"; do
    [[ -n "${f:-}" && -e "${f}" ]] || continue
    echo "-- >>> $(basename "${f}")"
    cat "${f}"
    echo
  done
}

emit_triggers() {
  local dir="$1"
  [[ -d "${dir}" ]] || return 0

  # Triggers are loaded after their tables. Each file is expected to use
  # DELIMITER $$ ... DELIMITER ; internally so the mysql client can parse
  # multi-statement bodies (IF / SIGNAL / etc.). DROP TRIGGER IF EXISTS is
  # emitted up front to keep re-runs idempotent even if the parent table
  # was not dropped between runs.
  local f
  for f in "${dir}"/*.sql; do
    [[ -e "${f}" ]] || continue
    echo "DROP TRIGGER IF EXISTS \`$(basename "${f}" .sql)\`;"
  done
  echo
  emit_files "${dir}"
}

# ---------------------------------------------------------------------------
# Emit
# ---------------------------------------------------------------------------

echo "-- Generated by sql/build-schema.sh (layers: ${LAYERS[*]})"
echo "-- DO NOT EDIT the output; edit files under sql/core/ or sql/flow/."
echo "SET NAMES utf8mb4;"
echo "SET FOREIGN_KEY_CHECKS = 0;"
echo "SET UNIQUE_CHECKS = 0;"
echo

# Drop every table across all selected layers before creating any, so a
# re-run is idempotent even when a table moved between layers.
TABLE_DIRS=()
for layer in "${LAYERS[@]}"; do
  TABLE_DIRS+=("${SCRIPT_DIR}/${layer}/tables")
done

emit_drop_tables_merged "${TABLE_DIRS[@]}"
echo

emit_tables_merged "${TABLE_DIRS[@]}"

# Cross-layer foreign keys reference tables from more than one layer, so they
# run only after every layer's CREATE TABLE has been emitted.
for layer in "${LAYERS[@]}"; do
  emit_files "${SCRIPT_DIR}/${layer}/constraints"
done

for layer in "${LAYERS[@]}"; do
  emit_views "${SCRIPT_DIR}/${layer}/views"
done

for layer in "${LAYERS[@]}"; do
  emit_triggers "${SCRIPT_DIR}/${layer}/triggers"
done

echo "SET UNIQUE_CHECKS = 1;"
echo "SET FOREIGN_KEY_CHECKS = 1;"
