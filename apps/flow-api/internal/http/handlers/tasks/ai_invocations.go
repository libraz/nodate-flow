// Package tasks — ai_invocations.go exposes the per-task AI reasoning
// panel feed. It returns the most recent redacted
// ai_invocations rows scoped to the task so the frontend can render
// "why did AI touch this task?" without leaking workspace-wide data.
package tasks

import (
	"context"
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// ListTaskAiInvocationsInput is the query for
// GET /tasks/{id}/ai/invocations.
//
// Limit override: AI invocations are cost-bounded artefacts the UI
// renders as a chronological audit log; both the cap (100) and
// default (20) are deliberately tighter than handlerutil.MaxListLimit
// to discourage accidental wide scans of the AI cost surface.
type ListTaskAiInvocationsInput struct {
	ID     string `path:"id"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"100" default:"20"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// TaskAiInvocation is the masked DTO for an ai_invocations row scoped
// to a task. Fields mirror the workspace-scoped ai.Invocation DTO in
// handlers/ai/invocations.go — keeping the shape identical lets the
// frontend reuse the same rendering code.
type TaskAiInvocation struct {
	ID               string `json:"id"`
	Purpose          string `json:"purpose"`
	Model            string `json:"model"`
	PromptRedacted   string `json:"promptRedacted"`
	ResponseRedacted string `json:"responseRedacted,omitempty"`
	TokensInput      int32  `json:"tokensInput,omitempty"`
	TokensOutput     int32  `json:"tokensOutput,omitempty"`
	CostEstimate     string `json:"costEstimate,omitempty"`
	Status           string `json:"status"`
	ErrorCode        string `json:"errorCode,omitempty"`
	InvokedAt        int64  `json:"invokedAt"`
}

// ListTaskAiInvocationsOutput wraps the list response.
type ListTaskAiInvocationsOutput struct {
	Body struct {
		Invocations []TaskAiInvocation `json:"invocations"`
	}
}

// ListAiInvocations handles GET /tasks/{id}/ai/invocations.
func ListAiInvocations(deps Deps) func(context.Context, *ListTaskAiInvocationsInput) (*ListTaskAiInvocationsOutput, error) {
	return func(ctx context.Context, in *ListTaskAiInvocationsInput) (*ListTaskAiInvocationsOutput, error) {
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
			limit = 20
		}
		rows, err := deps.Queries.ListAiInvocationsForTask(ctx, generated.ListAiInvocationsForTaskParams{
			WorkspaceID: ws.ID,
			TaskID:      sql.NullInt32{Int32: int32(task.ID), Valid: true}, //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListTaskAiInvocationsOutput{}
		out.Body.Invocations = make([]TaskAiInvocation, 0, len(rows))
		for _, r := range rows {
			dto := TaskAiInvocation{
				ID:             r.PublicID.String(),
				Purpose:        r.Purpose,
				Model:          r.Model,
				PromptRedacted: r.PromptRedacted,
				Status:         string(r.Status),
				InvokedAt:      r.InvokedAt.Unix(),
			}
			if r.ResponseRedacted.Valid {
				dto.ResponseRedacted = r.ResponseRedacted.String
			}
			if r.TokensInput.Valid {
				dto.TokensInput = r.TokensInput.Int32
			}
			if r.TokensOutput.Valid {
				dto.TokensOutput = r.TokensOutput.Int32
			}
			if r.CostEstimate.Valid {
				dto.CostEstimate = r.CostEstimate.String
			}
			if r.ErrorCode.Valid {
				dto.ErrorCode = r.ErrorCode.String
			}
			out.Body.Invocations = append(out.Body.Invocations, dto)
		}
		return out, nil
	}
}
