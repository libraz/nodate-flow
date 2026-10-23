package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"strconv"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/labels"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

func runListLabels(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		Limit  int32 `json:"limit"`
		Offset int32 `json:"offset"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	rows, err := deps.Queries.ListLabelsForWorkspace(ctx, generated.ListLabelsForWorkspaceParams{
		WorkspaceID: s.workspaceID,
		Limit:       in.Limit,
		Offset:      in.Offset,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	type labelOut struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description,omitempty"`
	}
	out := make([]labelOut, 0, len(rows))
	for _, r := range rows {
		l := labelOut{
			ID:    r.PublicID.String(),
			Name:  r.Name,
			Color: r.Color,
		}
		if r.Description.Valid {
			l.Description = r.Description.String
		}
		out = append(out, l)
	}
	return map[string]any{"labels": out}, nil
}

func runCreateLabel(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Name == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if in.Color == "" {
		in.Color = "#6b7280"
	}
	pub := newPublicID()
	if err := dbretry.InTx(ctx, deps.DB, "mcp.create_label", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if _, err := deps.Queries.WithTx(tx.RawTx()).CreateLabel(ctx, generated.CreateLabelParams{
			PublicID:        pub,
			WorkspaceID:     s.workspaceID,
			CreatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- actor user id from session, bounded by realistic deployments
			Name:            in.Name,
			Color:           in.Color,
			Description:     sql.NullString{String: in.Description, Valid: in.Description != ""},
		}); err != nil {
			return err
		}
		return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:    eventbus.LabelCreated,
			AuditAction:  "label.create",
			ResourceType: "label",
			ResourceID:   pub.String(),
			Payload:      map[string]any{"labelId": pub.String(), "name": in.Name, "via": "mcp"},
			CallSite:     "mcp.create_label",
		})
	}); err != nil {
		// A name already in use within the same scope is a refusal the
		// caller can act on by choosing another one, and no retry changes
		// it. It is named the way the REST route names it rather than
		// reported as a tool fault, which reads as a server problem.
		if handlerutil.IsDuplicateEntry(err) {
			return nil, apierrors.New(apierrors.WsLabelNameAlreadyTaken)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{"id": pub.String(), "name": in.Name, "color": in.Color}, nil
}

// findLabel resolves a label public id within the session workspace,
// answering a malformed or unknown id with the REST label refusal.
func findLabel(ctx context.Context, deps Deps, s *session, publicID string) (generated.FindLabelByPublicIdRow, types.PublicID, error) {
	labelPub, err := types.Parse(publicID)
	if err != nil {
		return generated.FindLabelByPublicIdRow{}, types.PublicID{}, apierrors.New(apierrors.WsLabelNotFound)
	}
	label, err := deps.Queries.FindLabelByPublicId(ctx, generated.FindLabelByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    labelPub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return generated.FindLabelByPublicIdRow{}, types.PublicID{}, apierrors.New(apierrors.WsLabelNotFound)
		}
		return generated.FindLabelByPublicIdRow{}, types.PublicID{}, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return label, labelPub, nil
}

func runAddTaskLabel(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		TaskID  string `json:"taskId"`
		LabelID string `json:"labelId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	access, taskPub, err := resolveTaskAccessForWrite(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	label, labelPub, err := findLabel(ctx, deps, s, in.LabelID)
	if err != nil {
		return nil, err
	}
	// A label scoped to a project attaches only to tasks of that project,
	// through the rule REST applies.
	if spec := labels.CheckAttachScope(label.ProjectID, access.Task.ProjectID); spec != nil {
		return nil, apierrors.New(spec)
	}
	junctionPub := newPublicID()
	taskID64 := int64(access.Task.ID)
	if err := dbretry.InTx(ctx, deps.DB, "mcp.add_task_label", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if _, err := deps.Queries.WithTx(tx.RawTx()).CreateTaskLabel(ctx, generated.CreateTaskLabelParams{
			PublicID:    junctionPub,
			WorkspaceID: s.workspaceID,
			TaskID:      access.Task.ID,
			LabelID:     label.ID,
		}); err != nil {
			return err
		}
		return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:    eventbus.TaskLabelAdded,
			AuditAction:  "task.label.add",
			ResourceType: "task_label",
			ResourceID:   labelPub.String(),
			TaskID:       &taskID64,
			Payload:      map[string]any{"taskId": taskPub.String(), "labelId": labelPub.String(), "via": "mcp"},
			CallSite:     "mcp.add_task_label",
		})
	}); err != nil {
		// The label is already on this task. The junction carries one live
		// row per (task, label), so the state the caller asked for already
		// holds; the code is the one REST answers for the same duplicate.
		if handlerutil.IsDuplicateEntry(err) {
			return nil, apierrors.New(apierrors.WsLabelAlreadyAttached)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{"ok": true}, nil
}

func runRemoveTaskLabel(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		TaskID  string `json:"taskId"`
		LabelID string `json:"labelId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	taskInternal, taskPub, err := resolveTaskForWrite(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	label, labelPub, err := findLabel(ctx, deps, s, in.LabelID)
	if err != nil {
		return nil, err
	}
	taskID64 := int64(taskInternal)
	if err := dbretry.InTx(ctx, deps.DB, "mcp.remove_task_label", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if err := deps.Queries.WithTx(tx.RawTx()).DisableTaskLabel(ctx, generated.DisableTaskLabelParams{
			WorkspaceID: s.workspaceID,
			TaskID:      taskInternal,
			LabelID:     label.ID,
		}); err != nil {
			return err
		}
		return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:    eventbus.TaskLabelRemoved,
			AuditAction:  "task.label.remove",
			ResourceType: "task_label",
			ResourceID:   labelPub.String(),
			TaskID:       &taskID64,
			Payload:      map[string]any{"taskId": taskPub.String(), "labelId": labelPub.String(), "via": "mcp"},
			CallSite:     "mcp.remove_task_label",
		})
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{"ok": true}, nil
}

