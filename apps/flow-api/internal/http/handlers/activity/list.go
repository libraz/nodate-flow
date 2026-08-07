package activity

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// activityColumns is the projection shared by the list query and the
// scan below. It matches generated.ListWorkspaceActivityRow field for
// field so the sqlc row type — and with it the mapper — keeps working
// unchanged.
const activityColumns = `va.public_id,
  va.source,
  va.source_table,
  va.occurred_at,
  va.actor_user_public_id,
  va.actor_kind,
  va.action,
  va.resource_type,
  va.resource_public_id,
  va.severity`

// visibilityPredicate returns the WHERE fragment restricting the feed to
// entries the caller is allowed to observe, plus its bind arguments. An
// empty fragment means "no restriction".
//
// v_workspace_activity unions three audit trails, and the workspace-wide
// one — audit_logs — has its own read endpoint behind a workspace-admin
// gate. That gate is about what the audit endpoint adds (client IP,
// user-agent, the raw metadata blob), none of which this feed projects;
// it is not a reason to hide the activity feed from members, who reach
// it from the workspace nav. What it is a reason for is not letting the
// feed become a way to read the administration trail, or the trail of
// tasks the caller may not open, without that gate.
//
// So for a non-elevated caller the feed narrows to two things they could
// observe anyway: what they did themselves, and what happened to a task
// they can see. Rows with no user actor (system entries) and rows about
// workspace administration fall outside both and stay with the admin
// endpoint. Elevated callers — the audit endpoint's own audience — see
// everything.
//
// The task half reuses acl.TaskVisibilityFilter through the middleware
// alias rather than restating the rule: the predicate is already written
// twice in this repository (once as this fragment, once inside the sqlc
// list queries), and a third copy is how the copies start disagreeing.
func visibilityPredicate(actorPublicID types.PublicID, actorID uint32, wsRole middleware.WorkspaceRole) (string, []any) {
	visFrag, visArgs := middleware.TaskVisibilityFilter(actorID, wsRole)
	if visFrag == "" {
		// Workspace admins and owners read the whole trail.
		return "", nil
	}

	frag := `(
    va.actor_user_public_id = ?
    OR (
      va.resource_type = 'task'
      AND EXISTS (
        SELECT 1 FROM v_task_list v
        WHERE v.workspace_id = va.workspace_id
          AND v.public_id = va.resource_public_id
          AND ` + visFrag + `
      )
    )
  )`
	args := append([]any{actorPublicID}, visArgs...)
	return frag, args
}

