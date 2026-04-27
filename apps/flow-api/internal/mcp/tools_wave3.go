package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"strconv"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
)

func runListIntakeItems(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		Status string `json:"status"`
		Limit  int32  `json:"limit"`
		Offset int32  `json:"offset"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	rows, err := deps.Queries.ListIntakeItemsForWorkspace(ctx, generated.ListIntakeItemsForWorkspaceParams{
		WorkspaceID:  s.workspaceID,
		StatusFilter: generated.IntakeItemsTriageStatus(in.Status),
		Limit:        in.Limit,
		Offset:       in.Offset,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	type itemOut struct {
		ID           string   `json:"id"`
		Title        string   `json:"title"`
		TriageStatus string   `json:"triageStatus"`
		AIScore      *float64 `json:"aiScore,omitempty"`
		CreatedAt    int64    `json:"createdAt"`
	}
	out := make([]itemOut, 0, len(rows))
	for _, r := range rows {
		item := itemOut{
			ID:           r.PublicID.String(),
			Title:        r.Title,
			TriageStatus: string(r.TriageStatus),
			CreatedAt:    r.CreatedAt.Unix(),
		}
		if r.AiScore.Valid {
			if f, parseErr := strconv.ParseFloat(r.AiScore.String, 64); parseErr == nil {
				item.AIScore = &f
			}
		}
		out = append(out, item)
	}
	return map[string]any{"items": out}, nil
}

func runTriageIntakeItem(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		IntakeItemID string `json:"intakeItemId"`
		Status       string `json:"status"`
		SnoozeUntil  *int64 `json:"snoozeUntil"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.IntakeItemID == "" || in.Status == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	// Validate status value.
	switch in.Status {
	case "accepted", "rejected", "snoozed", "duplicate":
		// ok
	default:
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}

	pub, err := types.Parse(in.IntakeItemID)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}

	// Check the item exists and is still pending.
	existing, err := deps.Queries.FindIntakeItemByPublicId(ctx, generated.FindIntakeItemByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    pub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.WsIntakeNotFound)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	if existing.TriageStatus != generated.IntakeItemsTriageStatusPending {
		return nil, apierrors.New(apierrors.WsIntakeAlreadyTriaged)
	}

	var snoozeUntil sql.NullTime
	if in.Status == "snoozed" && in.SnoozeUntil != nil {
		snoozeUntil = sql.NullTime{Time: time.Unix(*in.SnoozeUntil, 0), Valid: true}
	}

	if err := deps.Queries.UpdateIntakeItemTriage(ctx, generated.UpdateIntakeItemTriageParams{
		TriageStatus:    generated.IntakeItemsTriageStatus(in.Status),
		TriagedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		SnoozeUntil:     snoozeUntil,
		WorkspaceID:     s.workspaceID,
		PublicID:        pub,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	// Map status to event kind.
	var evtKind eventbus.Kind
	switch in.Status {
	case "accepted":
		evtKind = eventbus.IntakeItemAccepted
	case "rejected":
		evtKind = eventbus.IntakeItemRejected
	case "snoozed":
		evtKind = eventbus.IntakeItemSnoozed
	case "duplicate":
		evtKind = eventbus.IntakeItemDuplicate
	}
	actor := int64(s.userID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        evtKind,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		Payload:     map[string]any{"intakeItemId": in.IntakeItemID, "status": in.Status, "via": "mcp"},
	})

	return map[string]any{"ok": true, "status": in.Status}, nil
}

