package tasks

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/execution"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskupdate"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// errCreateValidation is the sentinel returned from the dbretry.InTx
// callback inside Create when a validation failure (unparseable id,
// unknown member, invalid role) is encountered. It is non-transient so
// dbretry skips the retry loop; the outer handler then dispatches the
// captured validation closure to translate the failure into the right
// problem+json envelope.
var errCreateValidation = errors.New("tasks.Create: validation failed")

// errAnswered rolls a transaction back once the handler has decided the
// response itself. It is not transient, so dbretry does not retry it.
var errAnswered = errors.New("tasks: answered")

// translateTaskRuleError renders a [taskrules] refusal as the
// problem+json envelope the REST contract answers for it. The rules
// package states what was violated and stops there, because MCP answers
// the same violations differently; this is the REST half of that split.
func translateTaskRuleError(err error) error {
	switch taskrules.Classify(err) {
	case taskrules.ViolationNone:
		return nil
	case taskrules.ViolationTitleEmpty:
		return httpErr(apierrors.ValidationBodyFieldInvalid)
	case taskrules.ViolationDueBeforeStart:
		return httpErr(apierrors.ValidationBodyDueBeforeStart)
	}
	return httpErr(apierrors.InternalUnexpected)
}

// translateActorRoleError converts an apierror returned by
// [parseActorRole] into the canonical problem+json envelope so the
// `received_role` detail surfaces through ProblemDetails extensions.
// Falls back to a generic WS.TASK.ACTOR_ROLE_INVALID envelope when the
// caller hands in something other than an *apierr.APIError, which
// shouldn't happen in practice but keeps the helper total.
func translateActorRoleError(err error) error {
	var apiErr *apierr.APIError
	if errors.As(err, &apiErr) {
		return handlerutil.HTTPErrFromAPIError(apiErr)
	}
	return httpErr(apierrors.WsTaskActorRoleInvalid)
}

// parseActorRole validates the `role` field on a task-actor body and
// converts it to the sqlc-generated enum type. The OpenAPI schema
// already restricts the field to the catalog enum, but the Huma
// validator only runs on declared inputs — older clients or
// hand-rolled JSON could still slip an unknown role through to the
// SQL boundary, where MySQL would emit a 1265 "Data truncated" error
// and the handler would surface it as a generic 500.
//
// Returning a typed apierror means the same payload from any code
// path (CRUD create, AddActor, AddAgentActor) lands on
// WS.TASK.ACTOR_ROLE_INVALID with HTTP 422 and the offending value
// echoed back in `received_role`.
func parseActorRole(s string) (generated.TaskActorsRole, error) {
	switch generated.TaskActorsRole(s) {
	case generated.TaskActorsRoleAssignee,
		generated.TaskActorsRoleReviewer,
		generated.TaskActorsRoleWatcher,
		generated.TaskActorsRoleApprover:
		return generated.TaskActorsRole(s), nil
	default:
		return "", apierr.New(apierrors.WsTaskActorRoleInvalid).WithDetail("received_role", s)
	}
}

func parseDateOrNullTime(s string) (sql.NullTime, error) {
	if s == "" {
		return sql.NullTime{}, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return sql.NullTime{}, err
	}
	return sql.NullTime{Time: t, Valid: true}, nil
}

// actorPtr delegates to handlerutil.ActorPtr.
var actorPtr = handlerutil.ActorPtr

