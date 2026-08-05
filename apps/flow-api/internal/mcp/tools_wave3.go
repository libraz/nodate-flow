package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"strconv"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
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
	// The triage status is committed, and the pending-status guard above
	// makes a retry fail with ALREADY_TRIAGED without re-appending. The
	// caller has no way to repair the log, so propagating would only
	// report a failure for work that succeeded.
	eventbus.AppendBestEffort(ctx, deps.DB, eventbus.Event{
		Type:        evtKind,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		Payload:     map[string]any{"intakeItemId": in.IntakeItemID, "status": in.Status, "via": "mcp"},
	}, "mcp.triage_intake_item")

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

	// Converting writes a task into the target project, so the actor must be
	// a project editor (or workspace-elevated), matching intake.Convert.
	prjID, err := resolveProjectForWrite(ctx, deps, s, in.ProjectID, acl.ProjectRoleEditor)
	if err != nil {
		return nil, err
	}

	var (
		taskPub types.PublicID
		taskID  int64
	)
	if txErr := dbretry.InTx(ctx, deps.DB, "mcp.convert_intake_to_task", nil, func(ctx context.Context, tx *sql.Tx) error {
		// An intake item is a workspace-level inbox entry with no audience of
		// its own, so the converted task takes the workspace default, exactly
		// as REST intake.Convert does.
		created, err := taskcreate.New(ctx, tx, taskcreate.Args{
			WorkspaceID: s.workspaceID,
			ProjectID:   prjID,
			ActorUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			Title:       item.Title,
			Description: item.Body,
		})
		if err != nil {
			return err
		}
		taskPub = created.PublicID
		taskID = created.ID

		return deps.Queries.WithTx(tx).SetIntakeItemTask(ctx, generated.SetIntakeItemTaskParams{
			TaskID:      sql.NullInt32{Int32: int32(created.ID), Valid: true}, //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			WorkspaceID: s.workspaceID,
			PublicID:    pub,
		})
	}); txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}

	actor := int64(s.userID)
	// The conversion committed a new task and linked the intake item; a
	// retry would create a second task.
	eventbus.AppendBestEffort(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.IntakeItemAccepted,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &taskID,
		Payload:     map[string]any{"intakeItemId": in.IntakeItemID, "taskId": taskPub.String(), "via": "mcp"},
	}, "mcp.convert_intake_to_task")
	eventbus.AppendBestEffort(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.TaskCreated,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &taskID,
		Payload:     map[string]any{"taskId": taskPub.String(), "title": item.Title, "source": "intake_convert_mcp"},
	}, "mcp.convert_intake_to_task")

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

	taskInternal, taskPub, err := resolveTaskForWrite(ctx, deps, s, in.TaskID, acl.ProjectRoleEditor)
	if err != nil {
		return nil, err
	}

	versionPub, err := types.Parse(in.VersionID)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}

	version, err := deps.Queries.FindDescriptionVersion(ctx, generated.FindDescriptionVersionParams{
		WorkspaceID: s.workspaceID,
		TaskID:      taskInternal,
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

	// Get current task state for the UpdateTask call. Access was already
	// authorized by resolveTask above; this transaction-scoped load reads
	// the row for a consistent update.
	taskRow, err := loadTaskRow(ctx, qtx, s.workspaceID, taskPub)
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
	// The restore committed a new description version row; a retry would
	// add another one on top of it.
	eventbus.AppendBestEffort(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.DescriptionVersionRestored,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &taskIDInt64,
		Payload:     map[string]any{"taskId": in.TaskID, "restoredFrom": in.VersionID, "newVersionId": newPub.String(), "via": "mcp"},
	}, "mcp.restore_description_version")

	return map[string]any{"ok": true, "newVersionId": newPub.String()}, nil
}
