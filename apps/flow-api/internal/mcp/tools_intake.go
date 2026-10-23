// MCP tools over the workspace intake queue: listing items, triaging one,
// and converting an item into a task.

package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
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
	status := generated.IntakeItemsTriageStatus(in.Status)
	// An expired snooze is pending again, so the pending filter lists it
	// too, as REST's does.
	var includeExpiredSnoozes int64
	if status == generated.IntakeItemsTriageStatusPending {
		includeExpiredSnoozes = 1
	}
	rows, err := deps.Queries.ListIntakeItemsForWorkspace(ctx, generated.ListIntakeItemsForWorkspaceParams{
		WorkspaceID:           s.workspaceID,
		StatusFilter:          status,
		IncludeExpiredSnoozes: includeExpiredSnoozes,
		Limit:                 in.Limit,
		Offset:                in.Offset,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	// No AI score is reported: nothing scores an intake item, so the
	// column is never populated and a field for it would always be absent.
	type itemOut struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		TriageStatus string `json:"triageStatus"`
		CreatedAt    int64  `json:"createdAt"`
	}
	out := make([]itemOut, 0, len(rows))
	for _, r := range rows {
		out = append(out, itemOut{
			ID:           r.PublicID.String(),
			Title:        r.Title,
			TriageStatus: string(r.TriageStatus),
			CreatedAt:    r.CreatedAt.Unix(),
		})
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

	// A snooze is a deadline: once it passes, the item lists under pending
	// again and can be triaged. An item parked as snoozed with no deadline
	// would never come back, so the request is refused with the code REST
	// gives it. The check belongs with the other argument validation,
	// before the item is read.
	var snoozeUntil sql.NullTime
	if in.Status == "snoozed" {
		if in.SnoozeUntil == nil || *in.SnoozeUntil <= 0 {
			return nil, apierrors.New(apierrors.WsIntakeSnoozeDeadlineRequired)
		}
		if !dbtype.DatetimeRepresentable(*in.SnoozeUntil) {
			return nil, apierrors.New(apierrors.ValidationBodyFieldInvalid).WithDetail("field", "snoozeUntil")
		}
		snoozeUntil = sql.NullTime{Time: time.Unix(*in.SnoozeUntil, 0), Valid: true}
	}

	pub, err := types.Parse(in.IntakeItemID)
	if err != nil {
		return nil, apierrors.New(apierrors.WsIntakeNotFound)
	}
	if _, err := deps.Queries.FindIntakeItemByPublicId(ctx, generated.FindIntakeItemByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    pub,
	}); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.WsIntakeNotFound)
		}
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
	// The UPDATE matches only an item still pending or past its snooze, so
	// it is the claim: a losing concurrent triage, or a repeat, counts zero
	// rows and records nothing.
	var claimed bool
	if err := dbretry.InTx(ctx, deps.DB, "mcp.triage_intake_item", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		rows, err := deps.Queries.WithTx(tx.RawTx()).UpdateIntakeItemTriage(ctx, generated.UpdateIntakeItemTriageParams{
			TriageStatus:    generated.IntakeItemsTriageStatus(in.Status),
			TriagedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			SnoozeUntil:     snoozeUntil,
			WorkspaceID:     s.workspaceID,
			PublicID:        pub,
		})
		if err != nil {
			return err
		}
		if claimed = rows > 0; !claimed {
			return nil
		}
		return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:    evtKind,
			AuditAction:  "intake.triage",
			ResourceType: "intake_item",
			ResourceID:   pub.String(),
			Payload:      map[string]any{"intakeItemId": pub.String(), "status": in.Status, "via": "mcp"},
			CallSite:     "mcp.triage_intake_item",
		})
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	if !claimed {
		return nil, apierrors.New(apierrors.WsIntakeAlreadyTriaged)
	}
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
		return nil, apierrors.New(apierrors.WsIntakeNotFound)
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
	// A fast answer only: the conditional link UPDATE inside the
	// transaction is the claim.
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
		taskPub  types.PublicID
		taskID   int64
		answered error
	)
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.convert_intake_to_task", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
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

		// The UPDATE matches only an unconverted item, so a conversion that
		// lost the race counts zero and rolls its task back with everything
		// else here.
		linked, err := deps.Queries.WithTx(tx.RawTx()).SetIntakeItemTask(ctx, generated.SetIntakeItemTaskParams{
			TaskID:      sql.NullInt32{Int32: int32(created.ID), Valid: true}, //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			WorkspaceID: s.workspaceID,
			PublicID:    pub,
		})
		if err != nil {
			return err
		}
		if linked == 0 {
			answered = apierrors.New(apierrors.WsIntakeAlreadyConverted)
			return answered
		}
		// One call, two changes: the item is converted and a task comes into
		// existence. Both records join this transaction.
		rec := recorder(deps)
		if err := rec.RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:    eventbus.IntakeItemAccepted,
			AuditAction:  "intake.convert",
			ResourceType: "intake_item",
			ResourceID:   pub.String(),
			TaskID:       &taskID,
			Payload:      map[string]any{"intakeItemId": pub.String(), "taskId": taskPub.String(), "via": "mcp"},
			CallSite:     "mcp.convert_intake_to_task",
		}); err != nil {
			return err
		}
		return rec.RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:    eventbus.TaskCreated,
			AuditAction:  "task.create",
			ResourceType: "task",
			ResourceID:   taskPub.String(),
			TaskID:       &taskID,
			Payload:      map[string]any{"taskId": taskPub.String(), "title": title.String(), "source": "intake_convert_mcp"},
			CallSite:     "mcp.convert_intake_to_task",
		})
	})
	if answered != nil {
		return nil, answered
	}
	if txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}

	noteInvocationTask(ctx, uint32(taskID)) //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
	// The conversion is a task creation, so the task starts with an
	// embedding like any other. The text is what the insert stored: the
	// trimmed title and the item's body.
	embed.RefreshTaskAfterCommit(ctx, deps.Embedder, s.workspaceID, uint32(taskID), title.String(), item.Body.String) //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments

	return map[string]any{"ok": true, "taskId": taskPub.String()}, nil
}
