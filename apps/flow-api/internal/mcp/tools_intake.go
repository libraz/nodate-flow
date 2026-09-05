// MCP tools over the workspace intake queue: listing items, triaging one,
// and converting an item into a task.

package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"strconv"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
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

	// A snooze is a deadline, and nothing resurfaces an item without one:
	// the queue is filtered by triage_status alone, so an item parked as
	// snoozed with a NULL snooze_until leaves the pending list and has no
	// date at which anything brings it back. Accepting that silently was
	// worse than refusing it, because the tool reported success. The check
	// belongs with the other argument validation, before the item is read:
	// a malformed call should not depend on the item existing.
	var snoozeUntil sql.NullTime
	if in.Status == "snoozed" {
		if in.SnoozeUntil == nil || *in.SnoozeUntil <= 0 {
			return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
		}
		snoozeUntil = sql.NullTime{Time: time.Unix(*in.SnoozeUntil, 0), Valid: true}
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

	// Not an existence check: the item is read into `existing` above,
	// which is also what rejects one already triaged.
	if _, err := deps.Queries.UpdateIntakeItemTriage(ctx, generated.UpdateIntakeItemTriageParams{
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
	// The triage status is committed, and the pending-status guard above
	// makes a retry fail with ALREADY_TRIAGED without re-appending. The
	// caller has no way to repair the log, so propagating would only
	// report a failure for work that succeeded.
	recordMutation(ctx, deps, s, mutation{
		EventType:    evtKind,
		AuditAction:  "intake.triage",
		ResourceType: "intake_item",
		ResourceID:   in.IntakeItemID,
		Payload:      map[string]any{"intakeItemId": in.IntakeItemID, "status": in.Status, "via": "mcp"},
		CallSite:     "mcp.triage_intake_item",
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

	// Converting writes a task into the target project, so the actor must be
	// a project editor (or workspace-elevated), matching intake.Convert.
	prjID, err := resolveProjectForWrite(ctx, deps, s, in.ProjectID)
	if err != nil {
		return nil, err
	}

	// The converted task takes the item's title, so it faces the rule
	// every other task title faces. An item whose title is blank has
	// nothing to convert into.
	title, err := taskrules.NewTitle(item.Title)
	if err != nil {
		return nil, translateTaskRuleError(err)
	}

	var (
		taskPub types.PublicID
		taskID  int64
	)
	if txErr := dbretry.InTx(ctx, deps.DB, "mcp.convert_intake_to_task", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		// An intake item is a workspace-level inbox entry with no audience of
		// its own, so the converted task takes the workspace default, exactly
		// as REST intake.Convert does.
		created, err := taskcreate.New(ctx, tx, taskcreate.AuthoredBy(s.userID), taskcreate.Args{
			WorkspaceID: s.workspaceID,
			ProjectID:   prjID,
			Title:       title,
			Description: item.Body,
		})
		if err != nil {
			return err
		}
		taskPub = created.PublicID
		taskID = created.ID

		// The item was resolved before this transaction and the task it is
		// being linked to was just inserted, so the count adds nothing the
		// transaction does not already guarantee.
		_, linkErr := deps.Queries.WithTx(tx.RawTx()).SetIntakeItemTask(ctx, generated.SetIntakeItemTaskParams{
			TaskID:      sql.NullInt32{Int32: int32(created.ID), Valid: true}, //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			WorkspaceID: s.workspaceID,
			PublicID:    pub,
		})
		return linkErr
	}); txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}

	noteInvocationTask(ctx, uint32(taskID)) //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
	// The conversion committed a new task and linked the intake item; a
	// retry would create a second task.
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.IntakeItemAccepted,
		AuditAction:  "intake.convert",
		ResourceType: "intake_item",
		ResourceID:   in.IntakeItemID,
		TaskID:       &taskID,
		Payload:      map[string]any{"intakeItemId": in.IntakeItemID, "taskId": taskPub.String(), "via": "mcp"},
		CallSite:     "mcp.convert_intake_to_task",
	})
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.TaskCreated,
		AuditAction:  "task.create",
		ResourceType: "task",
		ResourceID:   taskPub.String(),
		TaskID:       &taskID,
		Payload:      map[string]any{"taskId": taskPub.String(), "title": title.String(), "source": "intake_convert_mcp"},
		CallSite:     "mcp.convert_intake_to_task",
	})
	// The conversion is a task creation, so the task starts with an
	// embedding like any other. The text is what the insert stored: the
	// trimmed title and the item's body.
	embed.RefreshTaskAfterCommit(ctx, deps.Embedder, s.workspaceID, uint32(taskID), title.String(), item.Body.String) //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments

	return map[string]any{"ok": true, "taskId": taskPub.String()}, nil
}
