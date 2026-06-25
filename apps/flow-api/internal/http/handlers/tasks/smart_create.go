package tasks

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	nflog "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/log"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/logutil"
)

// SmartCreateDeps is the dependency bundle for the propose-smart and
// apply-smart endpoints. It is separate from the main task Deps because
// these endpoints require an AI orchestrator and embedder that the
// regular CRUD routes do not need.
type SmartCreateDeps struct {
	DB       *sql.DB
	Queries  *generated.Queries
	AI       *ai.Orchestrator
	Embedder *embed.Client
	Audit    *audit.Recorder
}

// ---- Propose Smart I/O ---------------------------------------------------

// ProposeSmartInput is the request for POST /workspaces/{wsId}/tasks/propose-smart.
type ProposeSmartInput struct {
	WsID string `path:"wsId"`
	Body struct {
		ProjectID   string `json:"projectId" minLength:"1"`
		Title       string `json:"title" minLength:"1" maxLength:"500"`
		Description string `json:"description,omitempty" maxLength:"50000"`
	}
}

// ProposeSmartOutput is the response for POST /workspaces/{wsId}/tasks/propose-smart.
type ProposeSmartOutput struct {
	Body ai.SmartProposal
}

// ---- Apply Smart I/O ------------------------------------------------------

// ApplySmartSubtask is a single subtask entry in the apply-smart request body.
type ApplySmartSubtask struct {
	Title          string `json:"title" minLength:"1" maxLength:"500"`
	Description    string `json:"description,omitempty" maxLength:"50000"`
	Priority       int32  `json:"priority" minimum:"0" maximum:"4"`
	AssigneeUserID string `json:"assigneeUserId,omitempty"`
}

// ApplySmartInput is the request for POST /workspaces/{wsId}/tasks/apply-smart.
type ApplySmartInput struct {
	WsID string `path:"wsId"`
	Body struct {
		ProjectID       string              `json:"projectId" minLength:"1"`
		Title           string              `json:"title" minLength:"1" maxLength:"500"`
		Description     string              `json:"description,omitempty" maxLength:"50000"`
		Priority        int32               `json:"priority" minimum:"0" maximum:"4"`
		AssigneeUserIDs []string            `json:"assigneeUserIds,omitempty"`
		Subtasks        []ApplySmartSubtask `json:"subtasks,omitempty"`
	}
}

// ApplySmartOutputBody is the response body for POST /workspaces/{wsId}/tasks/apply-smart.
type ApplySmartOutputBody struct {
	TaskID     string   `json:"taskId"`
	SubtaskIDs []string `json:"subtaskIds"`
}

// ApplySmartOutput is the response for POST /workspaces/{wsId}/tasks/apply-smart.
type ApplySmartOutput struct {
	Body ApplySmartOutputBody
}

// ---- Handlers --------------------------------------------------------------

// ProposeSmart handles POST /workspaces/{wsId}/tasks/propose-smart.
// It asks the AI orchestrator to decompose a task title+description into
// subtasks and suggest assignees based on embedding similarity with
// historical tasks and workspace membership.
func ProposeSmart(deps SmartCreateDeps) func(context.Context, *ProposeSmartInput) (*ProposeSmartOutput, error) {
	return func(ctx context.Context, in *ProposeSmartInput) (*ProposeSmartOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		if deps.AI == nil || deps.Embedder == nil {
			return nil, httpErr(apierrors.AiProviderNotConfigured)
		}

		proposal, err := deps.AI.ProposeSmartCreate(
			ctx, ws.ID,
			in.Body.Title, in.Body.Description,
			deps.Embedder.Provider, deps.Queries,
		)
		if err != nil {
			return nil, mapAIError(err)
		}
		return &ProposeSmartOutput{Body: *proposal}, nil
	}
}

