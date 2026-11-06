package intake

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/sourceintake"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// actor reads the workspace and the acting user off the request
// context as the one value both halves of a record are stamped with.
//
// The actor is resolved unconditionally. Recording it was previously
// conditional on the context carrying one, which meant an
// actor-less request appended the event and skipped the audit row —
// exactly the half-recorded change the mutation log exists to prevent.
// A missing actor is a NULL column, not a reason to record nothing.
func actor(ctx context.Context, ws middleware.WorkspaceContext) mutationlog.Actor {
	actorID, _ := middleware.ActorFromContext(ctx)
	return mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}
}

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

		// The row is committed on its own connection by now, so the
		// record is written after the fact and never fails the request.
		deps.Mutations.Record(ctx, actor(ctx, ws), mutationlog.Mutation{
			EventType:    eventbus.IntakeItemCreated,
			AuditAction:  "intake.create",
			ResourceType: "intake_item",
			ResourceID:   pub.String(),
			Payload:      map[string]any{"intakeItemId": pub.String(), "title": in.Body.Title},
			CallSite:     "intake.Create",
		})

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
		// An expired snooze is pending again, so the pending filter
		// lists it too.
		var includeExpiredSnoozes int64
		if status == generated.IntakeItemsTriageStatusPending {
			includeExpiredSnoozes = 1
		}

		if in.Cursor != "" {
			cursorAt, cursorPID, derr := handlerutil.DecodeCursor(in.Cursor)
			if derr != nil {
				return nil, httpErr(apierrors.ValidationQueryFieldInvalid)
			}
			rows, err := deps.Queries.ListIntakeItemsForWorkspaceKeyset(ctx, generated.ListIntakeItemsForWorkspaceKeysetParams{
				WorkspaceID:           ws.ID,
				StatusFilter:          status,
				IncludeExpiredSnoozes: includeExpiredSnoozes,
				CursorCreatedAt:       sql.NullTime{Time: cursorAt, Valid: !cursorAt.IsZero()},
				CursorPublicID:        cursorPID,
				Limit:                 limit + 1,
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
			WorkspaceID:           ws.ID,
			StatusFilter:          status,
			IncludeExpiredSnoozes: includeExpiredSnoozes,
			Limit:                 limit,
			Offset:                in.Offset,
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

// errAnswered rolls a transaction back once the handler has decided the
// response itself. It is not transient, so dbretry does not retry it.
var errAnswered = errors.New("intake: answered")

// triageable reports whether an item can leave the queue: it is pending,
// or snoozed with a deadline that has passed.
func triageable(status generated.IntakeItemsTriageStatus, snoozeUntil sql.NullTime) bool {
	switch status {
	case generated.IntakeItemsTriageStatusPending:
		return true
	case generated.IntakeItemsTriageStatusSnoozed:
		return snoozeUntil.Valid && !snoozeUntil.Time.After(time.Now())
	default:
		return false
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

		// A snooze is a deadline: once it passes, the item lists under
		// pending again and can be triaged. An item parked as snoozed with
		// a NULL snooze_until has no date at which that happens, so it is
		// lost rather than deferred — and a non-positive deadline says the
		// same thing, since epoch or earlier is not a date anything waits
		// for. The check sits with
		// the other argument validation, before the item is read, so a
		// malformed call does not depend on the item existing. The MCP
		// triage tool refuses the same inputs.
		//
		// The bound is not a schema `minimum` on the field: it is
		// conditional on the status, which a struct tag cannot express,
		// and a partial tag would answer one malformed snooze with two
		// different codes.
		var snoozeUntil sql.NullTime
		if in.Body.Status == "snoozed" {
			if in.Body.SnoozeUntil == nil || *in.Body.SnoozeUntil <= 0 {
				return nil, httpErr(apierrors.WsIntakeSnoozeDeadlineRequired)
			}
			if err := handlerutil.CheckDatetimeField("snoozeUntil", in.Body.SnoozeUntil); err != nil {
				return nil, err
			}
			snoozeUntil = sql.NullTime{Time: time.Unix(*in.Body.SnoozeUntil, 0), Valid: true}
		}
		// A deadline sent with any other status is dropped rather than
		// stored: only a snoozed item has something to resurface from, and
		// the column is written unconditionally by the update below.

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

		// Reject if already triaged. An expired snooze is pending again.
		// A fast answer only: the conditional UPDATE below is the claim.
		if !triageable(existing.TriageStatus, existing.SnoozeUntil) {
			return nil, httpErr(apierrors.WsIntakeAlreadyTriaged)
		}

		actorID, _ := middleware.ActorFromContext(ctx)

		// The UPDATE matches only an item still pending (or past its
		// snooze), so a losing concurrent triage counts zero rows and
		// records nothing.
		var claimed bool
		if err := dbretry.InTx(ctx, deps.DB, "intake.Triage", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			rows, err := generated.New(tx).UpdateIntakeItemTriage(ctx, generated.UpdateIntakeItemTriageParams{
				TriageStatus:    generated.IntakeItemsTriageStatus(in.Body.Status),
				TriagedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
				SnoozeUntil:     snoozeUntil,
				WorkspaceID:     ws.ID,
				PublicID:        pub,
			})
			if err != nil {
				return err
			}
			if claimed = rows > 0; !claimed {
				return nil
			}
			// The event kind is chosen from the decision, so it is the one
			// mutation here the static check cannot read off the literal;
			// triageEventKind is total over the enum the schema accepts.
			return deps.Mutations.RecordInTx(ctx, tx, actor(ctx, ws), mutationlog.Mutation{
				EventType:    triageEventKind(in.Body.Status),
				AuditAction:  "intake.triage",
				ResourceType: "intake_item",
				ResourceID:   pub.String(),
				Payload:      map[string]any{"intakeItemId": pub.String(), "status": in.Body.Status},
				CallSite:     "intake.Triage",
			})
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		row, err := deps.Queries.FindIntakeItemByPublicId(ctx, generated.FindIntakeItemByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsIntakeNotFound, apierrors.InternalUnexpected))
		}
		if !claimed {
			return nil, httpErr(apierrors.WsIntakeAlreadyTriaged)
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

		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}

		// A replay returns the one converted task under current visibility, ignoring new arguments.
		if item.TaskID.Valid {
			return existingConvertedTask(ctx, deps, ws.ID, actorID, item.TaskID)
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

		// Converting writes a task into the target project, so the actor must
		// be a project editor (or workspace-elevated), matching tasks.Create.
		if spec := handlerutil.RequireProjectRole(ctx, deps.DB, ws.ID, prj.ID, actorID, acl.ProjectRoleEditor); spec != nil {
			return nil, httpErr(spec)
		}

		// Create the task inside a transaction for task-number safety.
		// Number allocation locks the project counter, so two conversions
		// racing on the same project can deadlock; InTx re-runs the whole
		// unit rather than failing the request.
		var (
			taskID       int64
			taskPub      types.PublicID
			replayTaskID sql.NullInt32
			title        taskrules.Title
			description  sql.NullString
			dueOn        sql.NullTime
			lockedSignal generated.LockSignalForIntakeConversionRow
			signalLocked bool
			signalLinked bool
			adoptedTask  types.PublicID
			answered     error
			lostRace     bool
			validationFn func() error
		)
		act := actor(ctx, ws)
		txErr := dbretry.InTx(ctx, deps.DB, "intake.Convert", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			lostRace = false
			validationFn = nil
			replayTaskID = sql.NullInt32{}
			taskID = 0
			taskPub = types.PublicID{}
			title = taskrules.Title{}
			description = sql.NullString{}
			dueOn = sql.NullTime{}
			signalLocked = false
			signalLinked = false
			adoptedTask = types.PublicID{}
			lockedSignal = generated.LockSignalForIntakeConversionRow{}
			qtx := deps.Queries.WithTx(tx.RawTx())

			// Project counter first, then signal, intake and any existing task: capture's order after the counter.
			if err := taskcreate.LockProject(ctx, tx, ws.ID, prj.ID); err != nil {
				return err
			}
			if item.SignalID.Valid {
				locked, lockErr := qtx.LockSignalForIntakeConversion(ctx, generated.LockSignalForIntakeConversionParams{
					WorkspaceID: ws.ID,
					ID:          uint32(item.SignalID.Int32), //#nosec G115 -- signal_id is INT UNSIGNED and fits uint32 in supported deployments
				})
				if lockErr != nil && !errors.Is(lockErr, sql.ErrNoRows) {
					return lockErr
				}
				if lockErr == nil {
					lockedSignal = locked
					signalLocked = true
				}
			}

			// The locking read supersedes the earlier unlocked one.
			current, err := qtx.FindIntakeItemForConversion(ctx, generated.FindIntakeItemForConversionParams{
				WorkspaceID: ws.ID,
				PublicID:    pub,
			})
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					answered = httpErr(apierrors.WsIntakeNotFound)
					return errAnswered
				}
				return err
			}
			if current.SignalID.Valid {
				if !item.SignalID.Valid || current.SignalID.Int32 != item.SignalID.Int32 || !signalLocked {
					answered = httpErr(apierrors.WsInboxNotFound)
					return errAnswered
				}
			} else {
				signalLocked = false
			}
			if current.TaskID.Valid {
				replayTaskID = current.TaskID
				return nil
			}
			if !sourceintake.Linkable(current.TriageStatus, current.SnoozeUntil) {
				answered = httpErr(apierrors.WsIntakeAlreadyTriaged)
				return errAnswered
			}
			if signalLocked && lockedSignal.TaskID.Valid {
				target, err := adoptSignalTask(ctx, qtx, tx, ws.ID, actorID, lockedSignal)
				if err != nil {
					var apiError *apierr.APIError
					if errors.As(err, &apiError) {
						answered = handlerutil.HTTPErrFromAPIError(apiError)
						return errAnswered
					}
					return err
				}
				linked, err := qtx.LinkIntakeItemToExistingTask(ctx, generated.LinkIntakeItemToExistingTaskParams{
					TaskID:          sql.NullInt32{Int32: int32(target.Task.ID), Valid: true}, //#nosec G115 -- tasks.id is INT UNSIGNED and fits int32 in supported deployments
					TriagedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true},        //#nosec G115 -- actor id is INT UNSIGNED and fits int32 in supported deployments
					WorkspaceID:     ws.ID,
					PublicID:        pub,
				})
				if err != nil {
					return err
				}
				if linked == 0 {
					answered = httpErr(apierrors.WsIntakeAlreadyConverted)
					return errAnswered
				}
				adoptedTaskID := int64(target.Task.ID)
				if err := deps.Mutations.RecordInTx(ctx, tx, act, mutationlog.Mutation{
					EventType:    eventbus.IntakeItemAccepted,
					AuditAction:  "intake.link_existing_task",
					ResourceType: "intake_item",
					ResourceID:   pub.String(),
					TaskID:       &adoptedTaskID,
					Payload:      sourceintake.AcceptedPayload(pub, target.PublicID, target.ProjectID, lockedSignal.PublicID),
					CallSite:     "intake.Convert",
				}); err != nil {
					return err
				}
				adoptedTask = target.PublicID
				return nil
			}

			titleText := current.Title
			if in.Body.Title != "" {
				titleText = in.Body.Title
			}
			title, err = taskrules.NewTitle(titleText)
			if err != nil {
				validationFn = func() error { return httpErr(apierrors.ValidationBodyFieldInvalid) }
				return errConvertValidation
			}
			description = current.Body
			if in.Body.Body != nil {
				if utf8.RuneCountInString(*in.Body.Body) > 50_000 {
					validationFn = func() error { return httpErr(apierrors.ValidationBodyFieldInvalid) }
					return errConvertValidation
				}
				description = sql.NullString{String: *in.Body.Body, Valid: *in.Body.Body != ""}
			}
			dueOn, err = parseIntakeDueOn(in.Body.DueOn)
			if err != nil {
				validationFn = func() error { return httpErr(apierrors.ValidationBodyDateFormatInvalid) }
				return errConvertValidation
			}

			attribution := taskcreate.AuthoredBy(actorID)
			if len(in.Body.AssigneeUserIDs) > 50 {
				validationFn = func() error { return httpErr(apierrors.ValidationBodyFieldInvalid) }
				return errConvertValidation
			}
			if len(in.Body.AssigneeUserIDs) > 0 {
				// Read after the project lock so taskcreate allocates from a fresh snapshot.
				actors := make([]taskcreate.Actor, 0, len(in.Body.AssigneeUserIDs))
				seen := make(map[uint32]struct{}, len(in.Body.AssigneeUserIDs))
				for _, userPublicID := range in.Body.AssigneeUserIDs {
					userPub, err := types.Parse(userPublicID)
					if err != nil {
						validationFn = func() error { return httpErr(apierrors.WsMemberNotFound) }
						return errConvertValidation
					}
					userID, err := qtx.FindWorkspaceMemberUserInternalIdByPublicId(ctx, generated.FindWorkspaceMemberUserInternalIdByPublicIdParams{
						WorkspaceID: ws.ID,
						PublicID:    userPub,
					})
					if err != nil {
						if errors.Is(err, sql.ErrNoRows) {
							validationFn = func() error { return httpErr(apierrors.WsMemberNotFound) }
							return errConvertValidation
						}
						return err
					}
					if _, duplicate := seen[userID]; duplicate {
						validationFn = func() error { return httpErr(apierrors.ValidationBodyFieldInvalid) }
						return errConvertValidation
					}
					seen[userID] = struct{}{}
					actors = append(actors, taskcreate.Actor{UserID: userID, Role: generated.TaskActorsRoleAssignee})
				}
				attribution = attribution.WithActors(actors...)
			}

			created, err := taskcreate.New(ctx, tx, attribution, taskcreate.Args{
				WorkspaceID: ws.ID,
				ProjectID:   prj.ID,
				Title:       title,
				Description: description,
				DueOn:       dueOn,
			})
			if err != nil {
				return err
			}
			taskID = created.ID
			taskPub = created.PublicID

			// Link intake item to the created task. The UPDATE matches only
			// an unconverted item, so a conversion that lost the race counts
			// zero and rolls its task back with everything else here.
			linked, err := qtx.SetIntakeItemTask(ctx, generated.SetIntakeItemTaskParams{
				TaskID:      sql.NullInt32{Int32: int32(taskID), Valid: true}, //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
				WorkspaceID: ws.ID,
				PublicID:    pub,
			})
			if err != nil {
				return err
			}
			if linked == 0 {
				lostRace = true
				answered = httpErr(apierrors.WsIntakeAlreadyConverted)
				return errAnswered
			}
			if signalLocked {
				linkedSignal, err := qtx.LinkInboxSignalToTask(ctx, generated.LinkInboxSignalToTaskParams{
					TaskID:      sql.NullInt32{Int32: int32(taskID), Valid: true}, //#nosec G115 -- task_id is tasks.id (INT UNSIGNED), fits int32 in supported deployments
					WorkspaceID: ws.ID,
					ID:          lockedSignal.ID,
				})
				if err != nil {
					return err
				}
				if linkedSignal == 0 {
					answered = httpErr(apierrors.WsIntakeAlreadyConverted)
					return errAnswered
				}
				signalLinked = true
			}

			// One request, two changes: the item is triaged and a task
			// comes into existence. They are recorded separately because
			// they are separate changes to whoever reads either log — a
			// task board watching task.created has no reason to know the
			// task came from intake.
			//
			// Both records join this transaction. Recording them after it
			// would leave a window where the task exists and nothing says
			// so, and would let a rolled-back conversion still be
			// answered by an audit query. The audit rows are registered
			// on the commit, so an attempt that rolls back takes its own
			// records with it.
			var signalPub types.PublicID
			if signalLocked {
				signalPub = lockedSignal.PublicID
			}
			if err := deps.Mutations.RecordInTx(ctx, tx, act, mutationlog.Mutation{
				EventType:    eventbus.IntakeItemAccepted,
				AuditAction:  "intake.convert",
				ResourceType: "intake_item",
				ResourceID:   pub.String(),
				TaskID:       &taskID,
				Payload:      sourceintake.AcceptedPayload(pub, taskPub, prjPub, signalPub),
				CallSite:     "intake.Convert",
			}); err != nil {
				return err
			}
			if err := deps.Mutations.RecordInTx(ctx, tx, act, mutationlog.Mutation{
				EventType:    eventbus.TaskCreated,
				AuditAction:  "task.create",
				ResourceType: "task",
				ResourceID:   taskPub.String(),
				TaskID:       &taskID,
				// The task's normalized title, not the raw request value:
				// taskrules.NewTitle trims and validates it before storage.
				// The project is named by the id the row holds,
				// not by the string the request sent, so the two logs
				// cannot disagree about which project it landed in.
				Payload: map[string]any{
					"taskId":    taskPub.String(),
					"projectId": prjPub.String(),
					"title":     title.String(),
					"source":    "intake_convert",
				},
				CallSite: "intake.Convert",
			}); err != nil {
				return err
			}
			if signalLinked {
				return deps.Mutations.RecordInTx(ctx, tx, act, mutationlog.Mutation{
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
					CallSite: "intake.Convert",
				})
			}
			return nil
		})
		if answered != nil {
			if lostRace {
				current, err := deps.Queries.FindIntakeItemByPublicId(ctx, generated.FindIntakeItemByPublicIdParams{
					WorkspaceID: ws.ID,
					PublicID:    pub,
				})
				if err == nil && current.TaskID.Valid {
					return existingConvertedTask(ctx, deps, ws.ID, actorID, current.TaskID)
				}
				if err != nil {
					return nil, httpErr(apierrors.InternalUnexpected)
				}
			}
			return nil, answered
		}
		if replayTaskID.Valid {
			return existingConvertedTask(ctx, deps, ws.ID, actorID, replayTaskID)
		}
		if txErr == nil && adoptedTask != (types.PublicID{}) {
			out := &ConvertIntakeItemOutput{}
			out.Body.Ok = true
			out.Body.TaskID = adoptedTask.String()
			return out, nil
		}
		if errors.Is(txErr, errConvertValidation) && validationFn != nil {
			return nil, validationFn()
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// The embedded text matches the stored title and description after
		// title validation, so search cannot index characters the task lacks.
		embed.RefreshTaskAfterCommit(ctx, deps.Embedder, ws.ID, uint32(taskID), title.String(), description.String) //#nosec G115 -- LastInsertId for tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments

		out := &ConvertIntakeItemOutput{}
		out.Body.Ok = true
		out.Body.TaskID = taskPub.String()
		return out, nil
	}
}

