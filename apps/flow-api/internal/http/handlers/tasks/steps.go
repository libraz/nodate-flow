package tasks

import (
	"context"
	"database/sql"
	"log/slog"
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
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
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
//
// The fields are the fields of [ApplyStep], with the same names, types
// and bounds, because the pair of endpoints is documented as "apply
// what propose returned" and a caller must be able to do exactly that.
// Priority used to be a label here and an integer there, so the two
// halves of the round trip did not fit: the proposal had to be
// rewritten before it could be applied, using a mapping that lived
// nowhere the caller could see.
type ProposedStep struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    int32  `json:"priority" minimum:"0" maximum:"4" doc:"Priority on the same 0-4 scale as tasks; 0 means none"`
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
//
// Priority is optional, matching CreateTaskBody and the MCP apply_steps
// tool, both of which treat title as the only required field. The
// column has a default and 0 is a real value ("no priority"), so
// demanding the field prevented nothing and made the sibling surfaces
// disagree about what a step is. An omitted priority and an explicit 0
// both resolve to 0, so the usual `omitempty` ambiguity on an integer
// costs nothing here.
type ApplyStep struct {
	Title       string `json:"title" minLength:"1" maxLength:"500"`
	Description string `json:"description,omitempty" maxLength:"50000"`
	Priority    int32  `json:"priority,omitempty" minimum:"0" maximum:"4" default:"0"`
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

		// Fetch existing child tasks to avoid duplicate proposals. The
		// middleware resolved the parent for this actor; a child carries
		// its own visibility and its title reaches the response, so the
		// children are filtered by the Layer-4 rule in their own right.
		actorID, _ := middleware.ActorFromContext(ctx)
		vis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole(ws.Role))
		var children []ai.ChildTaskSummary
		childRows, err := deps.Queries.ListChildTasksByParentID(ctx, generated.ListChildTasksByParentIDParams{
			WorkspaceID:   ws.ID,
			ParentTaskID:  sql.NullInt32{Int32: int32(task.ID), Valid: true}, //#nosec G115 -- parent task id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			IsElevated:    vis.IsElevated,
			ActorUserID:   vis.ActorUserID,
			ActorUserID_2: vis.ActorUserID,
			ActorUserID_3: vis.ActorUserID,
		})
		if err == nil {
			for _, c := range childRows {
				children = append(children, ai.ChildTaskSummary{
					Title: c.Title,
					State: string(c.DerivedState),
				})
			}
		}

		// Resolve optional embed client for similar-task context. The
		// interface stays unset when no embedder is configured: a nil
		// *embed.Client held in it would be non-nil as an interface and
		// the orchestrator's nil check would let it through.
		var embedClient ai.EmbedClient
		var reader ai.SmartCreateReader
		if deps.Embedder != nil {
			embedClient = deps.Embedder
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
			embedClient, reader,
			vis,
		)
		if err != nil {
			return nil, mapAIError(err)
		}

		out := &ProposeStepsOutput{}
		out.Body.ParentTaskID = task.PublicID.String()
		out.Body.Steps = make([]ProposedStep, 0, len(steps))
		for _, st := range steps {
			// The model answers with a word; the API speaks the same
			// integer scale as every other task surface.
			priority, known := handlerutil.PriorityFromLabel(st.Priority)
			if !known {
				// The proposal still goes out — one unrankable step is
				// not worth failing a decomposition over — but the
				// substitution is recorded. A model drifting off the
				// vocabulary it was handed is otherwise invisible: the
				// caller sees a priority and cannot tell it was chosen
				// by a fallback rather than by the model.
				slog.WarnContext(ctx, "tasks: LLM returned a priority outside the vocabulary",
					slog.String("handler", "tasks.ProposeSteps"),
					slog.String("priority_label", st.Priority),
					logutil.LogEntity("workspace", ws.PublicID),
					slog.String("task_public_id", task.PublicID.String()),
				)
			}
			out.Body.Steps = append(out.Body.Steps, ProposedStep{
				Title:       st.Title,
				Description: st.Description,
				Priority:    priority,
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

		// Every step title is checked before the transaction opens, so a
		// blank one in the middle of the list is refused as a validation
		// error rather than aborting a batch that has already inserted
		// its predecessors.
		stepTitles := make([]taskrules.Title, 0, len(in.Body.Steps))
		for _, st := range in.Body.Steps {
			title, err := taskrules.NewTitle(st.Title)
			if err != nil {
				return nil, translateTaskRuleError(err)
			}
			stepTitles = append(stepTitles, title)
		}

		// Resolve parent's project_id.
		var parentProjectID uint32
		if err := deps.DB.QueryRowContext(ctx,
			`SELECT project_id FROM tasks WHERE id = ? AND workspace_id = ? LIMIT 1`,
			task.ID, ws.ID,
		).Scan(&parentProjectID); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Every step is created in one transaction so a failure halfway
		// through the list leaves no orphan children behind. Task-number
		// allocation reads MAX(task_number) inside the transaction, so each
		// step sees the rows its predecessors inserted and the children get
		// distinct numbers.
		created := make([]string, 0, len(in.Body.Steps))
		childIDs := make([]int64, 0, len(in.Body.Steps))
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.ApplySteps", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			created = created[:0]
			childIDs = childIDs[:0]
			for i, st := range in.Body.Steps {
				child, err := taskcreate.New(ctx, tx, taskcreate.AuthoredBy(actorID), taskcreate.Args{
					WorkspaceID:  ws.ID,
					ProjectID:    parentProjectID,
					ParentTaskID: sql.NullInt32{Int32: int32(task.ID), Valid: true}, //#nosec G115 -- parent task id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
					Title:        stepTitles[i],
					Description:  sql.NullString{String: st.Description, Valid: st.Description != ""},
					Priority:     st.Priority,
				})
				if err != nil {
					return err
				}
				created = append(created, child.PublicID.String())
				childIDs = append(childIDs, child.ID)
			}
			return nil
		})
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Emit events after commit so they reference committed rows. The
		// title an event carries is the title the child row carries:
		// recording the submitted one instead would describe the step as
		// having a name it was never stored under.
		parentPubStr := task.PublicID.String()
		for i := range in.Body.Steps {
			actor := int64(actorID)
			eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(deps.DB), eventbus.Event{
				Type:        eventbus.TaskCreated,
				WorkspaceID: ws.ID,
				ActorUserID: &actor,
				TaskID:      &childIDs[i],
				Payload: map[string]any{
					"taskId":       created[i],
					"title":        stepTitles[i].String(),
					"parentTaskId": parentPubStr,
					"via":          "api:apply_steps",
				},
			}, "tasks.ApplySteps")
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

		// Each step is a task in its own right, so it carries its own
		// embedding. The embedded text is the stored text: the step
		// title is trimmed on the way in, so embedding the submitted
		// one would index a padded string against a row holding the
		// trimmed one.
		for i, st := range in.Body.Steps {
			embed.RefreshTaskAfterCommit(ctx, deps.Embedder, ws.ID, uint32(childIDs[i]), stepTitles[i].String(), st.Description) //#nosec G115 -- LastInsertId for tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
		}

		out := &ApplyStepsOutput{}
		out.Body.Created = created
		return out, nil
	}
}

// RegisterSteps wires the AI-powered step decomposition endpoints under
// /tasks/{id}/. Both routes mutate or spend: propose-steps consumes LLM
// budget and apply-steps creates child tasks, so the caller must attach
// RequireTaskAccess followed by RequireProjectRole(ProjectRoleEditor) so a
// read-only project role cannot reach them.
func RegisterSteps(api huma.API, deps StepsDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "tasks-propose-steps",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/propose-steps",
		Summary:     "AI-powered step decomposition for a task",
		Description: "Asks the workspace's LLM to decompose this task into ordered execution steps. Returns the proposal and a cache key so /apply-steps can persist it; nothing is written until apply.",
		Tags:        []string{"Tasks"},
	}, ProposeSteps(deps))

	huma.Register(api, huma.Operation{
		OperationID: "tasks-apply-steps",
		Method:      http.MethodPost,
		Path:        "/tasks/{id}/apply-steps",
		Summary:     "Apply proposed steps as child tasks under a parent task",
		Description: "Persists a proposal returned by /propose-steps as ordered child tasks under this parent. The client may trim or reorder the steps before applying.",
		Tags:        []string{"Tasks"},
	}, ApplySteps(deps))
}