func runConvertIntakeToTask(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		IntakeItemID string `json:"intakeItemId"`
		ProjectID    string `json:"projectId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.IntakeItemID == "" || in.ProjectID == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}

	pub, err := types.Parse(in.IntakeItemID)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}

	item, err := deps.Queries.FindIntakeItemByPublicId(ctx, generated.FindIntakeItemByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    pub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.WsIntakeNotFound)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	if item.TaskID.Valid {
		return nil, apierrors.New(apierrors.WsIntakeAlreadyConverted)
	}

	prjPub, err := types.Parse(in.ProjectID)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	prj, err := deps.Queries.FindProjectByPublicId(ctx, generated.FindProjectByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    prjPub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.WsProjectNotFound)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	taskPub := newPublicID()
	desc := sql.NullString{}
	if item.Body.Valid {
		desc = item.Body
	}

	tx, err := deps.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	defer tx.Rollback() //nolint:errcheck
	qtx := deps.Queries.WithTx(tx)

	nextNum, err := qtx.AssignTaskNumber(ctx, generated.AssignTaskNumberParams{
		WorkspaceID: prj.WorkspaceID,
		ProjectID:   prj.ID,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	taskID, err := qtx.CreateTask(ctx, generated.CreateTaskParams{
		PublicID:        taskPub,
		WorkspaceID:     s.workspaceID,
		ProjectID:       prj.ID,
		TaskNumber:      uint32(nextNum), //#nosec G115 -- task_number is per-project sequence, fits uint32
		ParentTaskID:    sql.NullInt32{},
		CreatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		UpdatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		Title:           item.Title,
		Description:     desc,
		Priority:        0,
		DueOn:           sql.NullTime{},
		StartedOn:       sql.NullTime{},
		Visibility:      generated.TasksVisibilityPublic,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	if err := qtx.SetIntakeItemTask(ctx, generated.SetIntakeItemTaskParams{
		TaskID:      sql.NullInt32{Int32: int32(taskID), Valid: true}, //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		WorkspaceID: s.workspaceID,
		PublicID:    pub,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	actor := int64(s.userID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.IntakeItemAccepted,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &taskID,
		Payload:     map[string]any{"intakeItemId": in.IntakeItemID, "taskId": taskPub.String(), "via": "mcp"},
	})
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.TaskCreated,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &taskID,
		Payload:     map[string]any{"taskId": taskPub.String(), "title": item.Title, "source": "intake_convert_mcp"},
	})

	return map[string]any{"ok": true, "taskId": taskPub.String()}, nil
}

func runListDescriptionVersions(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	taskInternal, _, err := resolveTask(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	rows, err := deps.Queries.ListDescriptionVersions(ctx, generated.ListDescriptionVersionsParams{
		WorkspaceID: s.workspaceID,
		TaskID:      taskInternal,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	type versionOut struct {
		ID                string `json:"id"`
		VersionNumber     int    `json:"versionNumber"`
		AuthorID          string `json:"authorId,omitempty"`
		AuthorDisplayName string `json:"authorDisplayName,omitempty"`
		BodyLength        int    `json:"bodyLength"`
		CreatedAt         int64  `json:"createdAt"`
	}
	out := make([]versionOut, 0, len(rows))
	for _, r := range rows {
		v := versionOut{
			ID:            r.PublicID.String(),
			VersionNumber: int(r.VersionNumber),
			BodyLength:    int(r.BodyLength),
			CreatedAt:     r.CreatedAt.Unix(),
		}
		var zero types.PublicID
		if r.AuthorPublicID != zero {
			v.AuthorID = r.AuthorPublicID.String()
		}
		if r.AuthorDisplayName.Valid {
			v.AuthorDisplayName = r.AuthorDisplayName.String
		}
		out = append(out, v)
	}
	return map[string]any{"versions": out}, nil
}

func runRestoreDescriptionVersion(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		TaskID    string `json:"taskId"`
		VersionID string `json:"versionId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.TaskID == "" || in.VersionID == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}

	taskInternal, taskPub, err := resolveTask(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}

	versionPub, err := types.Parse(in.VersionID)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}

	version, err := deps.Queries.FindDescriptionVersion(ctx, generated.FindDescriptionVersionParams{
		WorkspaceID: s.workspaceID,
		PublicID:    versionPub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.WsDescriptionVersionNotFound)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	tx, err := deps.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	defer tx.Rollback() //nolint:errcheck
	qtx := deps.Queries.WithTx(tx)

	// Get current task state for the UpdateTask call.
	taskRow, err := qtx.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    taskPub,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	if err := qtx.UpdateTask(ctx, generated.UpdateTaskParams{
		Title:           taskRow.Title,
		Description:     sql.NullString{String: version.Body, Valid: version.Body != ""},
		Priority:        taskRow.Priority,
		DueOn:           taskRow.DueOn,
		StartedOn:       taskRow.StartedOn,
		SortWeight:      taskRow.SortWeight,
		Visibility:      taskRow.Visibility,
		UpdatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		WorkspaceID:     s.workspaceID,
		PublicID:        taskPub,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	nextVer, err := qtx.NextDescriptionVersionNumber(ctx, taskInternal)
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	newPub := newPublicID()
	if _, err := qtx.CreateDescriptionVersion(ctx, generated.CreateDescriptionVersionParams{
		PublicID:      newPub,
		WorkspaceID:   s.workspaceID,
		TaskID:        taskInternal,
		AuthorUserID:  sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		VersionNumber: uint32(nextVer),                                    //#nosec G115 -- per-task version sequence, fits uint32
		Body:          version.Body,
		BodyLength:    uint32(len(version.Body)), //#nosec G115 -- description body length capped at 50KB by handler validation, fits uint32
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	actor := int64(s.userID)
	taskIDInt64 := int64(taskInternal)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.DescriptionVersionRestored,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &taskIDInt64,
		Payload:     map[string]any{"taskId": in.TaskID, "restoredFrom": in.VersionID, "newVersionId": newPub.String(), "via": "mcp"},
	})

	return map[string]any{"ok": true, "newVersionId": newPub.String()}, nil
}
