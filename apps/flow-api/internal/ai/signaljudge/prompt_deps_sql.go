// Package signaljudge — SQL-backed adapters for the [PromptDeps]
// lookups the runner uses to build a judge's per-run context window.
// These bind [RecentTasksLookup], [LinkedTasksLookup],
// [JudgeInstructionsLookup], and [PromptDeps.WorkspaceNow] to the
// production database.
//
// The three content adapters intentionally avoid going through the
// sqlc-generated surface for the same reason the sibling adapters in
// signal_updater_sql.go do: each lookup is a single small SELECT and
// the matching sqlc queries either project a much wider row than the
// [TaskSummary] shape needs (ListTasksForWorkspace) or do not exist
// (calendar-event linked tasks). Raw SQL keeps the audit surface
// minimal.
//
// [NewSQLWorkspaceNow] is the one adapter here that reads through the
// generated queries instead, because that criterion does not hold for
// it: FindWorkspaceTimezoneCountryById projects exactly the columns a
// zone lookup reads, and it is already the statement the AI budget
// window resolves a workspace's local midnight from. A hand-written
// SELECT of workspaces.timezone would be a second definition of "the
// workspace's timezone" that can drift from the budget window's — over
// the `enabled = TRUE` bound the generated query carries, among other
// things — and the two would then disagree about which day a workspace
// is in, with each answer defensible on its own.
//
// The integration test in
// apps/flow-api/tests/signaljudge/prompt_render_test.go constructs these
// adapters and runs them, rather than restating their SQL in the test.
// A retyped copy of a statement is a second place the audience bound has
// to be kept, and a test asserting against the copy proves nothing about
// the statement a judge run executes.
//
// Internal ids stay internal. The [TaskSummary] rows these adapters
// produce expose only public_id, title, derived_state, and due_on;
// the joins against internal id columns happen entirely server-side
// and never escape the SQL boundary. The clock adapter takes the
// internal workspace id the same way and hands back a formatted
// timestamp, so nothing identifying leaves it either.
package signaljudge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// publicOnly is the audience bound both task lookups in this file carry,
// named once so the two cannot drift and so a test can hold them to it.
//
// A judge run has no actor. The prompt it builds is stored in
// ai_invocations.prompt_redacted, which is readable by every member of
// the workspace, so the only audience that is safe without an actor to
// compare against is the one every member already belongs to.
const publicOnly = `visibility = 'public'`

