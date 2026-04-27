package timeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// timelineRow is the in-handler scan target. It mirrors the columns
// produced by the dynamic SELECT below; keeping it private to this
// package avoids leaking generated.* row types into the mapper layer.
type timelineRow struct {
	publicID         types.PublicID
	taskPublicID     []byte
	actorPublicID    []byte
	actorDisplayName sql.NullString
	eventType        string
	payload          json.RawMessage
	occurredAt       time.Time
	total            interface{}
}

// queryTimeline runs a dynamic SELECT against v_task_timeline and
// returns the materialised rows plus the COUNT(*) OVER() total. baseWhere
// holds scope predicates that are always applied (e.g. workspace_id);
// baseArgs holds the matching args. The kind / actor filters are layered
// on top.
func queryTimeline(
	ctx context.Context,
	db *sql.DB,
	baseWhere []string,
	baseArgs []any,
	kinds []string,
	actorPublicID []byte,
	callerUserID uint32,
	limit, offset int32,
) ([]timelineRow, int64, error) {
	where := append([]string{}, baseWhere...)
	args := append([]any{}, baseArgs...)

	// 4-layer ACL visibility predicate (defense-in-depth). An event is
	// visible to the caller if any of the following hold:
	//   - the event has no associated task (workspace-root event), OR
	//   - the associated task has no project (workspace-scoped task and
	//     the caller is already guaranteed a workspace member by the
	//     route middleware), OR
	//   - the associated task's project is one the caller is a project
	//     member of.
	// This mirrors the per-task ACL enforced by RequireTaskAccess so the
	// workspace timeline cannot leak task titles/payloads for projects
	// the caller cannot otherwise access.
	// Workspace owner/admin see everything in their workspace (escape
	// hatch); other members only see events for projects they belong to
	// or workspace-root / non-task events.
	where = append(where, `(
  v.task_public_id IS NULL
  OR v.project_public_id IS NULL
  OR EXISTS (
    SELECT 1 FROM workspace_members wm
    WHERE wm.user_id = ?
      AND wm.workspace_id = v.workspace_id
      AND wm.role IN ('owner','admin')
      AND wm.enabled = TRUE
  )
  OR v.project_public_id IN (
    SELECT p.public_id
    FROM project_members pm
    INNER JOIN projects p ON p.id = pm.project_id AND p.enabled = TRUE
    WHERE pm.user_id = ? AND pm.enabled = TRUE
  )
)`)
	args = append(args, callerUserID, callerUserID)

	if len(kinds) > 0 {
		placeholders := make([]string, 0, len(kinds))
		for _, k := range kinds {
			placeholders = append(placeholders, "?")
			args = append(args, k)
		}
		where = append(where, "v.type IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(actorPublicID) > 0 {
		where = append(where, "v.actor_user_public_id = ?")
		args = append(args, actorPublicID)
	}

	q := fmt.Sprintf(`SELECT
  v.public_id,
  v.task_public_id,
  v.actor_user_public_id,
  v.actor_display_name,
  v.type,
  v.payload_json,
  v.occurred_at,
  COUNT(*) OVER() AS total
FROM v_task_timeline v
WHERE %s
ORDER BY v.occurred_at DESC, v.event_id DESC
LIMIT ? OFFSET ?`, strings.Join(where, " AND "))
	args = append(args, limit, offset)

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var (
		out   []timelineRow
		total int64
	)
	for rows.Next() {
		var r timelineRow
		if err := rows.Scan(
			&r.publicID,
			&r.taskPublicID,
			&r.actorPublicID,
			&r.actorDisplayName,
			&r.eventType,
			&r.payload,
			&r.occurredAt,
			&r.total,
		); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(out) > 0 {
		total = totalAsInt64(out[0].total)
	}
	return out, total, nil
}

// toDTO converts a timelineRow to the public Event DTO. This is
// the only place that crosses the time / public-id boundary.
func toDTO(r timelineRow) Event {
	return Event{
		ID:               r.publicID.UUID().String(),
		Type:             r.eventType,
		TaskID:           uuidFromBytes(r.taskPublicID),
		ActorUserID:      uuidFromBytes(r.actorPublicID),
		ActorDisplayName: nullStr(r.actorDisplayName),
		Payload:          r.payload,
		OccurredAt:       r.occurredAt.Unix(),
	}
}

// resolveActorFilter parses the optional actor query parameter into the
// raw 16-byte UUID slice expected by the v_task_timeline JOIN. An empty
// string yields a nil slice (no filter applied); a malformed UUID is
// returned as a sentinel error so callers can map it to a 404-style
// "no events" response without leaking presence information.
func resolveActorFilter(actor string) ([]byte, error) {
	if actor == "" {
		return nil, nil
	}
	u, err := uuid.Parse(actor)
	if err != nil {
		return nil, errInvalidActor
	}
	b, _ := u.MarshalBinary()
	return b, nil
}

// errInvalidActor is the sentinel returned by [resolveActorFilter] for
// malformed UUIDs. Handlers translate it to an empty result set.
var errInvalidActor = fmt.Errorf("timeline: invalid actor uuid")

// ListForTask handles GET /tasks/{id}/timeline. The route must be mounted
// behind RequireTaskAccess so the workspace and task contexts are populated.
func ListForTask(deps Deps) func(context.Context, *ListTimelineForTaskInput) (*ListTimelineOutput, error) {
	return func(ctx context.Context, in *ListTimelineForTaskInput) (*ListTimelineOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		actorPub, err := resolveActorFilter(in.Actor)
		if err != nil {
			return emptyOutput(), nil
		}
		taskPubBytes, _ := task.PublicID.MarshalBinary()
		userID, _ := middleware.ActorFromContext(ctx)
		rows, total, err := queryTimeline(ctx, deps.DB,
			[]string{"v.workspace_id = ?", "v.task_public_id = ?"},
			[]any{ws.ID, taskPubBytes},
			in.Kind, actorPub, userID, limit, in.Offset)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return buildOutput(rows, total), nil
	}
}

// ListForProject handles GET /projects/{prjId}/timeline. The route must
// be mounted behind RequireProjectMemberByGlobalId so the workspace and
// project contexts are populated.
func ListForProject(deps Deps) func(context.Context, *ListTimelineForProjectInput) (*ListTimelineOutput, error) {
	return func(ctx context.Context, in *ListTimelineForProjectInput) (*ListTimelineOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		prj, ok := middleware.ProjectFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		actorPub, err := resolveActorFilter(in.Actor)
		if err != nil {
			return emptyOutput(), nil
		}
		prjPubBytes, _ := prj.PublicID.MarshalBinary()
		userID, _ := middleware.ActorFromContext(ctx)
		rows, total, err := queryTimeline(ctx, deps.DB,
			[]string{"v.workspace_id = ?", "v.project_public_id = ?"},
			[]any{ws.ID, prjPubBytes},
			in.Kind, actorPub, userID, limit, in.Offset)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return buildOutput(rows, total), nil
	}
}

// ListForWorkspace handles GET /workspaces/{wsId}/timeline. The route
// must be mounted behind RequireWorkspaceMember.
func ListForWorkspace(deps Deps) func(context.Context, *ListTimelineForWorkspaceInput) (*ListTimelineOutput, error) {
	return func(ctx context.Context, in *ListTimelineForWorkspaceInput) (*ListTimelineOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		actorPub, err := resolveActorFilter(in.Actor)
		if err != nil {
			return emptyOutput(), nil
		}
		userID, _ := middleware.ActorFromContext(ctx)
		rows, total, err := queryTimeline(ctx, deps.DB,
			[]string{"v.workspace_id = ?"},
			[]any{ws.ID},
			in.Kind, actorPub, userID, limit, in.Offset)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return buildOutput(rows, total), nil
	}
}

// buildOutput materialises the response body shared by all three
// handlers. It always returns a non-nil events slice so the JSON shape
// is stable for clients (`events: []` instead of `events: null`).
func buildOutput(rows []timelineRow, total int64) *ListTimelineOutput {
	out := &ListTimelineOutput{}
	out.Body.Events = make([]Event, 0, len(rows))
	for _, r := range rows {
		out.Body.Events = append(out.Body.Events, toDTO(r))
	}
	out.Body.Total = total
	return out
}

// emptyOutput returns a successful but empty response. It is used when
// a malformed filter would otherwise leak the existence of unrelated
// rows; we deliberately return 200 with an empty page rather than 400.
func emptyOutput() *ListTimelineOutput {
	out := &ListTimelineOutput{}
	out.Body.Events = []Event{}
	return out
}
