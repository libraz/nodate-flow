// Package signaljudge — SQL-backed adapters for the three
// [PromptDeps] lookups the runner uses to build a judge's per-run
// context window. These bind [RecentTasksLookup],
// [LinkedTasksLookup], and [JudgeInstructionsLookup] to the production
// database via narrow SELECTs against the live schema.
//
// The adapters intentionally avoid going through the sqlc-generated
// surface for the same reason the sibling adapters in
// signal_updater_sql.go do: each lookup is a single small SELECT and
// the matching sqlc queries either project a much wider row than the
// [TaskSummary] shape needs (ListTasksForWorkspace) or do not exist
// (calendar-event linked tasks). Raw SQL keeps the audit surface
// minimal and mirrors the inline test adapters in
// apps/flow-api/tests/signaljudge/prompt_render_test.go so the
// integration test continues to exercise the same SQL contract.
//
// Internal ids stay internal. The [TaskSummary] rows these adapters
// produce expose only public_id, title, derived_state, and due_on;
// the joins against internal id columns happen entirely server-side
// and never escape the SQL boundary.
package signaljudge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SQLRecentTasksLookup loads the most recent tasks for a workspace.
// Implements [RecentTasksLookup] against the live `tasks` table.
//
// Ordering is pinned to `created_at DESC, id DESC` so the rendered
// judge prompt is byte-stable across re-runs of the same signal:
// the prompt builder later resorts by public_id when emitting the
// markdown list, but the SELECT order determines which subset
// survives the LIMIT cap when the workspace has more tasks than
// [MaxRecentTasks].
type SQLRecentTasksLookup struct {
	// DB is the workspace-scoped MySQL handle. Required.
	DB *sql.DB
}

