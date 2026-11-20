package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

// runListTimeboxes lists timeboxes in the caller's workspace.
func runListTimeboxes(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		Limit  int32 `json:"limit"`
		Offset int32 `json:"offset"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	rows, err := deps.Queries.ListTimeboxesForWorkspace(ctx, generated.ListTimeboxesForWorkspaceParams{
		WorkspaceID: s.workspaceID,
		Limit:       in.Limit,
		Offset:      in.Offset,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		m := map[string]any{
			"id":       r.PublicID.String(),
			"name":     r.Name,
			"startsOn": r.StartsOn.UTC().Format("2006-01-02"),
			"endsOn":   r.EndsOn.UTC().Format("2006-01-02"),
			"status":   string(r.Status),
		}
		if r.Description.Valid {
			m["description"] = r.Description.String
		}
		if r.ProjectName.Valid {
			m["projectId"] = r.ProjectPublicID.String()
			m["projectName"] = r.ProjectName.String
		}
		items = append(items, m)
	}
	return map[string]any{"timeboxes": items}, nil
}

// runCreateTimebox creates a new timebox in the workspace.
func runCreateTimebox(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		Name        string `json:"name"`
		StartsOn    string `json:"startsOn"`
		EndsOn      string `json:"endsOn"`
		Description string `json:"description"`
		ProjectID   string `json:"projectId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Name == "" || in.StartsOn == "" || in.EndsOn == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	starts, err := time.Parse("2006-01-02", in.StartsOn)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	ends, err := time.Parse("2006-01-02", in.EndsOn)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if !ends.After(starts) {
		return nil, apierrors.New(apierrors.TimeboxTimeboxInvalidDates)
	}
	var projectID sql.NullInt32
	if in.ProjectID != "" {
		prjID, err := resolveProjectForWrite(ctx, deps, s, in.ProjectID)
		if err != nil {
			return nil, err
		}
		projectID = sql.NullInt32{Int32: int32(prjID), Valid: true} //#nosec G115 -- project id is projects.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	}
	pub := newPublicID()
	desc := sql.NullString{String: in.Description, Valid: in.Description != ""}
	if err := dbretry.InTx(ctx, deps.DB, "mcp.create_timebox", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if _, err := deps.Queries.WithTx(tx.RawTx()).CreateTimebox(ctx, generated.CreateTimeboxParams{
			PublicID:    pub,
			WorkspaceID: s.workspaceID,
			ProjectID:   projectID,
			CreatorID:   s.userID,
			Name:        in.Name,
			Description: desc,
			StartsOn:    starts,
			EndsOn:      ends,
		}); err != nil {
			return err
		}
		return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:    eventbus.TimeboxCreated,
			AuditAction:  "timebox.create",
			ResourceType: "timebox",
			ResourceID:   pub.String(),
			Payload: map[string]any{
				"timeboxId": pub.String(),
				"name":      in.Name,
				"via":       "mcp",
			},
			CallSite: "mcp.create_timebox",
		})
	}); err != nil {
		// A workspace holds one live timebox per name. The refusal is
		// permanent — the caller has to pick another name — so it is
		// named the way the REST route names it rather than reported as a
		// tool fault, which reads as a server problem an agent retries.
		if handlerutil.IsDuplicateEntry(err) {
			return nil, apierrors.New(apierrors.TimeboxTimeboxNameTaken)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{"id": pub.String()}, nil
}

// runAddTaskToTimebox associates a task with a timebox by resolving
// both public UUIDs to internal IDs.
func runAddTaskToTimebox(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TimeboxID string `json:"timeboxId"`
		TaskID    string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.TimeboxID == "" || in.TaskID == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	// Resolve timebox public_id -> internal id.
	tbPub, err := types.Parse(in.TimeboxID)
	if err != nil {
		return nil, apierrors.New(apierrors.TimeboxTimeboxNotFound)
	}
	tbRow, err := deps.Queries.GetTimeboxByPublicId(ctx, generated.GetTimeboxByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    tbPub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.TimeboxTimeboxNotFound)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	// A completed timebox is closed to changes, on REST and here alike.
	if tbRow.Status == generated.TimeboxesStatusCompleted {
		return nil, apierrors.New(apierrors.TimeboxTimeboxAlreadyCompleted)
	}
	// Resolve task public_id -> internal id.
	taskInternal, taskPub, err := resolveTask(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	linkPub := newPublicID()
	taskID64 := int64(taskInternal)
	if err := dbretry.InTx(ctx, deps.DB, "mcp.add_task_to_timebox", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if err := deps.Queries.WithTx(tx.RawTx()).AddTaskToTimebox(ctx, generated.AddTaskToTimeboxParams{
			PublicID:    linkPub,
			WorkspaceID: s.workspaceID,
			TimeboxID:   tbRow.ID,
			TaskID:      taskInternal,
		}); err != nil {
			return err
		}
		return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:    eventbus.TimeboxTaskAdded,
			AuditAction:  "timebox.task.add",
			ResourceType: "timebox",
			ResourceID:   tbPub.String(),
			TaskID:       &taskID64,
			Payload: map[string]any{
				"timeboxId": tbPub.String(),
				"taskId":    taskPub.String(),
				"via":       "mcp",
			},
			CallSite: "mcp.add_task_to_timebox",
		})
	}); err != nil {
		// The task is already in this timebox. The state the caller asked
		// for already holds and no retry changes that, so it is answered
		// with the code the REST route answers rather than as a tool
		// fault an agent would retry.
		if handlerutil.IsDuplicateEntry(err) {
			return nil, apierrors.New(apierrors.TimeboxTaskAlreadyAdded)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{"ok": true}, nil
}
