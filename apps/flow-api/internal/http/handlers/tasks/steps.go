package tasks

import (
	"context"
	"database/sql"
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
)

// StepsDeps is the dependency bundle for the propose-steps and
// apply-steps endpoints. It is separate from the main task Deps because
// these endpoints require an AI orchestrator that the regular CRUD
// routes do not need.
type StepsDeps struct {
	DB       *sql.DB
	Queries  *generated.Queries
	AI       *ai.Orchestrator
	Embedder *embed.Client
	Audit    *audit.Recorder
}

// ---- Propose Steps I/O -----------------------------------------------------

// ProposeStepsInput is the request for POST /tasks/{id}/propose-steps.
type ProposeStepsInput struct {
	ID   string `path:"id"`
	Body struct {
		Granularity string `json:"granularity,omitempty" enum:"coarse,standard,fine" default:"standard" doc:"Decomposition granularity: coarse (3-5), standard (5-8), fine (8-15)"`
	}
}

// ProposedStep is a single step entry in the propose-steps response.
type ProposedStep struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
}

// ProposeStepsOutputBody is the response body for POST /tasks/{id}/propose-steps.
type ProposeStepsOutputBody struct {
	ParentTaskID string         `json:"parentTaskId"`
	Steps        []ProposedStep `json:"steps"`
}

// ProposeStepsOutput is the response for POST /tasks/{id}/propose-steps.
type ProposeStepsOutput struct {
	Body ProposeStepsOutputBody
}

// ---- Apply Steps I/O -------------------------------------------------------

// ApplyStep is a single step entry in the apply-steps request body.
type ApplyStep struct {
	Title       string `json:"title" minLength:"1" maxLength:"500"`
	Description string `json:"description,omitempty" maxLength:"50000"`
	Priority    int32  `json:"priority" minimum:"0" maximum:"4"`
}

// ApplyStepsInput is the request for POST /tasks/{id}/apply-steps.
type ApplyStepsInput struct {
	ID   string `path:"id"`
	Body struct {
		Steps []ApplyStep `json:"steps" minItems:"1"`
	}
}

// ApplyStepsOutputBody is the response body for POST /tasks/{id}/apply-steps.
type ApplyStepsOutputBody struct {
	Created []string `json:"created"`
}

// ApplyStepsOutput is the response for POST /tasks/{id}/apply-steps.
type ApplyStepsOutput struct {
	Body ApplyStepsOutputBody
}

// ---- Handlers --------------------------------------------------------------

// ProposeSteps handles POST /tasks/{id}/propose-steps.
// It reads the task's title and description from the database, then asks
// the AI orchestrator to decompose it into concrete execution steps.
func ProposeSteps(deps StepsDeps) func(context.Context, *ProposeStepsInput) (*ProposeStepsOutput, error) {
	return func(ctx context.Context, in *ProposeStepsInput) (*ProposeStepsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		if deps.AI == nil {
			return nil, httpErr(apierrors.AiProviderNotConfigured)
		}

		// Load full task row to get title + description.
		row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    types.PublicID(task.PublicID),
		})
		if err != nil {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		// Map granularity string to typed constant.
		granularity := ai.GranularityStandard
		switch in.Body.Granularity {
		case "coarse":
			granularity = ai.GranularityCoarse
		case "fine":
			granularity = ai.GranularityFine
		}

		// Fetch existing child tasks to avoid duplicate proposals.
		var children []ai.ChildTaskSummary
		childRows, err := deps.Queries.ListChildTasksByParentID(ctx, generated.ListChildTasksByParentIDParams{
			WorkspaceID:  ws.ID,
			ParentTaskID: sql.NullInt32{Int32: int32(task.ID), Valid: true},
		})
		if err == nil {
			for _, c := range childRows {
				children = append(children, ai.ChildTaskSummary{
					Title: c.Title,
					State: string(c.DerivedState),
				})
			}
		}

		// Resolve optional embed client for similar-task context.
		var embedProvider ai.EmbedClient
		var reader ai.SmartCreateReader
		if deps.Embedder != nil {
			embedProvider = deps.Embedder.Provider
			reader = deps.Queries
		}

		desc := ""
		if row.Description.Valid {
			desc = row.Description.String
		}

		steps, err := deps.AI.ProposeSteps(
			ctx, ws.ID,
			row.Title, desc,
			granularity, children,
			embedProvider, reader,
		)
		if err != nil {
			return nil, mapAIError(err)
		}

		out := &ProposeStepsOutput{}
		out.Body.ParentTaskID = task.PublicID.String()
		out.Body.Steps = make([]ProposedStep, 0, len(steps))
		for _, st := range steps {
			out.Body.Steps = append(out.Body.Steps, ProposedStep{
				Title:       st.Title,
				Description: st.Description,
				Priority:    st.Priority,
			})
		}
		return out, nil
	}
}

