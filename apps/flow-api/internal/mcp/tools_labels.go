package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"strconv"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
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
	if _, err := deps.Queries.CreateLabel(ctx, generated.CreateLabelParams{
		PublicID:        pub,
		WorkspaceID:     s.workspaceID,
		CreatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- actor user id from session, bounded by realistic deployments
		Name:            in.Name,
		Color:           in.Color,
		Description:     sql.NullString{String: in.Description, Valid: in.Description != ""},
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	// The label row is committed; a retry would create a second one.
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.LabelCreated,
		AuditAction:  "label.create",
		ResourceType: "label",
		ResourceID:   pub.String(),
		Payload:      map[string]any{"labelId": pub.String(), "via": "mcp"},
		CallSite:     "mcp.create_label",
	})
	return map[string]any{"id": pub.String(), "name": in.Name, "color": in.Color}, nil
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
	taskInternal, _, err := resolveTaskForWrite(ctx, deps, s, in.TaskID, acl.ProjectRoleEditor)
	if err != nil {
		return nil, err
	}
	labelPub, err := types.Parse(in.LabelID)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	label, err := deps.Queries.FindLabelByPublicId(ctx, generated.FindLabelByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    labelPub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	junctionPub := newPublicID()
	if _, err := deps.Queries.CreateTaskLabel(ctx, generated.CreateTaskLabelParams{
		PublicID:    junctionPub,
		WorkspaceID: s.workspaceID,
		TaskID:      taskInternal,
		LabelID:     label.ID,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	taskID64 := int64(taskInternal)
	// The junction row is committed and uniquely keyed, so a retry is
	// rejected before it could re-append the event.
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.TaskLabelAdded,
		AuditAction:  "task.label.add",
		ResourceType: "task_label",
		ResourceID:   in.LabelID,
		TaskID:       &taskID64,
		Payload:      map[string]any{"taskId": in.TaskID, "labelId": in.LabelID, "via": "mcp"},
		CallSite:     "mcp.add_task_label",
	})
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
	taskInternal, _, err := resolveTaskForWrite(ctx, deps, s, in.TaskID, acl.ProjectRoleEditor)
	if err != nil {
		return nil, err
	}
	labelPub, err := types.Parse(in.LabelID)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	label, err := deps.Queries.FindLabelByPublicId(ctx, generated.FindLabelByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    labelPub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	if err := deps.Queries.DisableTaskLabel(ctx, generated.DisableTaskLabelParams{
		WorkspaceID: s.workspaceID,
		TaskID:      taskInternal,
		LabelID:     label.ID,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	taskID64 := int64(taskInternal)
	// Propagated: disabling an already-disabled junction is a no-op, so
	// a retry re-appends the event without changing anything else.
	if err := recordMutationStrict(ctx, deps, s, mutation{
		EventType:    eventbus.TaskLabelRemoved,
		AuditAction:  "task.label.remove",
		ResourceType: "task_label",
		ResourceID:   in.LabelID,
		TaskID:       &taskID64,
		Payload:      map[string]any{"taskId": in.TaskID, "labelId": in.LabelID, "via": "mcp"},
		CallSite:     "mcp.remove_task_label",
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
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	_, taskPub, err := resolveTaskForWrite(ctx, deps, s, in.TaskID, acl.ProjectRoleEditor)
	if err != nil {
		return nil, err
	}
	// Not an existence check: archiving an already-archived task writes
	// the same archived_at and counts zero. resolveTaskForWrite above is
	// what answers for a task that is not there.
	if _, err := deps.Queries.ArchiveTask(ctx, generated.ArchiveTaskParams{
		UpdatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		WorkspaceID:     s.workspaceID,
		PublicID:        taskPub,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	// Propagated: ArchiveTask only matches a not-yet-archived row, so a
	// retry is a no-op UPDATE that still re-appends the event.
	if err := recordMutationStrict(ctx, deps, s, mutation{
		EventType:    eventbus.TaskArchived,
		AuditAction:  "task.archived",
		ResourceType: "task",
		ResourceID:   taskPub.String(),
		Payload:      map[string]any{"taskId": in.TaskID, "via": "mcp"},
		CallSite:     "mcp.archive_task",
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{"ok": true}, nil
}

func runUnarchiveTask(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	_, taskPub, err := resolveTaskForWrite(ctx, deps, s, in.TaskID, acl.ProjectRoleEditor)
	if err != nil {
		return nil, err
	}
	// See archive: un-archiving an already-live task changes nothing and
	// counts zero.
	if _, err := deps.Queries.UnarchiveTask(ctx, generated.UnarchiveTaskParams{
		UpdatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		WorkspaceID:     s.workspaceID,
		PublicID:        taskPub,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	// Propagated for the same reason as archive: the retry converges.
	if err := recordMutationStrict(ctx, deps, s, mutation{
		EventType:    eventbus.TaskUnarchived,
		AuditAction:  "task.unarchived",
		ResourceType: "task",
		ResourceID:   taskPub.String(),
		Payload:      map[string]any{"taskId": in.TaskID, "via": "mcp"},
		CallSite:     "mcp.unarchive_task",
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{"ok": true}, nil
}