// ApplySmart handles POST /workspaces/{wsId}/tasks/apply-smart.
// It creates a parent task and optional subtasks within a single
// transaction, attaching assignees and emitting events for each
// created task.
func ApplySmart(deps SmartCreateDeps) func(context.Context, *ApplySmartInput) (*ApplySmartOutput, error) {
	return func(ctx context.Context, in *ApplySmartInput) (*ApplySmartOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskAccessDenied)
		}

		// Resolve project within workspace.
		prjPub, err := types.Parse(in.Body.ProjectID)
		if err != nil {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		prj, err := deps.Queries.FindProjectByPublicId(ctx, generated.FindProjectByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    prjPub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
		}

		// Begin transaction.
		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer tx.Rollback() //nolint:errcheck

		qtx := deps.Queries.WithTx(tx)

		// Create parent task.
		parentPub := types.New()
		desc := sql.NullString{String: in.Body.Description, Valid: in.Body.Description != ""}
		parentID, err := qtx.CreateTask(ctx, generated.CreateTaskParams{
			PublicID:        parentPub,
			WorkspaceID:     ws.ID,
			ProjectID:       prj.ID,
			ParentTaskID:    sql.NullInt32{},
			CreatedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
			UpdatedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
			Title:           in.Body.Title,
			Description:     desc,
			Priority:        in.Body.Priority,
			Visibility:      generated.TasksVisibilityPublic,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Attach assignees to parent task.
		for _, uid := range in.Body.AssigneeUserIDs {
			if err := addActorByPublicID(ctx, qtx, ws.ID, uint32(parentID), uid); err != nil { //#nosec G115 -- LastInsertId for tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
				return nil, err
			}
		}

		// Create subtasks.
		subtaskIDs := make([]string, 0, len(in.Body.Subtasks))
		for _, sub := range in.Body.Subtasks {
			subPub := types.New()
			subDesc := sql.NullString{String: sub.Description, Valid: sub.Description != ""}
			subID, err := qtx.CreateTask(ctx, generated.CreateTaskParams{
				PublicID:        subPub,
				WorkspaceID:     ws.ID,
				ProjectID:       prj.ID,
				ParentTaskID:    sql.NullInt32{Int32: int32(parentID), Valid: true}, //#nosec G115 -- parent_task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
				CreatedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true},  //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
				UpdatedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true},  //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
				Title:           sub.Title,
				Description:     subDesc,
				Priority:        sub.Priority,
				Visibility:      generated.TasksVisibilityPublic,
			})
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}

			// Attach subtask assignee if provided.
			if sub.AssigneeUserID != "" {
				if err := addActorByPublicID(ctx, qtx, ws.ID, uint32(subID), sub.AssigneeUserID); err != nil { //#nosec G115 -- LastInsertId for tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
					return nil, err
				}
			}
			subtaskIDs = append(subtaskIDs, subPub.String())
		}

		// Commit transaction.
		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Emit events after commit so they reference committed rows.
		emitCreatedEvent(ctx, deps.DB, ws.ID, actorID, parentID, parentPub, prjPub, in.Body.Title)
		for i, sub := range in.Body.Subtasks {
			subPub, _ := types.Parse(subtaskIDs[i])
			// SubtaskIDs were generated by types.New() and formatted via
			// String(), so Parse will not fail.
			emitCreatedEvent(ctx, deps.DB, ws.ID, actorID, 0, subPub, prjPub, sub.Title)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "task.smart_create",
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "task",
			ResourceID:   parentPub.String(),
			Metadata: map[string]any{
				"subtaskCount": len(subtaskIDs),
				"projectId":    in.Body.ProjectID,
			},
		})

		// Write-time embedding for the parent task (best-effort).
		if deps.Embedder != nil {
			_ = deps.Embedder.EmbedTask(ctx, ws.ID, uint32(parentID), in.Body.Title, in.Body.Description) //#nosec G115 -- LastInsertId for tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
		}

		out := &ApplySmartOutput{}
		out.Body.TaskID = parentPub.String()
		out.Body.SubtaskIDs = subtaskIDs
		return out, nil
	}
}