// Create handles POST /tasks. The acting workspace and project are
// resolved from the projectId in the body via FindProjectByPublicIdGlobal.
func Create(deps Deps) func(context.Context, *CreateTaskInput) (*CreateTaskOutput, error) {
	return func(ctx context.Context, in *CreateTaskInput) (*CreateTaskOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskAccessDenied)
		}
		// Checked before the project lookup: a blank title with an
		// unknown project id is answered as a validation error, and
		// moving the check past the lookup would answer not-found.
		title, err := taskrules.NewTitle(in.Body.Title)
		if err != nil {
			return nil, translateTaskRuleError(err)
		}
		prjPub, err := types.Parse(in.Body.ProjectID)
		if err != nil {
			return nil, httpErr(apierrors.WsProjectNotFound)
		}
		prj, err := deps.Queries.FindProjectByPublicIdGlobal(ctx, prjPub)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
		}
		// Project editor check (handler-level since /tasks has no workspace
		// path parameter to attach RequireProjectRole to).
		if spec := handlerutil.RequireProjectRole(ctx, deps.DB, prj.WorkspaceID, prj.ID, actorID, acl.ProjectRoleEditor); spec != nil {
			return nil, httpErr(spec)
		}

		due, err := parseDateOrNullTime(in.Body.DueOn)
		if err != nil {
			return nil, httpErr(apierrors.ValidationBodyDateFormatInvalid)
		}
		start, err := parseDateOrNullTime(in.Body.StartOn)
		if err != nil {
			return nil, httpErr(apierrors.ValidationBodyDateFormatInvalid)
		}
		if err := taskrules.DateOrder(due, start); err != nil {
			return nil, translateTaskRuleError(err)
		}

		var pub types.PublicID
		desc := sql.NullString{String: in.Body.Description, Valid: in.Body.Description != ""}

		// Run the create transaction inside a deadlock-aware retry
		// wrapper. The tx body acquires FK record locks against
		// workspaces, projects, users, and the project-row lock used by
		// task-number allocation, so it can deadlock with concurrent
		// transitions and fan-out under heavy parallel test load.
		// Restarting the whole tx on ER_LOCK_DEADLOCK is the standard
		// MySQL recipe.
		var (
			taskID       int64
			validationFn func() error
		)
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.Create", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			validationFn = nil
			qtx := deps.Queries.WithTx(tx.RawTx())

			// When the caller passed no explicit actor list we auto-attach
			// them as the sole `assignee` so the task shows up on their
			// /me/tasks feeds. An explicit non-empty list is treated as
			// authoritative, and the creator is recorded without being
			// attached.
			attr := taskcreate.SelfAssigned(actorID)
			if len(in.Body.Actors) > 0 {
				// Resolving reads, and the first read of a transaction is
				// what fixes its snapshot, so the project lock has to come
				// before it or the task number is allocated from a stale
				// view of the project.
				if lerr := taskcreate.LockProject(ctx, tx, prj.WorkspaceID, prj.ID); lerr != nil {
					return lerr
				}
				actors := make([]taskcreate.Actor, 0, len(in.Body.Actors))
				for _, a := range in.Body.Actors {
					userPub, perr := types.Parse(a.UserID)
					if perr != nil {
						validationFn = func() error { return httpErr(apierrors.WsMemberNotFound) }
						return errCreateValidation
					}
					// Resolve scoped to the task's workspace so an explicit actor
					// list cannot attach a user from another tenant.
					uid, lerr := qtx.FindWorkspaceMemberUserInternalIdByPublicId(ctx, generated.FindWorkspaceMemberUserInternalIdByPublicIdParams{
						WorkspaceID: prj.WorkspaceID,
						PublicID:    userPub,
					})
					if lerr != nil {
						if errors.Is(lerr, sql.ErrNoRows) {
							validationFn = func() error { return httpErr(apierrors.WsMemberNotFound) }
							return errCreateValidation
						}
						return lerr
					}
					role := generated.TaskActorsRoleAssignee
					if a.Role != "" {
						parsed, perr := parseActorRole(a.Role)
						if perr != nil {
							capturedErr := perr
							validationFn = func() error { return translateActorRoleError(capturedErr) }
							return errCreateValidation
						}
						role = parsed
					}
					actors = append(actors, taskcreate.Actor{UserID: uid, Role: role})
				}
				attr = taskcreate.AuthoredBy(actorID).WithActors(actors...)
			}

			created, err := taskcreate.New(ctx, tx, attr, taskcreate.Args{
				WorkspaceID: prj.WorkspaceID,
				ProjectID:   prj.ID,
				Title:       title,
				Description: desc,
				Priority:    in.Body.Priority,
				DueOn:       due,
				StartedOn:   start,
				// Empty body visibility means "workspace default"; taskcreate
				// substitutes it so the default lives in one place.
				Visibility: generated.TasksVisibility(in.Body.Visibility),
			})
			if err != nil {
				if errors.Is(err, taskcreate.ErrVisibilityInvalid) {
					validationFn = func() error { return httpErr(apierrors.ValidationBodyFieldInvalid) }
					return errCreateValidation
				}
				return err
			}
			tID := created.ID
			// The public id is minted per attempt, so a retried transaction
			// never commits an id a previous attempt already published.
			pub = created.PublicID
			taskID = tID

			// Record the creation inside the same tx so a crash between
			// commit and a post-commit append cannot lose the timeline
			// row; the audit row follows the commit. On a deadlock retry
			// the whole tx (including this append) rolls back and re-runs,
			// so no duplicate row is committed.
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: prj.WorkspaceID}, mutationlog.Mutation{
				EventType:    eventbus.TaskCreated,
				AuditAction:  "task.create",
				ResourceType: "task",
				ResourceID:   pub.String(),
				TaskID:       &taskID,
				Payload: map[string]any{
					"taskId":    pub.String(),
					"projectId": prjPub.String(),
					"title":     title.String(),
				},
				CallSite: "tasks.Create",
			})
		})
		if validationFn != nil {
			return nil, validationFn()
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		embed.RefreshTaskAfterCommit(ctx, deps.Embedder, prj.WorkspaceID, uint32(taskID), title.String(), in.Body.Description) //#nosec G115 -- LastInsertId for tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments

		row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: prj.WorkspaceID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &CreateTaskOutput{Body: rowToTaskFromFind(row)}, nil
	}
}

