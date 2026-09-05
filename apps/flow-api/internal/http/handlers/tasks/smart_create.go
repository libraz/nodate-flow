package tasks

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
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
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}

		// The similar tasks the proposal is built from are quoted by
		// title into the prompt and the rationale, so they are drawn
		// from what this actor may read.
		vis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole(ws.Role))
		proposal, err := deps.AI.ProposeSmartCreate(
			ctx, ws.ID,
			in.Body.Title, in.Body.Description,
			deps.Embedder, deps.Queries,
			vis,
		)
		if err != nil {
			return nil, mapAIError(err)
		}
		return &ProposeSmartOutput{Body: *proposal}, nil
	}
}

// createdSubtask is one subtask carried out of the ApplySmart
// transaction. Both ids travel together because the two consumers need
// different ones: the response and the audit entry address the row by its
// public id, while events.task_id is a foreign key onto tasks.id. Keeping
// only the public id there costs the subtask its creation event, and with
// it the first entry of the history its derived state is read from.
type createdSubtask struct {
	ID       int64
	PublicID types.PublicID
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

		// The parent and every subtask title are checked before the
		// transaction opens, so a blank one partway down the list is
		// refused rather than aborting a batch that has already inserted
		// the parent.
		parentTitle, err := taskrules.NewTitle(in.Body.Title)
		if err != nil {
			return nil, translateTaskRuleError(err)
		}
		subtaskTitles := make([]taskrules.Title, 0, len(in.Body.Subtasks))
		for _, sub := range in.Body.Subtasks {
			title, terr := taskrules.NewTitle(sub.Title)
			if terr != nil {
				return nil, translateTaskRuleError(terr)
			}
			subtaskTitles = append(subtaskTitles, title)
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
		// Same project-editor gate as POST /tasks: the project comes from the
		// request body, so workspace membership alone does not say whether the
		// caller may create tasks in it.
		if spec := requireProjectEditor(ctx, deps.DB, ws.ID, prj.ID, actorID); spec != nil {
			return nil, httpErr(spec)
		}

		var (
			parentID  int64
			parentPub types.PublicID
			subtasks  []createdSubtask
			// answered holds a response-shaped error decided inside the
			// transaction, so a rejected assignee is not reported as a
			// transaction failure.
			answered error
		)
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.ApplySmart", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			qtx := deps.Queries.WithTx(tx.RawTx())

			// Resolving reads, and the first read of a transaction is what
			// fixes its snapshot, so the project lock has to come before it
			// or the task number is allocated from a stale view of the
			// project.
			if lerr := taskcreate.LockProject(ctx, tx, ws.ID, prj.ID); lerr != nil {
				return lerr
			}

			// The proposal names the assignees; the caller who applied it is
			// the creator. Resolution runs in this transaction so a user
			// that stops being a member mid-apply cannot slip through.
			parentActors := make([]taskcreate.Actor, 0, len(in.Body.AssigneeUserIDs))
			for _, uid := range in.Body.AssigneeUserIDs {
				resolved, rerr := resolveAssignee(ctx, qtx, ws.ID, uid)
				if rerr != nil {
					answered = rerr
					return rerr
				}
				parentActors = append(parentActors, taskcreate.Actor{UserID: resolved, Role: generated.TaskActorsRoleAssignee})
			}

			// Create parent task.
			parent, err := taskcreate.New(ctx, tx, taskcreate.AuthoredBy(actorID).WithActors(parentActors...), taskcreate.Args{
				WorkspaceID: ws.ID,
				ProjectID:   prj.ID,
				Title:       parentTitle,
				Description: sql.NullString{String: in.Body.Description, Valid: in.Body.Description != ""},
				Priority:    in.Body.Priority,
			})
			if err != nil {
				return err
			}
			parentID = parent.ID
			parentPub = parent.PublicID

			// Create subtasks.
			subtasks = make([]createdSubtask, 0, len(in.Body.Subtasks))
			for i, sub := range in.Body.Subtasks {
				attr := taskcreate.AuthoredBy(actorID)
				if sub.AssigneeUserID != "" {
					resolved, rerr := resolveAssignee(ctx, qtx, ws.ID, sub.AssigneeUserID)
					if rerr != nil {
						answered = rerr
						return rerr
					}
					attr = attr.WithActors(taskcreate.Actor{UserID: resolved, Role: generated.TaskActorsRoleAssignee})
				}
				child, err := taskcreate.New(ctx, tx, attr, taskcreate.Args{
					WorkspaceID:  ws.ID,
					ProjectID:    prj.ID,
					ParentTaskID: sql.NullInt32{Int32: int32(parentID), Valid: true}, //#nosec G115 -- parent_task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
					Title:        subtaskTitles[i],
					Description:  sql.NullString{String: sub.Description, Valid: sub.Description != ""},
					Priority:     sub.Priority,
				})
				if err != nil {
					return err
				}
				subtasks = append(subtasks, createdSubtask{ID: child.ID, PublicID: child.PublicID})
			}
			return nil
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		subtaskIDs := make([]string, 0, len(subtasks))
		for _, sub := range subtasks {
			subtaskIDs = append(subtaskIDs, sub.PublicID.String())
		}

		// Emit events after commit so they reference committed rows. The
		// title an event carries is the title the row carries: recording
		// the submitted one instead would describe the task as having a
		// name it was never stored under.
		emitCreatedEvent(ctx, deps.DB, ws.ID, actorID, parentID, parentPub, prjPub, parentTitle.String())
		for i, sub := range subtasks {
			emitCreatedEvent(ctx, deps.DB, ws.ID, actorID, sub.ID, sub.PublicID, prjPub, subtaskTitles[i].String())
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

		// Each subtask is a task in its own right, so its own history has
		// to start where the others do.
		for i := range in.Body.Subtasks {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "task.create",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "task",
				ResourceID:   subtaskIDs[i],
				Metadata: map[string]any{
					"title":        subtaskTitles[i].String(),
					"projectId":    in.Body.ProjectID,
					"parentTaskId": parentPub.String(),
				},
			})
		}

		// The embedded text is the stored text. Embedding the submitted
		// title instead would index a padded string against a row holding
		// the trimmed one, so a search would match on characters the task
		// does not carry.
		embed.RefreshTaskAfterCommit(ctx, deps.Embedder, ws.ID, uint32(parentID), parentTitle.String(), in.Body.Description) //#nosec G115 -- LastInsertId for tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
		// A subtask is a task in its own right, so it is searchable under
		// its own text rather than only under its parent's.
		for i, sub := range subtasks {
			embed.RefreshTaskAfterCommit(ctx, deps.Embedder, ws.ID, uint32(sub.ID), subtaskTitles[i].String(), in.Body.Subtasks[i].Description) //#nosec G115 -- LastInsertId for tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
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

// resolveAssignee maps a user public ID onto the internal ID an actor row
// takes. It returns an httpErr-wrapped error on failure, so the caller can
// answer a rejected assignee as a response rather than as a transaction
// failure.
func resolveAssignee(ctx context.Context, qtx *generated.Queries, wsID uint32, userPublicID string) (uint32, error) {
	userPub, err := types.Parse(userPublicID)
	if err != nil {
		return 0, httpErr(apierrors.WsMemberNotFound)
	}
	// Resolve the target user scoped to this workspace so an AI proposal
	// cannot attach a user from another tenant as an assignee.
	uid, err := qtx.FindWorkspaceMemberUserInternalIdByPublicId(ctx, generated.FindWorkspaceMemberUserInternalIdByPublicIdParams{
		WorkspaceID: wsID,
		PublicID:    userPub,
	})
	if err != nil {
		return 0, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsMemberNotFound, apierrors.InternalUnexpected))
	}
	if uid > math.MaxInt32 {
		return 0, httpErr(apierrors.InternalUnexpected)
	}
	return uid, nil
}

// emitCreatedEvent appends a TaskCreated event for a newly created task.
func emitCreatedEvent(ctx context.Context, db *sql.DB, wsID, actorID uint32, taskID int64, taskPub, prjPub types.PublicID, title string) {
	actorIDv := int64(actorID)
	eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(db), eventbus.Event{
		Type:        eventbus.TaskCreated,
		WorkspaceID: wsID,
		ActorUserID: &actorIDv,
		TaskID:      &taskID,
		Payload: map[string]any{
			"taskId":    taskPub.String(),
			"projectId": prjPub.String(),
			"title":     title,
		},
	}, "tasks.emitCreatedEvent")
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