// RegisterSmartCreate wires the AI-powered smart task creation endpoints
// under /workspaces/{wsId}/tasks/. The caller must attach
// RequireWorkspaceMember to the underlying chi router so workspace
// context is populated.
func RegisterSmartCreate(api huma.API, deps SmartCreateDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "tasks-propose-smart",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/tasks/propose-smart",
		Summary:     "AI-powered subtask decomposition and assignee suggestions",
		Description: "Asks the workspace's LLM to decompose a goal into subtasks with suggested assignees. Returns the proposal and a cache key so /apply-smart can persist it; nothing is written until apply.",
		Tags:        []string{"Tasks"},
	}, ProposeSmart(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-apply-smart",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/tasks/apply-smart",
		Summary:     "Apply AI proposal — create parent task with subtasks and assignees",
		Description: "Persists a proposal returned by /propose-smart: creates the parent task plus each accepted subtask under it and sets the suggested assignees. The client may trim the subtask set before applying.",
		Tags:        []string{"Tasks"},
	}, ApplySmart(deps))
}

// ---- helpers ---------------------------------------------------------------

// addActorByPublicID resolves a user public ID to an internal ID and adds
// them as an assignee on the given task. It returns an httpErr-wrapped
// error on failure.
func addActorByPublicID(ctx context.Context, qtx *generated.Queries, wsID, taskID uint32, userPublicID string) error {
	userPub, err := types.Parse(userPublicID)
	if err != nil {
		return httpErr(apierrors.WsMemberNotFound)
	}
	// Resolve the target user scoped to this workspace so an AI proposal
	// cannot attach a user from another tenant as an assignee.
	uid, err := qtx.FindWorkspaceMemberUserInternalIdByPublicId(ctx, generated.FindWorkspaceMemberUserInternalIdByPublicIdParams{
		WorkspaceID: wsID,
		PublicID:    userPub,
	})
	if err != nil {
		return httpErr(apierr.SpecForErrNoRows(err, apierrors.WsMemberNotFound, apierrors.InternalUnexpected))
	}
	pub := types.New()
	if _, err := qtx.AddActor(ctx, generated.AddActorParams{
		PublicID:    pub,
		WorkspaceID: wsID,
		TaskID:      taskID,
		UserID:      sql.NullInt32{Int32: int32(uid), Valid: true},
		Role:        generated.TaskActorsRoleAssignee,
	}); err != nil {
		return httpErr(apierrors.InternalUnexpected)
	}
	return nil
}

// emitCreatedEvent appends a TaskCreated event for a newly created task.
func emitCreatedEvent(ctx context.Context, db *sql.DB, wsID, actorID uint32, taskID int64, taskPub, prjPub types.PublicID, title string) {
	actorIDv := int64(actorID)
	if err := eventbus.Append(ctx, db, eventbus.Event{
		Type:        eventbus.TaskCreated,
		WorkspaceID: wsID,
		ActorUserID: &actorIDv,
		TaskID:      &taskID,
		Payload: map[string]any{
			"taskId":    taskPub.String(),
			"projectId": prjPub.String(),
			"title":     title,
		},
	}); err != nil {
		nflog.LoggerFromContext(ctx).ErrorContext(ctx, "eventbus.Append failed",
			slog.Any("err", err),
			slog.String("handler", "tasks.emitCreatedEvent"),
			slog.String("event_type", string(eventbus.TaskCreated)),
			logutil.LogEntityPID("task", taskPub),
		)
	}
}

// mapAIError translates AI package sentinel errors into HTTP error
// responses.
func mapAIError(err error) error {
	switch {
	case errors.Is(err, ai.ErrNoProvider):
		return httpErr(apierrors.AiProviderNotConfigured)
	case errors.Is(err, ai.ErrDailyBudgetExceeded):
		return httpErr(apierrors.AiCostGuardExceeded)
	case errors.Is(err, ai.ErrParse):
		return httpErr(apierrors.AiResponseInvalidJson)
	default:
		return httpErr(apierrors.AiProviderUpstreamUnreachable)
	}
}