// recentTasksQuery is the statement behind
// [SQLRecentTasksLookup.LoadRecent].
const recentTasksQuery = `SELECT BIN_TO_UUID(public_id, 0), title, derived_state, due_on
	FROM tasks
	WHERE workspace_id = ? AND enabled = TRUE
	  AND ` + publicOnly + `
	ORDER BY created_at DESC, id DESC
	LIMIT ?`

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
//
// task-visibility: not-applicable — a judge run is started by a signal
// rather than by a person, so there is no actor to compare a task's
// audience against, and every title this returns is rendered into the
// prompt that lands in ai_invocations.prompt_redacted, which every member
// of the workspace can read. The reader is therefore the whole
// membership, and the stronger bound is the one that holds for all of
// them at once: only tasks whose audience is already the whole workspace
// take part. That is why the predicate is visibility = 'public' rather
// than acl.TaskVisibilityFilter, which against a zero actor would return
// nothing at all and leave every judge run with an empty context while
// reporting success
func (l *SQLRecentTasksLookup) LoadRecent(ctx context.Context, workspaceID uint32, limit int) ([]TaskSummary, error) {
	if l == nil || l.DB == nil {
		return nil, nil
	}
	if limit <= 0 {
		return nil, nil
	}
	rows, err := l.DB.QueryContext(ctx, recentTasksQuery, workspaceID, limit)
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

// linkedTasksQuery is the statement behind
// [SQLLinkedTasksLookup.LoadLinked]. Both branches of the UNION carry
// [publicOnly]: a task does not become visible to the workspace by being
// attached to one of its calendar events.
const linkedTasksQuery = `
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
			AND t.` + publicOnly + `
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
			AND t.` + publicOnly + `
	) AS linked
	ORDER BY created_at DESC, task_id DESC
	LIMIT ?`

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
//
// task-visibility: not-applicable — the rows this returns are quoted
// into a prompt every workspace member can read and the run has no
// actor, so both branches are bound to visibility = 'public' instead,
// which is the stronger rule and the only one that holds without an
// actor
func (l *SQLLinkedTasksLookup) LoadLinked(ctx context.Context, workspaceID uint32, eventInternalID int32, limit int) ([]TaskSummary, error) {
	if l == nil || l.DB == nil {
		return nil, nil
	}
	if limit <= 0 {
		return nil, nil
	}
	rows, err := l.DB.QueryContext(ctx, linkedTasksQuery,
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

// WorkspaceTimezoneLoader is the narrow surface [NewSQLWorkspaceNow]
// needs from the sqlc-generated Queries struct. *generated.Queries
// satisfies it; a test can supply a fake without a database.
type WorkspaceTimezoneLoader interface {
	FindWorkspaceTimezoneCountryById(ctx context.Context, id uint32) (generated.FindWorkspaceTimezoneCountryByIdRow, error)
}

// errNoWorkspaceTimezoneLoader is what a wired-but-empty clock reports.
// It is an error rather than an empty answer because a nil loader is a
// mis-wiring, and the caller only distinguishes "answered" from "did
// not"; see [NewSQLWorkspaceNow].
var errNoWorkspaceTimezoneLoader = errors.New("signaljudge: workspace clock has no timezone loader")

// NewSQLWorkspaceNow builds the [PromptDeps.WorkspaceNow] clock: the
// current instant read in the workspace's own IANA timezone, formatted
// the way the builder's own fallback formats it, so the two differ only
// in the offset they carry.
//
// That offset is the whole point. The judge reasons about "overdue" and
// "just happened" against this timestamp, and a workspace nine hours
// ahead of UTC judged against a UTC clock gets a coherent-looking answer
// to a question nobody asked. An operator reading
// ai_invocations.prompt_redacted afterwards can tell which frame of
// reference a verdict was formed in only if the offset is in the string.
//
// It returns a function rather than a struct with a method because
// [PromptDeps.WorkspaceNow] is a function field; the three lookups
// beside it are structs because their fields are interfaces.
//
// Every path either yields a timestamp or reports an error, and never
// the empty string with a nil error: [BuildPromptContext] reads an empty
// answer as the same failure as an error, so returning one for a case
// this function considers successful would record a gap that is not one.
// The failures it does report are a workspace row that could not be read
// (including a disabled or missing workspace, which the generated query
// answers with sql.ErrNoRows) and a stored zone name the zoneinfo
// database does not know. Neither is papered over with UTC here: the
// builder already has one named UTC fallback, and a second, silent one
// underneath it would make a workspace with a broken timezone column
// indistinguishable from one that chose UTC.
//
// A workspace that chose UTC is not a gap and gets a real timestamp; so
// does one whose column is empty, which [region.Resolve] answers with
// the same UTC default the schema states for the column.
func NewSQLWorkspaceNow(loader WorkspaceTimezoneLoader) func(ctx context.Context, workspaceID uint32) (string, error) {
	return func(ctx context.Context, workspaceID uint32) (string, error) {
		if loader == nil {
			return "", errNoWorkspaceTimezoneLoader
		}
		row, err := loader.FindWorkspaceTimezoneCountryById(ctx, workspaceID)
		if err != nil {
			return "", fmt.Errorf("signaljudge: load workspace timezone: %w", err)
		}
		zone, err := region.Resolve(row.Timezone)
		if err != nil {
			return "", fmt.Errorf("signaljudge: resolve workspace timezone %q: %w", row.Timezone, err)
		}
		return time.Now().In(zone.Location()).Format(time.RFC3339), nil
	}
}