// List handles GET /workspaces/{wsId}/activity. It returns a cursor-paginated
// page of the unified workspace activity feed (audit + AI + MCP), newest first.
//
// Access control has two layers. The surrounding chi group layers
// RequireAuth + RequireWorkspaceMember before this handler runs, so the
// workspace id from context is trusted and the workspace_id column is
// always part of the WHERE clause: a caller authorised for workspace A
// can never observe workspace B's activity. Within the workspace,
// [visibilityPredicate] narrows the rows a non-admin may observe.
//
// Pagination follows the keyset idiom used elsewhere (see tasks.ListComments):
// the query asks for limit+1 rows; when the surplus row is present a
// nextCursor is encoded from the last in-page row's (occurred_at, public_id)
// tuple. total comes from a separate COUNT over the same filters.
func List(deps Deps) func(context.Context, *ListActivityInput) (*ListActivityOutput, error) {
	return func(ctx context.Context, in *ListActivityInput) (*ListActivityOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			// RequireWorkspaceMember always injects the workspace before this
			// handler is reached; a missing context indicates a routing
			// misconfiguration, not a missing resource.
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		cursorAt, cursorPID, derr := handlerutil.DecodeCursor(in.Cursor)
		if derr != nil {
			return nil, httpErr(apierrors.ValidationQueryFieldInvalid)
		}

		// The caller's own public_id is compared against the view's
		// actor column, which carries public ids only; resolving it once
		// here keeps the comparison out of the per-row path.
		actorPublicID, err := deps.Queries.FindUserPublicIdById(ctx, actorID)
		if err != nil {
			slog.ErrorContext(ctx, "activity actor lookup failed", slog.String("err", err.Error()), logutil.LogEntity("workspace", ws.PublicID))
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		where := []string{"va.workspace_id = ?"}
		args := []any{ws.ID}
		if in.Source != "" {
			where = append(where, "va.source = ?")
			args = append(args, in.Source)
		}
		if visFrag, visArgs := visibilityPredicate(actorPublicID, actorID, ws.Role); visFrag != "" {
			where = append(where, visFrag)
			args = append(args, visArgs...)
		}

		out := &ListActivityOutput{}
		out.Body.Activity = []Entry{}

		total, err := countActivity(ctx, deps.DB, where, args)
		if err != nil {
			slog.ErrorContext(ctx, "activity count query failed", slog.String("err", err.Error()), logutil.LogEntity("workspace", ws.PublicID))
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out.Body.Total = total

		rows, err := listActivity(ctx, deps.DB, where, args, cursorAt, cursorPID, limit+1)
		if err != nil {
			slog.ErrorContext(ctx, "activity list query failed", slog.String("err", err.Error()), logutil.LogEntity("workspace", ws.PublicID))
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		hasMore := int32(len(rows)) > limit //#nosec G115 -- rows length capped at limit+1 with limit validated to maximum:200
		if hasMore {
			rows = rows[:limit]
		}
		out.Body.Activity = make([]Entry, 0, len(rows))
		for _, r := range rows {
			out.Body.Activity = append(out.Body.Activity, mapRow(r))
		}
		if hasMore {
			last := rows[len(rows)-1]
			nc := handlerutil.EncodeCursor(last.OccurredAt, last.PublicID)
			out.Body.NextCursor = &nc
		}
		return out, nil
	}
}

// countActivity returns the number of rows matching the filters, before
// pagination. It is a separate statement rather than a COUNT(*) OVER()
// because the cursor predicate belongs to the page and not to the total.
func countActivity(ctx context.Context, db *sql.DB, where []string, args []any) (int64, error) {
	//#nosec G201 -- WHERE fragments are static literals composed in this file; all caller-supplied values are bound via placeholders.
	q := fmt.Sprintf(`SELECT COUNT(*) FROM v_workspace_activity va WHERE %s`, strings.Join(where, " AND "))
	var total int64
	if err := db.QueryRowContext(ctx, q, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

// listActivity runs the keyset page query. The cursor restricts the page
// to rows strictly before (occurred_at, public_id) in the feed's
// descending order; a zero cursor time means "first page".
func listActivity(
	ctx context.Context,
	db *sql.DB,
	where []string,
	args []any,
	cursorAt time.Time,
	cursorPID types.PublicID,
	limit int32,
) ([]generated.ListWorkspaceActivityRow, error) {
	where = append([]string{}, where...)
	args = append([]any{}, args...)
	if !cursorAt.IsZero() {
		where = append(where, "(va.occurred_at < ? OR (va.occurred_at = ? AND va.public_id < ?))")
		args = append(args, cursorAt, cursorAt, cursorPID)
	}

	//#nosec G201 -- WHERE fragments are static literals composed in this file; all caller-supplied values are bound via placeholders.
	q := fmt.Sprintf(`SELECT
  %s
FROM v_workspace_activity va
WHERE %s
ORDER BY va.occurred_at DESC, va.public_id DESC
LIMIT ?`, activityColumns, strings.Join(where, " AND "))
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []generated.ListWorkspaceActivityRow{}
	for rows.Next() {
		var r generated.ListWorkspaceActivityRow
		if err := rows.Scan(
			&r.PublicID,
			&r.Source,
			&r.SourceTable,
			&r.OccurredAt,
			&r.ActorUserPublicID,
			&r.ActorKind,
			&r.Action,
			&r.ResourceType,
			&r.ResourcePublicID,
			&r.Severity,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