func runResolveTaskRef(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		Ref string `json:"ref"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	parts := strings.SplitN(in.Ref, "-", 2)
	if len(parts) != 2 {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	identifier := strings.ToUpper(parts[0])
	num, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	row, err := deps.Queries.ResolveTaskRef(ctx, generated.ResolveTaskRefParams{
		WorkspaceID: s.workspaceID,
		Identifier:  sql.NullString{String: identifier, Valid: identifier != ""},
		TaskNumber:  uint32(num),
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.WsTaskNotFound)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	// Authorize the resolved task through the shared task-visibility ACL.
	// A task the caller cannot see must be indistinguishable from a
	// missing one, so this tool never becomes an existence oracle:
	// visibility denial surfaces as WS.TASK.NOT_FOUND, the same code the
	// ErrNoRows branch above returns.
	if _, _, err := resolveTask(ctx, deps, s, row.PublicID.String()); err != nil {
		return nil, err
	}
	return map[string]any{
		"taskId": row.PublicID.String(),
		"title":  row.Title,
	}, nil
}

func runArchiveTask(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	return setTaskArchived(ctx, deps, s, raw,
		func(ctx context.Context, q *generated.Queries, updatedBy sql.NullInt32, taskPub types.PublicID) (int64, error) {
			return q.ArchiveTask(ctx, generated.ArchiveTaskParams{
				UpdatedByUserID: updatedBy,
				WorkspaceID:     s.workspaceID,
				PublicID:        taskPub,
			})
		},
		func(taskPub string, taskID *int64) mutationlog.Mutation {
			return mutationlog.Mutation{
				EventType:    eventbus.TaskArchived,
				AuditAction:  "task.archived",
				ResourceType: "task",
				ResourceID:   taskPub,
				TaskID:       taskID,
				Payload:      map[string]any{"taskId": taskPub, "via": "mcp"},
				CallSite:     "mcp.archive_task",
			}
		})
}

func runUnarchiveTask(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	return setTaskArchived(ctx, deps, s, raw,
		func(ctx context.Context, q *generated.Queries, updatedBy sql.NullInt32, taskPub types.PublicID) (int64, error) {
			return q.UnarchiveTask(ctx, generated.UnarchiveTaskParams{
				UpdatedByUserID: updatedBy,
				WorkspaceID:     s.workspaceID,
				PublicID:        taskPub,
			})
		},
		func(taskPub string, taskID *int64) mutationlog.Mutation {
			return mutationlog.Mutation{
				EventType:    eventbus.TaskUnarchived,
				AuditAction:  "task.unarchived",
				ResourceType: "task",
				ResourceID:   taskPub,
				TaskID:       taskID,
				Payload:      map[string]any{"taskId": taskPub, "via": "mcp"},
				CallSite:     "mcp.unarchive_task",
			}
		})
}

// setTaskArchived runs an archive or un-archive statement and records it.
// The statement matches only a task not already in the target state, so a
// repeat or a losing concurrent call counts zero and records nothing: there
// is no change to describe. The event names the task, as REST's does, so it
// reaches the task's own timeline.
func setTaskArchived(
	ctx context.Context,
	deps Deps,
	s *session,
	raw json.RawMessage,
	write func(context.Context, *generated.Queries, sql.NullInt32, types.PublicID) (int64, error),
	describe func(taskPub string, taskID *int64) mutationlog.Mutation,
) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	taskInternal, taskPub, err := resolveTaskForWrite(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	taskID64 := int64(taskInternal)
	m := describe(taskPub.String(), &taskID64)
	updatedBy := sql.NullInt32{Int32: int32(s.userID), Valid: true} //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	if err := dbretry.InTx(ctx, deps.DB, m.CallSite, nil, func(ctx context.Context, tx *dbretry.Tx) error {
		rows, err := write(ctx, deps.Queries.WithTx(tx.RawTx()), updatedBy, taskPub)
		if err != nil || rows == 0 {
			return err
		}
		return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), m)
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{"ok": true}, nil
}