// Get handles GET /tasks/{id}.
func Get(deps Deps) func(context.Context, *GetTaskInput) (*GetTaskOutput, error) {
	return func(ctx context.Context, _ *GetTaskInput) (*GetTaskOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsTaskNotFound, apierrors.InternalUnexpected))
		}
		return &GetTaskOutput{Body: rowToTaskFromFind(row)}, nil
	}
}

// Patch handles PATCH /tasks/{id}. derived_state is intentionally not
// writable here; the constraint engine and event bus mutate it.
//
// The route takes a date, not a window. When the task carries a
// projection event, itemkit mirrors the new date onto it by re-anchoring
// the event's own duration — the stored end minus the stored start, which
// the database's own chronology CHECK keeps non-negative — to that date,
// and shifts both ends together for a working-day snap. So the pair
// written is ordered by construction and holds nothing the caller sent:
//
// calendar-precondition: chronology not-applicable — this route sends a date and no window, and the pair it mirrors keeps the stored event's own duration
func Patch(deps Deps) func(context.Context, *PatchTaskInput) (*PatchTaskOutput, error) {
	return func(ctx context.Context, in *PatchTaskInput) (*PatchTaskOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		// Resolve the actor once so itemkit calls (which refuse a zero
		// actor) and the downstream audit entry stay consistent. The auth
		// middleware is expected to have populated this; a missing actor
		// here is a 401, not a 404.
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthTokenMissingOrMalformed)
		}
		// Dates in the body are parsed before the transaction; everything
		// merged against the stored row happens inside it.
		patch := taskupdate.Patch{
			Title:       in.Body.Title,
			Description: in.Body.Description,
			Priority:    in.Body.Priority,
			SortWeight:  in.Body.SortWeight,
		}
		for _, d := range []struct {
			body *string
			dst  **sql.NullTime
		}{
			{in.Body.DueOn, &patch.DueOn},
			{in.Body.StartOn, &patch.StartOn},
			{in.Body.ExpectedDueOn, &patch.ExpectedDueOn},
		} {
			if d.body == nil {
				continue
			}
			parsed, err := parseDateOrNullTime(*d.body)
			if err != nil {
				return nil, httpErr(apierrors.ValidationBodyDateFormatInvalid)
			}
			*d.dst = &parsed
		}
		if in.Body.Visibility != nil {
			v := generated.TasksVisibility(*in.Body.Visibility)
			patch.Visibility = &v
		}

		actor := mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}
		var (
			result   taskupdate.Result
			answered error
			replayed bool
			output   PatchTaskOutput
		)
		// One transaction for the idempotency receipt, the update and the
		// response the receipt stores.
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.Patch", taskMutationTxOptions(in.IdempotencyKey), func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			replayed = false
			receipt, isReplay, err := lockTaskMutation(ctx, tx, ws, task, actorID,
				execution.CapabilityEditTask, "task.patch", in.IdempotencyKey, in.Body, &output.Body)
			if err != nil {
				return err
			}
			if isReplay {
				replayed = true
				return nil
			}
			result, err = taskupdate.Apply(ctx, tx, taskupdate.Args{
				WorkspaceID: ws.ID,
				TaskID:      task.ID,
				PublicID:    types.FromUUID(task.PublicID),
				Patch:       patch,
				Actor:       actor,
			})
			if err != nil {
				answered = translateTaskUpdateError(err)
				return err
			}
			if in.IdempotencyKey == "" {
				return nil
			}
			row, err := deps.Queries.WithTx(tx.RawTx()).FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
				WorkspaceID: ws.ID,
				PublicID:    types.FromUUID(task.PublicID),
			})
			if err != nil {
				return err
			}
			output.Body = rowToTaskFromFind(row)
			return completeTaskMutation(ctx, tx, receipt, output.Body)
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			return nil, taskMutationHTTPError(txErr)
		}
		if replayed {
			return &output, nil
		}
		deps.Mutations.RecordTxAudit(ctx, actor, mutationlog.Mutation{
			AuditAction:  "task.update",
			ResourceType: "task",
			ResourceID:   task.PublicID.String(),
			Payload:      result.Payload,
			CallSite:     "tasks.Patch (taskupdate.Apply appended the event)",
		})
		// Only text that actually moved invalidates the vector: a refresh
		// costs a provider call the workspace pays for.
		if result.TitleChanged || result.DescChanged {
			embed.RefreshTaskAfterCommit(ctx, deps.Embedder, ws.ID, task.ID, result.NewTitle.String(), nullStr(result.NewDesc))
		}

		if in.IdempotencyKey == "" {
			row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
				WorkspaceID: ws.ID,
				PublicID:    types.FromUUID(task.PublicID),
			})
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			output.Body = rowToTaskFromFind(row)
		}
		return &output, nil
	}
}

