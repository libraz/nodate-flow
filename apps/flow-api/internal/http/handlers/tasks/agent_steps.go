// GET /tasks/{id}/agent-runs/{runKey}/steps — the tool calls of one
// task-work run, read back from its agent.task.step events.

package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"

	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	nflog "github.com/libraz/nodate-flow/apps/flow-api/internal/log"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// AgentStep is one tool call of a run.
type AgentStep struct {
	Step        int    `json:"step"`
	Tool        string `json:"tool"`
	Thought     string `json:"thought"`
	ArgsSummary string `json:"argsSummary"`
	Outcome     string `json:"outcome" enum:"applied,proposed,noop,skipped,not_permitted,invalid,failed,stopped"`
	ProposalID  string `json:"proposalId,omitempty"`
	ErrorCode   string `json:"errorCode,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

// ListAgentStepsInput is the request for GET /tasks/{id}/agent-runs/{runKey}/steps.
type ListAgentStepsInput struct {
	ID     string `path:"id"`
	RunKey string `path:"runKey" doc:"Run key from the run's agent-runs payload (UUID)"`
}

// ListAgentStepsBody is the response payload.
type ListAgentStepsBody struct {
	Total int64       `json:"total"`
	Steps []AgentStep `json:"steps"`
}

// ListAgentStepsOutput is the response for GET /tasks/{id}/agent-runs/{runKey}/steps.
type ListAgentStepsOutput struct {
	Body ListAgentStepsBody
}

// stepPayload is the stored shape of an agent.task.step payload.
type stepPayload struct {
	Step        int    `json:"step"`
	Tool        string `json:"tool"`
	Thought     string `json:"thought"`
	ArgsSummary string `json:"argsSummary"`
	Outcome     string `json:"outcome"`
	ProposalID  string `json:"proposalId"`
	ErrorCode   string `json:"errorCode"`
}

// ListAgentSteps handles GET /tasks/{id}/agent-runs/{runKey}/steps.
func ListAgentSteps(deps Deps) func(context.Context, *ListAgentStepsInput) (*ListAgentStepsOutput, error) {
	return func(ctx context.Context, in *ListAgentStepsInput) (*ListAgentStepsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		runKey, err := uuid.Parse(in.RunKey)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		rows, err := deps.Queries.ListAgentTaskSteps(ctx, generated.ListAgentTaskStepsParams{
			WorkspaceID: ws.ID,
			TaskID:      sql.NullInt32{Int32: int32(task.ID), Valid: true}, //#nosec G115 -- tasks.id fits int32 within realistic deployments
			StepType:    string(eventbus.AgentTaskStep),
			RunKey:      runKey.String(),
		})
		if err != nil {
			nflog.LoggerFromContext(ctx).ErrorContext(ctx, "ListAgentTaskSteps failed",
				slog.Any("err", err),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("task", task.PublicID),
			)
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListAgentStepsOutput{}
		out.Body.Steps = make([]AgentStep, 0, len(rows))
		for _, r := range rows {
			var p stepPayload
			if err := json.Unmarshal(r.PayloadJson, &p); err != nil {
				continue
			}
			out.Body.Steps = append(out.Body.Steps, AgentStep{
				Step:        p.Step,
				Tool:        p.Tool,
				Thought:     p.Thought,
				ArgsSummary: p.ArgsSummary,
				Outcome:     p.Outcome,
				ProposalID:  p.ProposalID,
				ErrorCode:   p.ErrorCode,
				CreatedAt:   r.OccurredAt.Unix(),
			})
		}
		out.Body.Total = int64(len(out.Body.Steps))
		return out, nil
	}
}