// LoadRecent implements [RecentTasksLookup]. Returns at most `limit`
// enabled tasks for the workspace, newest first. A workspace with no
// tasks returns an empty slice and nil error.
//
// `due_on` is rendered as a YYYY-MM-DD string when present; NULL
// produces an empty [TaskSummary.DueOn]. Other columns are projected
// verbatim from the row.
func (l *SQLRecentTasksLookup) LoadRecent(ctx context.Context, workspaceID uint32, limit int) ([]TaskSummary, error) {
	if l == nil || l.DB == nil {
		return nil, nil
	}
	if limit <= 0 {
		return nil, nil
	}
	const q = `SELECT BIN_TO_UUID(public_id, 0), title, derived_state, due_on
		FROM tasks
		WHERE workspace_id = ? AND enabled = TRUE
		ORDER BY created_at DESC, id DESC
		LIMIT ?`
	rows, err := l.DB.QueryContext(ctx, q, workspaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("signaljudge: load recent tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []TaskSummary{}
	for rows.Next() {
		var pub, title, state string
		var due sql.NullTime
		if err := rows.Scan(&pub, &title, &state, &due); err != nil {
			return nil, fmt.Errorf("signaljudge: scan recent task: %w", err)
		}
		ts := TaskSummary{
			PublicID:     pub,
			Title:        title,
			DerivedState: state,
		}
		if due.Valid {
			ts.DueOn = due.Time.UTC().Format("2006-01-02")
		}
		out = append(out, ts)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("signaljudge: iterate recent tasks: %w", err)
	}
	return out, nil
}

// SQLLinkedTasksLookup loads the tasks attached to a calendar event
// subject. Implements [LinkedTasksLookup] against the live schema.
//
// A task can link to an event through two distinct shapes:
//
//  1. The 1:1 projection link via `calendar_events.task_id` (used when
//     the event is the time-blocked or due-date projection of a single
//     task; see `calendar_events.task_role`).
//  2. The M:N umbrella link via `task_event_links` (relation =
//     contributes_to / blocks / depends_on / prep_for).
//
// The query UNIONs both paths so the judge sees the complete set of
// tasks the operator has associated with the event. UNION (not UNION
// ALL) collapses the rare case where the same task is reachable
// through both paths into a single row.
//
// Workspace scoping is enforced on every joined table as a
// defence-in-depth check; without it a misuse of the lookup that
// passed a foreign workspace's event id could surface another
// tenant's tasks.
type SQLLinkedTasksLookup struct {
	// DB is the workspace-scoped MySQL handle. Required.
	DB *sql.DB
}

// LoadLinked implements [LinkedTasksLookup]. Returns at most `limit`
// enabled tasks linked to the calendar event, newest first. The
// `eventInternalID` parameter is the value of `signals.subject_id`
// when `signals.subject_type = 'calendar_event'`. Returns an empty
// slice and nil error when the event has no linked tasks or the
// event id does not exist in the workspace.
//
// Internal ids (`t.id`, `ce.id`, `tel.id`) are NEVER projected —
// only the audit-safe [TaskSummary] fields the prompt builder needs
// surface to the caller.
func (l *SQLLinkedTasksLookup) LoadLinked(ctx context.Context, workspaceID uint32, eventInternalID int32, limit int) ([]TaskSummary, error) {
	if l == nil || l.DB == nil {
		return nil, nil
	}
	if limit <= 0 {
		return nil, nil
	}
	const q = `
		SELECT public_id_str, title, derived_state, due_on FROM (
			SELECT
				BIN_TO_UUID(t.public_id, 0) AS public_id_str,
				t.title                     AS title,
				t.derived_state             AS derived_state,
				t.due_on                    AS due_on,
				t.created_at                AS created_at,
				t.id                        AS task_id
			FROM tasks t
			INNER JOIN task_event_links tel
				ON tel.task_id = t.id
				AND tel.enabled = TRUE
				AND tel.workspace_id = ?
				AND tel.event_id = ?
			WHERE t.enabled = TRUE
				AND t.workspace_id = ?
			UNION
			SELECT
				BIN_TO_UUID(t.public_id, 0) AS public_id_str,
				t.title                     AS title,
				t.derived_state             AS derived_state,
				t.due_on                    AS due_on,
				t.created_at                AS created_at,
				t.id                        AS task_id
			FROM tasks t
			INNER JOIN calendar_events ce
				ON ce.task_id = t.id
				AND ce.enabled = TRUE
				AND ce.workspace_id = ?
				AND ce.id = ?
			WHERE t.enabled = TRUE
				AND t.workspace_id = ?
		) AS linked
		ORDER BY created_at DESC, task_id DESC
		LIMIT ?`
	rows, err := l.DB.QueryContext(ctx, q,
		workspaceID, eventInternalID, workspaceID,
		workspaceID, eventInternalID, workspaceID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("signaljudge: load linked tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []TaskSummary{}
	for rows.Next() {
		var pub, title, state string
		var due sql.NullTime
		if err := rows.Scan(&pub, &title, &state, &due); err != nil {
			return nil, fmt.Errorf("signaljudge: scan linked task: %w", err)
		}
		ts := TaskSummary{
			PublicID:     pub,
			Title:        title,
			DerivedState: state,
		}
		if due.Valid {
			ts.DueOn = due.Time.UTC().Format("2006-01-02")
		}
		out = append(out, ts)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("signaljudge: iterate linked tasks: %w", err)
	}
	return out, nil
}

// SQLJudgeInstructionsLookup reads the per-workspace
// `ai_settings.judge_instructions` text. Implements
// [JudgeInstructionsLookup] via a single SELECT against the
// `ai_settings` table.
//
// Three result shapes are treated as "no per-workspace policy" and
// surface to the caller as an empty string with nil error:
//
//   - The workspace has never written an `ai_settings` row
//     (sql.ErrNoRows).
//   - The `judge_instructions` column is NULL.
//   - The column is set to the empty string.
//
// The prompt builder then omits the entire "Judge instructions"
// section from the rendered prompt rather than emitting a header
// with no body.
type SQLJudgeInstructionsLookup struct {
	// DB is the workspace-scoped MySQL handle. Required.
	DB *sql.DB
}

// LoadInstructions implements [JudgeInstructionsLookup].
func (l *SQLJudgeInstructionsLookup) LoadInstructions(ctx context.Context, workspaceID uint32) (string, error) {
	if l == nil || l.DB == nil {
		return "", nil
	}
	const q = `SELECT judge_instructions FROM ai_settings WHERE workspace_id = ? LIMIT 1`
	var raw sql.NullString
	if err := l.DB.QueryRowContext(ctx, q, workspaceID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("signaljudge: load judge instructions: %w", err)
	}
	if !raw.Valid {
		return "", nil
	}
	return raw.String, nil
}
