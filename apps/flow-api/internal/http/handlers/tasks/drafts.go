// Package tasks — handler for the retro draft queue endpoint.
//
// GET /workspaces/{wsId}/tasks/drafts?reason=retro returns the workspace's
// draft retrospective tasks: tasks linked back to an original task via a
// task_dependencies row of kind='retro_of'. The signal_judge Applier
// materialises these rows when it resolves a verdict with
// action=generate_retro (see apps/flow-api/internal/ai/signaljudge/applier.go).
//
// Authorisation: standard workspace-member auth, mounted under the
// `RequireWorkspaceMember` chi group in router.go alongside the rest of
// the workspace-scoped task routes. Service tokens are not granted access
// — only humans browse the queue.
package tasks

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
)

// ListRetroDrafts returns the handler for
// GET /workspaces/{wsId}/tasks/drafts?reason=retro.
//
// The Huma operation guarantees `reason=retro` at the boundary via the
// `enum:"retro"` struct tag on [ListRetroDraftsInput.Reason]; anything
// else 422s before the handler runs. Pagination is offset-based with a
// default of 20 rows and a hard cap of 50.
//
// The response shape carries `total` (filter-applied count from the SQL
// window function) plus a `drafts` array. The optional agent fields
// (`createdByAgentId` / `createdByAgentName`) are best-effort: they come
// from the `task.retro.drafted` event's actor_agent_id, resolved for the
// whole page at once by [Queries.FindRetroDraftAgents]. Rows where the
// event has been retention-swept simply omit the agent fields.
//
// The `signal` enrichment block from the original design is deliberately left
// out of this iteration (see the doc comment on [RetroDraft]).
func ListRetroDrafts(deps Deps) func(context.Context, *ListRetroDraftsInput) (*ListRetroDraftsOutput, error) {
	return func(ctx context.Context, in *ListRetroDraftsInput) (*ListRetroDraftsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		// The route is open to every workspace member and each row
		// carries the draft's title next to the source task's, so the
		// page is filtered to drafts whose two ends the actor may both
		// see.
		vis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole(ws.Role))
		return listRetroDrafts(ctx, deps.Queries, ws.ID, vis, in.Limit, in.Offset)
	}
}

// listRetroDrafts is the whole of the endpoint below the request: it
// reads the page and enriches it, and knows nothing about HTTP.
//
// It is separate from the operation above so the number of statements a
// page costs can be asserted directly, without a workspace context the
// middleware alone can build — see drafts_roundtrips_test.go. The count
// is the property that matters here and it is not visible from the
// response.
func listRetroDrafts(
	ctx context.Context, q *generated.Queries, workspaceID uint32, vis acl.VisibilityArgs, limit, offset int32,
) (*ListRetroDraftsOutput, error) {
	rows, err := q.ListRetroDraftsForWorkspace(ctx, generated.ListRetroDraftsForWorkspaceParams{
		WorkspaceID:   workspaceID,
		IsElevated:    vis.IsElevated,
		ActorUserID:   vis.ActorUserID,
		ActorUserID_2: vis.ActorUserID,
		ActorUserID_3: vis.ActorUserID,
		ActorUserID_4: vis.ActorUserID,
		ActorUserID_5: vis.ActorUserID,
		ActorUserID_6: vis.ActorUserID,
		Limit:         limit,
		Offset:        offset,
	})
	if err != nil {
		return nil, httpErr(apierrors.InternalUnexpected)
	}

	agents, err := retroDraftAgents(ctx, q, workspaceID, rows)
	if err != nil {
		return nil, err
	}

	out := &ListRetroDraftsOutput{}
	out.Body.Drafts = make([]RetroDraft, 0, len(rows))
	for _, r := range rows {
		draft := RetroDraft{
			TaskPublicID: r.TaskPublicID.String(),
			Title:        r.Title,
			Description:  nullStr(r.Description),
			CreatedAt:    r.CreatedAt.Unix(),
			SourceTask: RetroDraftSourceTask{
				PublicID: r.SourceTaskPublicID.String(),
				Title:    r.SourceTaskTitle,
			},
		}
		// A task with no drafted event (retention-swept, or seeded
		// outside the Applier) leaves the agent fields empty; they are
		// documented as optional in [RetroDraft].
		if a, found := agents[r.TaskID]; found {
			draft.CreatedByAgentID = a.AgentPublicID.String()
			draft.CreatedByAgentName = a.AgentName
		}
		out.Body.Drafts = append(out.Body.Drafts, draft)
	}

	if len(rows) > 0 {
		out.Body.Total = totalAsInt64(rows[0].Total)
	}
	return out, nil
}

// retroDraftAgents resolves the agent attribution for a whole page of
// drafts in one statement, keyed by the internal task id the list query
// already returned.
//
// One statement for the page rather than one per row: the queue caps at
// fifty, so the per-row form billed a full page at fifty-one round trips
// and grew with the page size the caller chose — for two optional
// strings per row. An empty page issues nothing at all.
func retroDraftAgents(
	ctx context.Context, q *generated.Queries, workspaceID uint32,
	rows []generated.ListRetroDraftsForWorkspaceRow,
) (map[uint32]generated.FindRetroDraftAgentsRow, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	taskIDs := make([]sql.NullInt32, 0, len(rows))
	for _, r := range rows {
		taskIDs = append(taskIDs, sql.NullInt32{
			Int32: int32(r.TaskID), //#nosec G115 -- tasks.id is INT UNSIGNED, fits int32 within realistic deployments
			Valid: true,
		})
	}
	agentRows, err := q.FindRetroDraftAgents(ctx, generated.FindRetroDraftAgentsParams{
		WorkspaceID: workspaceID,
		TaskIds:     taskIDs,
	})
	if err != nil {
		return nil, httpErr(apierrors.InternalUnexpected)
	}
	byTask := make(map[uint32]generated.FindRetroDraftAgentsRow, len(agentRows))
	for _, a := range agentRows {
		if !a.TaskID.Valid {
			continue
		}
		byTask[uint32(a.TaskID.Int32)] = a //#nosec G115 -- tasks.id is INT UNSIGNED and was passed in as one
	}
	return byTask, nil
}