var errConvertValidation = errors.New("intake.Convert: validation failed")

func parseIntakeDueOn(value string) (sql.NullTime, error) {
	if value == "" {
		return sql.NullTime{}, nil
	}
	date, err := time.Parse("2006-01-02", value)
	if err != nil {
		return sql.NullTime{}, err
	}
	return sql.NullTime{Time: date, Valid: true}, nil
}

func existingConvertedTask(ctx context.Context, deps Deps, workspaceID, actorID uint32, taskID sql.NullInt32) (*ConvertIntakeItemOutput, error) {
	taskPub, err := sourceintake.LinkedTask(ctx, deps.DB, deps.Queries, workspaceID, actorID, taskID.Int32, apierrors.WsTaskAccessDenied)
	if err != nil {
		var apiError *apierr.APIError
		if errors.As(err, &apiError) {
			return nil, handlerutil.HTTPErrFromAPIError(apiError)
		}
		return nil, httpErr(apierrors.InternalUnexpected)
	}
	out := &ConvertIntakeItemOutput{}
	out.Body.Ok = true
	out.Body.TaskID = taskPub.String()
	return out, nil
}

// adoptSignalTask locks and authorizes the task a source signal already
// names, so an intake converted from that signal joins it instead of
// creating a second task.
func adoptSignalTask(
	ctx context.Context,
	qtx *generated.Queries,
	tx *dbretry.Tx,
	workspaceID, actorID uint32,
	signal generated.LockSignalForIntakeConversionRow,
) (sourceintake.LinkTarget, error) {
	taskPub, err := qtx.FindPublicIdForIntakeTask(ctx, generated.FindPublicIdForIntakeTaskParams{
		WorkspaceID: workspaceID,
		ID:          uint32(signal.TaskID.Int32), //#nosec G115 -- task_id is INT UNSIGNED and fits uint32 in supported deployments
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sourceintake.LinkTarget{}, apierrors.New(apierrors.WsTaskNotFound)
		}
		return sourceintake.LinkTarget{}, err
	}
	return sourceintake.LockLinkTarget(ctx, tx.RawTx(), workspaceID, actorID, taskPub)
}
