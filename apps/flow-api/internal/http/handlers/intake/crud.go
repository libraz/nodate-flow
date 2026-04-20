package intake

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// Create handles POST /workspaces/{wsId}/intake.
func Create(deps Deps) func(context.Context, *CreateIntakeItemInput) (*CreateIntakeItemOutput, error) {
	return func(ctx context.Context, in *CreateIntakeItemInput) (*CreateIntakeItemOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		pub := types.New()
		body := sql.NullString{String: in.Body.Body, Valid: in.Body.Body != ""}

		if _, err := deps.Queries.CreateIntakeItem(ctx, generated.CreateIntakeItemParams{
			PublicID:    pub,
			WorkspaceID: ws.ID,
			SignalID:    sql.NullInt32{},
			Title:       in.Body.Title,
			Body:        body,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.IntakeItemCreated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload:     map[string]any{"intakeItemId": pub.String()},
		})

		if deps.Audit != nil {
			if actorID, ok := middleware.ActorFromContext(ctx); ok {
				deps.Audit.Record(ctx, audit.Entry{
					Action:       "intake.create",
					ActorID:      actorID,
					WorkspaceID:  ws.ID,
					ResourceType: "intake_item",
					ResourceID:   pub.String(),
					Metadata:     map[string]any{"title": in.Body.Title},
				})
			}
		}

		row, err := deps.Queries.FindIntakeItemByPublicId(ctx, generated.FindIntakeItemByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &CreateIntakeItemOutput{Body: mapFindRow(row)}, nil
	}
}

// List handles GET /workspaces/{wsId}/intake.
func List(deps Deps) func(context.Context, *ListIntakeItemsInput) (*ListIntakeItemsOutput, error) {
	return func(ctx context.Context, in *ListIntakeItemsInput) (*ListIntakeItemsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		rows, err := deps.Queries.ListIntakeItemsForWorkspace(ctx, generated.ListIntakeItemsForWorkspaceParams{
			WorkspaceID:  ws.ID,
			StatusFilter: generated.IntakeItemsTriageStatus(in.Status),
			Limit:        limit,
			Offset:       in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListIntakeItemsOutput{}
		out.Body.Items = make([]IntakeItem, 0, len(rows))
		for _, r := range rows {
			out.Body.Items = append(out.Body.Items, mapListRow(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// Get handles GET /workspaces/{wsId}/intake/{id}.
func Get(deps Deps) func(context.Context, *GetIntakeItemInput) (*GetIntakeItemOutput, error) {
	return func(ctx context.Context, in *GetIntakeItemInput) (*GetIntakeItemOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		pub, err := types.Parse(in.ID)
		if err != nil {
			return nil, httpErr(apierrors.WsIntakeNotFound)
		}

		row, err := deps.Queries.FindIntakeItemByPublicId(ctx, generated.FindIntakeItemByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsIntakeNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &GetIntakeItemOutput{Body: mapFindRow(row)}, nil
	}
}

// triageEventKind maps a triage status string to its event bus kind.
func triageEventKind(status string) eventbus.Kind {
	switch status {
	case "accepted":
		return eventbus.IntakeItemAccepted
	case "rejected":
		return eventbus.IntakeItemRejected
	case "snoozed":
		return eventbus.IntakeItemSnoozed
	case "duplicate":
		return eventbus.IntakeItemDuplicate
	default:
		return eventbus.IntakeItemAccepted
	}
}

// Triage handles PATCH /workspaces/{wsId}/intake/{id}.
func Triage(deps Deps) func(context.Context, *TriageIntakeItemInput) (*TriageIntakeItemOutput, error) {
	return func(ctx context.Context, in *TriageIntakeItemInput) (*TriageIntakeItemOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		pub, err := types.Parse(in.ID)
		if err != nil {
			return nil, httpErr(apierrors.WsIntakeNotFound)
		}

		existing, err := deps.Queries.FindIntakeItemByPublicId(ctx, generated.FindIntakeItemByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsIntakeNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Reject if already triaged (not pending).
		if existing.TriageStatus != generated.IntakeItemsTriageStatusPending {
			return nil, httpErr(apierrors.WsIntakeAlreadyTriaged)
		}

		actorID, _ := middleware.ActorFromContext(ctx)

		var snoozeUntil sql.NullTime
		if in.Body.Status == "snoozed" && in.Body.SnoozeUntil != nil {
			snoozeUntil = sql.NullTime{Time: time.Unix(*in.Body.SnoozeUntil, 0), Valid: true}
		}

		if err := deps.Queries.UpdateIntakeItemTriage(ctx, generated.UpdateIntakeItemTriageParams{
			TriageStatus:    generated.IntakeItemsTriageStatus(in.Body.Status),
			TriagedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true},
			SnoozeUntil:     snoozeUntil,
			WorkspaceID:     ws.ID,
			PublicID:        pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        triageEventKind(in.Body.Status),
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload:     map[string]any{"intakeItemId": pub.String(), "status": in.Body.Status},
		})

		if deps.Audit != nil {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "intake.triage",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "intake_item",
				ResourceID:   pub.String(),
				Metadata:     map[string]any{"status": in.Body.Status},
			})
		}

		row, err := deps.Queries.FindIntakeItemByPublicId(ctx, generated.FindIntakeItemByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &TriageIntakeItemOutput{Body: mapFindRow(row)}, nil
	}
}

// Convert handles POST /workspaces/{wsId}/intake/{id}/convert.
func Convert(deps Deps) func(context.Context, *ConvertIntakeItemInput) (*ConvertIntakeItemOutput, error) {
	return func(ctx context.Context, in *ConvertIntakeItemInput) (*ConvertIntakeItemOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		pub, err := types.Parse(in.ID)
		if err != nil {
			return nil, httpErr(apierrors.WsIntakeNotFound)
		}

		item, err := deps.Queries.FindIntakeItemByPublicId(ctx, generated.FindIntakeItemByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsIntakeNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Prevent double conversion.
		if item.TaskID.Valid {
			return nil, httpErr(apierrors.WsIntakeAlreadyConverted)
		}

		// Resolve project.
		prjPub, err := types.Parse(in.Body.ProjectID)
		if err != nil {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		prj, err := deps.Queries.FindProjectByPublicId(ctx, generated.FindProjectByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    prjPub,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsProjectNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		actorID, _ := middleware.ActorFromContext(ctx)

		taskPub := types.New()
		desc := sql.NullString{String: nullStr(item.Body), Valid: item.Body.Valid}

		// Create the task inside a transaction for task-number safety.
		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer tx.Rollback() //nolint:errcheck
		qtx := deps.Queries.WithTx(tx)

		nextNum, err := qtx.AssignTaskNumber(ctx, prj.ID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		taskID, err := qtx.CreateTask(ctx, generated.CreateTaskParams{
			PublicID:        taskPub,
			WorkspaceID:     ws.ID,
			ProjectID:       prj.ID,
			TaskNumber:      uint32(nextNum),
			ParentTaskID:    sql.NullInt32{},
			CreatedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true},
			Title:           item.Title,
			Description:     desc,
			Priority:        0,
			DueOn:           sql.NullTime{},
			StartedOn:       sql.NullTime{},
			EventOn:         sql.NullTime{},
			Visibility:      generated.TasksVisibilityPublic,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Link intake item to the created task.
		if err := qtx.SetIntakeItemTask(ctx, generated.SetIntakeItemTaskParams{
			TaskID:      sql.NullInt32{Int32: int32(taskID), Valid: true},
			WorkspaceID: ws.ID,
			PublicID:    pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.IntakeItemAccepted,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskID,
			Payload: map[string]any{
				"intakeItemId": pub.String(),
				"taskId":       taskPub.String(),
				"projectId":    prjPub.String(),
			},
		})

		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskCreated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskID,
			Payload: map[string]any{
				"taskId":    taskPub.String(),
				"projectId": prjPub.String(),
				"title":     item.Title,
				"source":    "intake_convert",
			},
		})

		if deps.Audit != nil {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "intake.convert",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "intake_item",
				ResourceID:   pub.String(),
				Metadata:     map[string]any{"taskId": taskPub.String()},
			})
		}

		out := &ConvertIntakeItemOutput{}
		out.Body.Ok = true
		out.Body.TaskID = taskPub.String()
		return out, nil
	}
}
