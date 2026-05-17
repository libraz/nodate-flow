// Package tasks — handler for the Phase 6 / L2 retro draft queue endpoint.
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
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
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
// (`createdByAgentId` / `createdByAgentName`) are best-effort: the
// handler resolves them per-row via [Queries.FindRetroDraftAgent], which
// looks up the `task.retro.drafted` event's actor_agent_id. Rows where
// the event has been retention-swept simply omit the agent fields.
//
// The `signal` enrichment block from the L2 design is deliberately left
// out of this iteration (see the doc comment on [RetroDraft]).
func ListRetroDrafts(deps Deps) func(context.Context, *ListRetroDraftsInput) (*ListRetroDraftsOutput, error) {
	return func(ctx context.Context, in *ListRetroDraftsInput) (*ListRetroDraftsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		rows, err := deps.Queries.ListRetroDraftsForWorkspace(ctx, generated.ListRetroDraftsForWorkspaceParams{
			WorkspaceID: ws.ID,
			Limit:       in.Limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
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

			// Best-effort agent attribution. The Applier emits a
			// task.retro.drafted event with actor_agent_id set; we
			// resolve it to the agent's public_id + name here. Rows
			// where the event is missing (retention-swept or seeded
			// outside the Applier) leave the agent fields empty —
			// they are documented as optional in [RetroDraft].
			agentRow, aerr := deps.Queries.FindRetroDraftAgent(ctx, generated.FindRetroDraftAgentParams{
				WorkspaceID: ws.ID,
				TaskID: sql.NullInt32{
					Int32: int32(r.TaskID), //#nosec G115 -- tasks.id is INT UNSIGNED, fits int32 within realistic deployments
					Valid: true,
				},
			})
			switch {
			case aerr == nil:
				draft.CreatedByAgentID = agentRow.AgentPublicID.String()
				draft.CreatedByAgentName = agentRow.AgentName
			case errors.Is(aerr, sql.ErrNoRows):
				// Optional fields stay empty.
			default:
				return nil, httpErr(apierrors.InternalUnexpected)
			}

			out.Body.Drafts = append(out.Body.Drafts, draft)
		}

		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}

		return out, nil
	}
}
