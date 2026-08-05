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
	actor := int64(s.userID)
	// The label row is committed; a retry would create a second one.
	eventbus.AppendBestEffort(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.LabelCreated,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		Payload:     map[string]any{"labelId": pub.String(), "via": "mcp"},
	}, "mcp.create_label")
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
	actor := int64(s.userID)
	// The junction row is committed and uniquely keyed, so a retry is
	// rejected before it could re-append the event.
	eventbus.AppendBestEffort(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.TaskLabelAdded,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &taskID64,
		Payload:     map[string]any{"taskId": in.TaskID, "labelId": in.LabelID, "via": "mcp"},
	}, "mcp.add_task_label")
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
	actor := int64(s.userID)
	// Propagated: disabling an already-disabled junction is a no-op, so
	// a retry re-appends the event without changing anything else.
	if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.TaskLabelRemoved,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &taskID64,
		Payload:     map[string]any{"taskId": in.TaskID, "labelId": in.LabelID, "via": "mcp"},
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
	if err := deps.Queries.ArchiveTask(ctx, generated.ArchiveTaskParams{
		UpdatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		WorkspaceID:     s.workspaceID,
		PublicID:        taskPub,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	actor := int64(s.userID)
	// Propagated: ArchiveTask only matches a not-yet-archived row, so a
	// retry is a no-op UPDATE that still re-appends the event.
	if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.TaskArchived,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		Payload:     map[string]any{"taskId": in.TaskID, "via": "mcp"},
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
	if err := deps.Queries.UnarchiveTask(ctx, generated.UnarchiveTaskParams{
		UpdatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		WorkspaceID:     s.workspaceID,
		PublicID:        taskPub,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	actor := int64(s.userID)
	// Propagated for the same reason as archive: the retry converges.
	if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.TaskUnarchived,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		Payload:     map[string]any{"taskId": in.TaskID, "via": "mcp"},
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{"ok": true}, nil
}