// translateTaskUpdateError renders a [taskupdate.Apply] refusal as the
// problem+json envelope PATCH answers with. Errors it does not recognise
// return nil, leaving the transaction error to taskMutationHTTPError.
func translateTaskUpdateError(err error) error {
	var ike *taskupdate.ItemkitError
	switch {
	case errors.Is(err, taskupdate.ErrTaskNotFound):
		return httpErr(apierrors.WsTaskNotFound)
	case errors.Is(err, taskupdate.ErrDueOnConflict):
		return httpErr(apierrors.WsTaskUpdateConflict)
	case errors.As(err, &ike):
		return translateItemkitTaskError(ike.Err)
	case taskrules.Classify(err) != taskrules.ViolationOther:
		return translateTaskRuleError(err)
	}
	return nil
}

// translateItemkitTaskError converts an itemkit invariant into the
// public error code so PATCH /tasks returns 422 instead of 500 when
// the call violates a cross-table invariant. Thin alias over the
// shared classifier in handlerutil so the task and calendar
// translators stay in lockstep on which itemkit messages map to
// public codes.
var translateItemkitTaskError = handlerutil.TranslateTaskItemkitError

// Disable handles DELETE /tasks/{id}. The write routes through
// itemkit.DeleteTask so any linked calendar_events cascade
// soft-disabled in the same transaction.
func Disable(deps Deps) func(context.Context, *DisableTaskInput) (*DisableTaskOutput, error) {
	return func(ctx context.Context, _ *DisableTaskInput) (*DisableTaskOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		actorID, _ := middleware.ActorFromContext(ctx)
		// Wrap the delete in dbretry.InTx: itemkit.DeleteTask appends
		// item.deleted (and a legacy task.disabled) event row inside
		// the same tx, which competes for FK locks with concurrent
		// transitions and fan-out goroutines under heavy parallel
		// load. Restarting the whole transaction on ER_LOCK_DEADLOCK
		// is the canonical MySQL recipe.
		//
		// We pass the raw error through to dbretry so it can detect
		// the transient mysql code; only after the retry budget is
		// exhausted (or the error is non-transient) do we translate
		// the result into a problem+json envelope.
		var (
			rawErr  error
			deleted bool
		)
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.Disable", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			rawErr = nil
			var err error
			deleted, err = itemkit.DeleteTask(ctx, tx, ws.ID, task.ID, actorID)
			if err != nil {
				rawErr = err
				return err
			}
			return nil
		})
		if txErr != nil {
			if rawErr != nil {
				return nil, translateItemkitTaskError(rawErr)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		// A repeat or losing concurrent delete changed nothing, so it
		// records nothing and still answers ok.
		if deleted {
			deps.Mutations.RecordTxAudit(ctx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				AuditAction:  "task.delete",
				ResourceType: "task",
				ResourceID:   task.PublicID.String(),
				CallSite:     "tasks.Disable (itemkit.DeleteTask appended the event)",
			})
		}
		// No task.disabled append here: itemkit.DeleteTask already emits
		// item.deleted plus its legacy task.disabled dual-emit inside the
		// tx above (see itemkit.legacyKindFor). A second post-commit append
		// here duplicated the task.disabled timeline row for one delete and
		// risked losing it on a crash between commit and append.
		out := &DisableTaskOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
