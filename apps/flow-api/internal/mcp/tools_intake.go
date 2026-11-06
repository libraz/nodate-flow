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
	"github.com/libraz/nodate-flow/apps/flow-api/internal/sourceintake"
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
	// A replay returns the converted task under current visibility, as REST does.
	if item.TaskID.Valid {
		return convertedTaskResult(ctx, deps, s, item.TaskID)
	}

	// Converting writes a task into the target project, so the actor must be
	// a project editor (or workspace-elevated), matching intake.Convert.
	prjID, err := resolveProjectForWrite(ctx, deps, s, in.ProjectID)
	if err != nil {
		return nil, err
	}
	prjPub, err := types.Parse(in.ProjectID)
	if err != nil {
		return nil, apierrors.New(apierrors.WsProjectNotFound)
	}

	var (
		taskPub      types.PublicID
		taskID       int64
		answered     error
		replayTaskID sql.NullInt32
		adopted      sourceintake.LinkTarget
		lockedSignal generated.LockSignalForIntakeConversionRow
		signalLocked bool
		taskTitle    taskrules.Title
		taskBody     sql.NullString
	)
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.convert_intake_to_task", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		replayTaskID = sql.NullInt32{}
		adopted = sourceintake.LinkTarget{}
		taskPub = types.PublicID{}
		taskID = 0
		taskTitle = taskrules.Title{}
		taskBody = sql.NullString{}
		lockedSignal = generated.LockSignalForIntakeConversionRow{}
		signalLocked = false
		qtx := deps.Queries.WithTx(tx.RawTx())

		// An intake item is a workspace-level inbox entry with no audience of
		// its own, so the converted task takes the workspace default, exactly
		// as REST intake.Convert does. Establish the same lock order: project
		// allocation, optional source signal, then the current intake row.
		if err := taskcreate.LockProject(ctx, tx, s.workspaceID, prjID); err != nil {
			return err
		}
		if item.SignalID.Valid {
			locked, lockErr := qtx.LockSignalForIntakeConversion(ctx, generated.LockSignalForIntakeConversionParams{
				WorkspaceID: s.workspaceID,
				ID:          uint32(item.SignalID.Int32), //#nosec G115 -- signal_id is INT UNSIGNED and fits uint32 in supported deployments
			})
			if lockErr != nil && !stderrors.Is(lockErr, sql.ErrNoRows) {
				return lockErr
			}
			if lockErr == nil {
				lockedSignal = locked
				signalLocked = true
			}
		}

		current, err := qtx.FindIntakeItemForConversion(ctx, generated.FindIntakeItemForConversionParams{
			WorkspaceID: s.workspaceID,
			PublicID:    pub,
		})
		if err != nil {
			if stderrors.Is(err, sql.ErrNoRows) {
				answered = apierrors.New(apierrors.WsIntakeNotFound)
				return answered
			}
			return err
		}
		if current.SignalID.Valid {
			if !item.SignalID.Valid || current.SignalID.Int32 != item.SignalID.Int32 || !signalLocked {
				answered = apierrors.New(apierrors.WsInboxNotFound)
				return answered
			}
		} else {
			signalLocked = false
		}
		if current.TaskID.Valid {
			replayTaskID = current.TaskID
			return nil
		}
		if !sourceintake.Linkable(current.TriageStatus, current.SnoozeUntil) {
			answered = apierrors.New(apierrors.WsIntakeAlreadyTriaged)
			return answered
		}
		if signalLocked && lockedSignal.TaskID.Valid {
			signalTaskPub, err := qtx.FindPublicIdForIntakeTask(ctx, generated.FindPublicIdForIntakeTaskParams{
				WorkspaceID: s.workspaceID,
				ID:          uint32(lockedSignal.TaskID.Int32), //#nosec G115 -- task_id is INT UNSIGNED and fits uint32 in supported deployments
			})
			if err != nil {
				if stderrors.Is(err, sql.ErrNoRows) {
					answered = apierrors.New(apierrors.WsTaskNotFound)
					return answered
				}
				return err
			}
			target, err := sourceintake.LockLinkTarget(ctx, tx.RawTx(), s.workspaceID, s.userID, signalTaskPub)
			if err != nil {
				var apiError *apierrors.APIError
				if stderrors.As(err, &apiError) {
					answered = err
				}
				return err
			}
			linked, err := qtx.LinkIntakeItemToExistingTask(ctx, generated.LinkIntakeItemToExistingTaskParams{
				TaskID:          sql.NullInt32{Int32: int32(target.Task.ID), Valid: true}, //#nosec G115 -- tasks.id is INT UNSIGNED and fits int32 in supported deployments
				TriagedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true},       //#nosec G115 -- session user id is INT UNSIGNED and fits int32 in supported deployments
				WorkspaceID:     s.workspaceID,
				PublicID:        pub,
			})
			if err != nil {
				return err
			}
			if linked == 0 {
				answered = apierrors.New(apierrors.WsIntakeAlreadyConverted)
				return answered
			}
			adoptedTaskID := int64(target.Task.ID)
			payload := sourceintake.AcceptedPayload(pub, target.PublicID, target.ProjectID, lockedSignal.PublicID)
			payload["via"] = "mcp"
			if err := recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
				EventType:    eventbus.IntakeItemAccepted,
				AuditAction:  "intake.link_existing_task",
				ResourceType: "intake_item",
				ResourceID:   pub.String(),
				TaskID:       &adoptedTaskID,
				Payload:      payload,
				CallSite:     "mcp.convert_intake_to_task",
			}); err != nil {
				return err
			}
			adopted = target
			return nil
		}

		// Use the locked intake row for the task content so concurrent source
		// edits cannot make the event, task body and embedding disagree.
		taskTitle, err = taskrules.NewTitle(current.Title)
		if err != nil {
			answered = translateTaskRuleError(err)
			return answered
		}
		taskBody = current.Body
		created, err := taskcreate.New(ctx, tx, taskcreate.AuthoredBy(s.userID), taskcreate.Args{
			WorkspaceID: s.workspaceID,
			ProjectID:   prjID,
			Title:       taskTitle,
			Description: taskBody,
		})
		if err != nil {
			return err
		}
		taskPub = created.PublicID
		taskID = created.ID

		// The UPDATE matches only an unconverted item, so a conversion that
		// lost the race counts zero and rolls its task back with everything
		// else here.
		linked, err := qtx.SetIntakeItemTask(ctx, generated.SetIntakeItemTaskParams{
			TaskID:      sql.NullInt32{Int32: int32(created.ID), Valid: true}, //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			WorkspaceID: s.workspaceID,
			PublicID:    pub,
		})
		if err != nil {
			return err
		}
		if linked == 0 {
			answered = errConvertLostRace
			return answered
		}
		if signalLocked {
			linkedSignal, err := qtx.LinkInboxSignalToTask(ctx, generated.LinkInboxSignalToTaskParams{
				TaskID:      sql.NullInt32{Int32: int32(created.ID), Valid: true}, //#nosec G115 -- task_id is INT UNSIGNED and fits uint32 in supported deployments
				WorkspaceID: s.workspaceID,
				ID:          lockedSignal.ID,
			})
			if err != nil {
				return err
			}
			if linkedSignal == 0 {
				answered = apierrors.New(apierrors.WsIntakeAlreadyConverted)
				return answered
			}
		}
		// One call, two changes: the item is converted and a task comes into
		// existence. Both records join this transaction.
		rec := recorder(deps)
		var signalPub types.PublicID
		if signalLocked {
			signalPub = lockedSignal.PublicID
		}
		acceptedPayload := sourceintake.AcceptedPayload(pub, taskPub, prjPub, signalPub)
		acceptedPayload["via"] = "mcp"
		if err := rec.RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:    eventbus.IntakeItemAccepted,
			AuditAction:  "intake.convert",
			ResourceType: "intake_item",
			ResourceID:   pub.String(),
			TaskID:       &taskID,
			Payload:      acceptedPayload,
			CallSite:     "mcp.convert_intake_to_task",
		}); err != nil {
			return err
		}
		if err := rec.RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:    eventbus.TaskCreated,
			AuditAction:  "task.create",
			ResourceType: "task",
			ResourceID:   taskPub.String(),
			TaskID:       &taskID,
			Payload:      map[string]any{"taskId": taskPub.String(), "title": taskTitle.String(), "source": "intake_convert_mcp"},
			CallSite:     "mcp.convert_intake_to_task",
		}); err != nil {
			return err
		}
		if signalLocked {
			return rec.RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
				EventType:    eventbus.SignalAttached,
				AuditAction:  "signal.attach",
				ResourceType: "signal",
				ResourceID:   lockedSignal.PublicID.String(),
				TaskID:       &taskID,
				Payload: map[string]any{
					"signalId": lockedSignal.PublicID.String(),
					"taskId":   taskPub.String(),
					"source":   string(lockedSignal.Source),
					"kind":     lockedSignal.Kind,
				},
				CallSite: "mcp.convert_intake_to_task",
			})
		}
		return nil
	})
	if stderrors.Is(answered, errConvertLostRace) {
		current, err := deps.Queries.FindIntakeItemByPublicId(ctx, generated.FindIntakeItemByPublicIdParams{
			WorkspaceID: s.workspaceID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		if current.TaskID.Valid {
			return convertedTaskResult(ctx, deps, s, current.TaskID)
		}
		return nil, apierrors.New(apierrors.WsIntakeAlreadyConverted)
	}
	if answered != nil {
		return nil, answered
	}
	if txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}
	if replayTaskID.Valid {
		return convertedTaskResult(ctx, deps, s, replayTaskID)
	}
	if adopted.PublicID != (types.PublicID{}) {
		noteInvocationTask(ctx, adopted.Task.ID)
		return map[string]any{"ok": true, "taskId": adopted.PublicID.String()}, nil
	}

	noteInvocationTask(ctx, uint32(taskID)) //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
	// The conversion is a task creation, so the task starts with an
	// embedding like any other. The text is what the insert stored: the
	// trimmed title and the item's body.
	embed.RefreshTaskAfterCommit(ctx, deps.Embedder, s.workspaceID, uint32(taskID), taskTitle.String(), taskBody.String) //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments

	return map[string]any{"ok": true, "taskId": taskPub.String()}, nil
}

// errConvertLostRace rolls back a conversion whose intake was claimed by a
// concurrent one; the winner's task is then returned as a replay.
var errConvertLostRace = stderrors.New("mcp.convert_intake_to_task: lost race")

// convertedTaskResult answers a conversion replay with the task the intake
// already names, under the caller's current visibility.
func convertedTaskResult(ctx context.Context, deps Deps, s *session, taskID sql.NullInt32) (any, error) {
	taskPub, err := sourceintake.LinkedTask(ctx, deps.DB, deps.Queries, s.workspaceID, s.userID, taskID.Int32, apierrors.WsTaskNotFound)
	if err != nil {
		var apiError *apierrors.APIError
		if stderrors.As(err, &apiError) {
			return nil, err
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	noteInvocationTask(ctx, uint32(taskID.Int32)) //#nosec G115 -- task_id is INT UNSIGNED and fits uint32 in supported deployments
	return map[string]any{"ok": true, "taskId": taskPub.String()}, nil
}
