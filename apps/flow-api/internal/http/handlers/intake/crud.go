package intake

import (
	"context"
	"database/sql"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// requireNonGuest rejects workspace guests. Guests have read-only access to
// the intake queue (list/get); creating, triaging, and converting items are
// write actions reserved for members and above. The workspace role is resolved
// upstream by RequireWorkspaceMember and carried on the request context.
func requireNonGuest(ws middleware.WorkspaceContext) *apierr.Spec {
	if !ws.Role.AtLeast(middleware.WorkspaceRoleMember) {
		return apierrors.WsMemberRoleDenied
	}
	return nil
}

// requireProjectEditor mirrors tasks.requireProjectEditor: converting an intake
// item into a task writes into the target project, so the actor must hold an
// editor (or higher) project role, or be a workspace-elevated admin/owner. The
// workspace role is already resolved by RequireWorkspaceMember and carried on
// the request context, so it is passed in rather than re-checked here.
func requireProjectEditor(ctx context.Context, db *sql.DB, wsID, prjID, actorID uint32, wsRole acl.WorkspaceRole) *apierr.Spec {
	prjRole, _, err := acl.LookupProjectMembership(ctx, db, wsID, prjID, actorID, wsRole)
	if err != nil {
		return apierr.SpecForErrNoRows(err, apierrors.WsProjectAccessDenied, apierrors.InternalUnexpected)
	}
	if prjRole == acl.ProjectRoleElevated || prjRole.AtLeast(acl.ProjectRoleEditor) {
		return nil
	}
	return apierrors.WsProjectAccessDenied
}

// Create handles POST /workspaces/{wsId}/intake.
func Create(deps Deps) func(context.Context, *CreateIntakeItemInput) (*CreateIntakeItemOutput, error) {
	return func(ctx context.Context, in *CreateIntakeItemInput) (*CreateIntakeItemOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		if spec := requireNonGuest(ws); spec != nil {
			return nil, httpErr(spec)
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

		eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(deps.DB), eventbus.Event{
			Type:        eventbus.IntakeItemCreated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload:     map[string]any{"intakeItemId": pub.String()},
		}, "intake.Create")

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
		status := generated.IntakeItemsTriageStatus(in.Status)

		if in.Cursor != "" {
			cursorAt, cursorPID, derr := handlerutil.DecodeCursor(in.Cursor)
			if derr != nil {
				return nil, httpErr(apierrors.ValidationQueryFieldInvalid)
			}
			rows, err := deps.Queries.ListIntakeItemsForWorkspaceKeyset(ctx, generated.ListIntakeItemsForWorkspaceKeysetParams{
				WorkspaceID:     ws.ID,
				StatusFilter:    status,
				CursorCreatedAt: sql.NullTime{Time: cursorAt, Valid: !cursorAt.IsZero()},
				CursorPublicID:  cursorPID,
				Limit:           limit + 1,
			})
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			hasMore := int32(len(rows)) > limit //#nosec G115 -- rows length capped at limit+1 with limit validated to maximum:200
			if hasMore {
				rows = rows[:limit]
			}
			out := &ListIntakeItemsOutput{}
			out.Body.Items = make([]Record, 0, len(rows))
			for _, r := range rows {
				out.Body.Items = append(out.Body.Items, mapKeysetListRow(r))
			}
			if hasMore {
				last := rows[len(rows)-1]
				nc := handlerutil.EncodeCursor(last.CreatedAt, last.PublicID)
				out.Body.NextCursor = &nc
			}
			return out, nil
		}

		rows, err := deps.Queries.ListIntakeItemsForWorkspace(ctx, generated.ListIntakeItemsForWorkspaceParams{
			WorkspaceID:  ws.ID,
			StatusFilter: status,
			Limit:        limit,
			Offset:       in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListIntakeItemsOutput{}
		out.Body.Items = make([]Record, 0, len(rows))
		for _, r := range rows {
			out.Body.Items = append(out.Body.Items, mapListRow(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
			if int64(in.Offset+limit) < out.Body.Total {
				last := rows[len(rows)-1]
				nc := handlerutil.EncodeCursor(last.CreatedAt, last.PublicID)
				out.Body.NextCursor = &nc
			}
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
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsIntakeNotFound, apierrors.InternalUnexpected))
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
		if spec := requireNonGuest(ws); spec != nil {
			return nil, httpErr(spec)
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
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsIntakeNotFound, apierrors.InternalUnexpected))
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

		// Not an existence check: re-triaging an item to the status it
		// already holds changes nothing and MySQL counts zero. The item is
		// re-read below and that read is what fails if it is gone.
		if _, err := deps.Queries.UpdateIntakeItemTriage(ctx, generated.UpdateIntakeItemTriageParams{
			TriageStatus:    generated.IntakeItemsTriageStatus(in.Body.Status),
			TriagedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
			SnoozeUntil:     snoozeUntil,
			WorkspaceID:     ws.ID,
			PublicID:        pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		triageType := triageEventKind(in.Body.Status)
		eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(deps.DB), eventbus.Event{
			Type:        triageType,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload:     map[string]any{"intakeItemId": pub.String(), "status": in.Body.Status},
		}, "intake.Triage")

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
		if spec := requireNonGuest(ws); spec != nil {
			return nil, httpErr(spec)
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
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsIntakeNotFound, apierrors.InternalUnexpected))
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
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
		}

		actorID, _ := middleware.ActorFromContext(ctx)

		// Converting writes a task into the target project, so the actor must
		// be a project editor (or workspace-elevated), matching tasks.Create.
		if spec := requireProjectEditor(ctx, deps.DB, ws.ID, prj.ID, actorID, ws.Role); spec != nil {
			return nil, httpErr(spec)
		}

		// Create the task inside a transaction for task-number safety.
		// Number allocation locks the project counter, so two conversions
		// racing on the same project can deadlock; InTx re-runs the whole
		// unit rather than failing the request.
		var (
			taskID  int64
			taskPub types.PublicID
		)
		if err := dbretry.InTx(ctx, deps.DB, "intake.Convert", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			qtx := deps.Queries.WithTx(tx.RawTx())

			// An intake item is a workspace-level inbox entry with no audience of
			// its own, so the converted task takes the workspace default.
			created, err := taskcreate.New(ctx, tx, taskcreate.Args{
				WorkspaceID: ws.ID,
				ProjectID:   prj.ID,
				ActorUserID: sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
				Title:       item.Title,
				Description: sql.NullString{String: nullStr(item.Body), Valid: item.Body.Valid},
			})
			if err != nil {
				return err
			}
			taskID = created.ID
			taskPub = created.PublicID

			// Link intake item to the created task.
			// The item was resolved earlier in this transaction and the task it
			// is being linked to was just inserted, so the count adds nothing the
			// transaction does not already guarantee.
			_, err = qtx.SetIntakeItemTask(ctx, generated.SetIntakeItemTaskParams{
				TaskID:      sql.NullInt32{Int32: int32(taskID), Valid: true}, //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
				WorkspaceID: ws.ID,
				PublicID:    pub,
			})
			return err
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(deps.DB), eventbus.Event{
			Type:        eventbus.IntakeItemAccepted,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskID,
			Payload: map[string]any{
				"intakeItemId": pub.String(),
				"taskId":       taskPub.String(),
				"projectId":    prjPub.String(),
			},
		}, "intake.Convert")

		eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(deps.DB), eventbus.Event{
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
		}, "intake.Convert")

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
