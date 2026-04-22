// Package db hosts lightweight, hand-written database helpers that sit
// alongside the sqlc-generated code in internal/db/generated. It
// deliberately stays small — anything that can be expressed as a sqlc
// query belongs under sql/queries, not here.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"strings"
)

// expectedTables is the curated list of tables the time-api process
// touches through its sqlc-generated queries. It is derived from a scan
// of apps/time-api/internal/db/generated/*.sql.go and is intentionally
// hand-maintained so an out-of-date dev database surfaces a clear,
// actionable warning on boot rather than a generic 500 at request time.
//
// NOTE: Views (e.g. v_users, v_workspace_members) are excluded on
// purpose — the probe targets base tables only. If a new table is added
// to sql/tables/ and queried by time-api, append it here.
//
// TODO: unify this list by scanning sql/tables/*.sql at build time (for
// example via go:embed) once the set stabilises.
var expectedTables = []string{
	// Calendar domain — the primary reason this probe exists. The
	// calendar_event_invites table was the original offender (see
	// docs/bugs/2026-04-23-devops-calendar-event-invites-table-not-in-dev-db.md).
	"calendars",
	"calendar_events",
	"calendar_event_attachments",
	"calendar_event_attendees",
	"calendar_event_checklist_items",
	"calendar_event_comments",
	"calendar_event_invites",
	"calendar_memos",
	"calendar_public_shares",
	"calendar_public_share_events",
	"calendar_subscriptions",

	// Magic-link invite acceptance flow.
	"magic_link_tokens",

	// Shared identity / tenancy tables consumed by time-api through the
	// auth middleware and workspace resolver.
	"users",
	"identities",
	"sessions",
	"user_recovery_codes",
	"personal_access_tokens",
	"mcp_tokens",
	"workspaces",
	"workspace_members",
	"workspace_invites",
}

// VerifySchema probes INFORMATION_SCHEMA for the set of tables time-api
// expects to exist in the currently selected database. Any missing
// tables are logged as a single warning with a remediation hint so a
// developer whose MySQL volume predates a schema change sees an
// actionable message on boot rather than a generic 500 at request time.
//
// The probe is intentionally fail-open: it never aborts startup. A
// query failure (permissions, transient connectivity, etc.) is logged
// and ignored so production instances are not wedged by an
// observability check.
//
// Callers that want to skip the probe entirely in production can gate
// the call at the main.go level on their environment flag; this
// function has no opinion about that.
func VerifySchema(ctx context.Context, db *sql.DB, logger *slog.Logger) {
	if db == nil || logger == nil {
		return
	}

	tables := make([]string, len(expectedTables))
	copy(tables, expectedTables)
	sort.Strings(tables)

	// Build a single parameterised IN(...) clause. One round-trip, one
	// index scan on information_schema — cheap and idempotent.
	placeholders := make([]string, len(tables))
	args := make([]any, len(tables))
	for i, t := range tables {
		placeholders[i] = "?"
		args[i] = t
	}

	query := fmt.Sprintf(
		"SELECT TABLE_NAME FROM information_schema.tables "+
			"WHERE table_schema = DATABASE() AND table_name IN (%s)",
		strings.Join(placeholders, ","),
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		logger.Warn("schema probe query failed; continuing", "err", err)
		return
	}
	defer rows.Close()

	present := make(map[string]struct{}, len(tables))
	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			logger.Warn("schema probe row scan failed; continuing", "err", scanErr)
			return
		}
		present[name] = struct{}{}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		logger.Warn("schema probe row iteration failed; continuing", "err", rowsErr)
		return
	}

	var missing []string
	for _, t := range tables {
		if _, ok := present[t]; !ok {
			missing = append(missing, t)
		}
	}

	if len(missing) == 0 {
		logger.Info("schema probe ok", "tables_checked", len(tables))
		return
	}

	logger.Warn(
		"schema probe detected missing tables; your dev DB is out of date. "+
			"Run `make db-reset` to apply the current schema.",
		"missing", missing,
		"missing_count", len(missing),
		"tables_checked", len(tables),
	)
}