// ApplySteps handles POST /tasks/{id}/apply-steps.
// It creates the given steps as child tasks under the existing parent
// task. Each step becomes a new task row with parent_task_id set to the
// parent's internal id. Returns the list of created child public ids.
func ApplySteps(deps StepsDeps) func(context.Context, *ApplyStepsInput) (*ApplyStepsOutput, error) {
	return func(ctx context.Context, in *ApplyStepsInput) (*ApplyStepsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskAccessDenied)
		}

		// Resolve parent's project_id.
		var parentProjectID uint32
		if err := deps.DB.QueryRowContext(ctx,
			`SELECT project_id FROM tasks WHERE id = ? AND workspace_id = ? LIMIT 1`,
			task.ID, ws.ID,
		).Scan(&parentProjectID); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Begin transaction.
		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer tx.Rollback() //nolint:errcheck

		qtx := deps.Queries.WithTx(tx)

		created := make([]string, 0, len(in.Body.Steps))
		childIDs := make([]int64, 0, len(in.Body.Steps))
		for _, st := range in.Body.Steps {
			pub := types.New()
			desc := sql.NullString{String: st.Description, Valid: st.Description != ""}
			childID, err := qtx.CreateTask(ctx, generated.CreateTaskParams{
				PublicID:        pub,
				WorkspaceID:     ws.ID,
				ProjectID:       parentProjectID,
				ParentTaskID:    sql.NullInt32{Int32: int32(task.ID), Valid: true},
				CreatedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true},
				Title:           st.Title,
				Description:     desc,
				Priority:        st.Priority,
				Visibility:      generated.TasksVisibilityPublic,
			})
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			created = append(created, pub.String())
			childIDs = append(childIDs, childID)
		}

		// Commit transaction.
		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Emit events after commit so they reference committed rows.
		parentPubStr := task.PublicID.String()
		for i, st := range in.Body.Steps {
			actor := int64(actorID)
			_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
				Type:        eventbus.TaskCreated,
				WorkspaceID: ws.ID,
				ActorUserID: &actor,
				TaskID:      &childIDs[i],
				Payload: map[string]any{
					"taskId":       created[i],
					"title":        st.Title,
					"parentTaskId": parentPubStr,
					"via":          "api:apply_steps",
				},
			})
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "task.apply_steps",
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "task",
			ResourceID:   parentPubStr,
			Metadata: map[string]any{
				"stepCount": len(created),
			},
		})

		out := &ApplyStepsOutput{}
		out.Body.Created = created
		return out, nil
	}
}

// RegisterSteps wires the AI-powered step decomposition endpoints under
// /tasks/{id}/. The caller must attach RequireTaskAccess to the
// underlying chi router so task / project / workspace contexts are
// populated.
func RegisterSteps(api huma.API, deps StepsDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "tasks-propose-steps",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/propose-steps",
		Summary:     "AI-powered step decomposition for a task",
	}, ProposeSteps(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-apply-steps",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/apply-steps",
		Summary:     "Apply proposed steps as child tasks under a parent task",
	}, ApplySteps(deps))
}
