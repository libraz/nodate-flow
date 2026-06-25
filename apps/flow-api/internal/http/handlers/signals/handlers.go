package signals

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/resolve"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/signalkinds"
)

// resolveWorkspaceByPublic loads the internal workspaces.id for a
// public UUID without any membership check. Used by the service-token
// path of POST /signals where there is no actor user; the worker
// declares the workspace scope in the request body. A missing or
// disabled workspace returns WS.WORKSPACE.NOT_FOUND so existence and
// permission failures are indistinguishable to the caller.
func resolveWorkspaceByPublic(ctx context.Context, db *sql.DB, wsPublic string) (uint32, error) {
	if wsPublic == "" {
		return 0, httpErr(apierrors.WsWorkspaceNotFound)
	}
	pub, err := types.Parse(wsPublic)
	if err != nil {
		return 0, httpErr(apierrors.WsWorkspaceNotFound)
	}
	const q = `SELECT id FROM workspaces WHERE public_id = ? AND enabled = TRUE LIMIT 1`
	var wsID uint32
	if err := db.QueryRowContext(ctx, q, pub).Scan(&wsID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, httpErr(apierrors.WsWorkspaceNotFound)
		}
		return 0, httpErr(apierrors.InternalUnexpected)
	}
	return wsID, nil
}

// resolveTaskInWorkspace loads the internal task id for a public UUID,
// constrained to the given workspace. Returns (0, false, nil) when the
// task does not exist; the caller decides whether that is an error.
func resolveTaskInWorkspace(ctx context.Context, db *sql.DB, wsID uint32, taskPublic string) (int64, bool, error) {
	pub, err := types.Parse(taskPublic)
	if err != nil {
		return 0, false, nil
	}
	const q = `SELECT id FROM tasks WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
	var id int64
	if err := db.QueryRowContext(ctx, q, wsID, pub).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return id, true, nil
}

// resolveUserInWorkspace loads the internal user id for a public UUID,
// constrained to enabled membership in the given workspace. Returns
// (0, false, nil) when the user is not a member; cross-tenant existence
// stays hidden.
func resolveUserInWorkspace(ctx context.Context, db *sql.DB, wsID uint32, userPublic string) (int64, bool, error) {
	pub, err := types.Parse(userPublic)
	if err != nil {
		return 0, false, nil
	}
	const q = `SELECT u.id FROM users u
		INNER JOIN workspace_members wm ON wm.user_id = u.id AND wm.enabled = TRUE
		WHERE wm.workspace_id = ? AND u.public_id = ? AND u.enabled = TRUE LIMIT 1`
	var id int64
	if err := db.QueryRowContext(ctx, q, wsID, pub).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return id, true, nil
}

// resolveCalendarEventInWorkspace loads the internal calendar_events.id for
// a public UUID, constrained to the given workspace. Returns (0, false, nil)
// when the event does not exist or belongs to a different tenant.
func resolveCalendarEventInWorkspace(ctx context.Context, db *sql.DB, wsID uint32, eventPublic string) (int64, bool, error) {
	pub, err := types.Parse(eventPublic)
	if err != nil {
		return 0, false, nil
	}
	const q = `SELECT id FROM calendar_events WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
	var id int64
	if err := db.QueryRowContext(ctx, q, wsID, pub).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return id, true, nil
}

// resolveSignalByDedupeKey loads the public_id of the existing signals row
// that collided on the workspace-scoped dedupe key (workspace_id, source,
// external_id). It is only meaningful when external_id is non-NULL, because
// MySQL treats NULL as distinct in a UNIQUE key so a NULL-external_id INSERT
// never collides. Returns (zero, false, nil) when no matching row exists; a
// genuine miss here means the INSERT IGNORE was a no-op for a reason other
// than the dedupe key and the caller must not fabricate a public id.
func resolveSignalByDedupeKey(ctx context.Context, db *sql.DB, wsID uint32, source generated.SignalsSource, externalID sql.NullString) (types.PublicID, bool, error) {
	if !externalID.Valid {
		return types.PublicID{}, false, nil
	}
	const q = `SELECT public_id FROM signals
		WHERE workspace_id = ? AND source = ? AND external_id = ? LIMIT 1`
	var pub types.PublicID
	if err := db.QueryRowContext(ctx, q, wsID, source, externalID.String).Scan(&pub); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.PublicID{}, false, nil
		}
		return types.PublicID{}, false, err
	}
	return pub, true, nil
}

// Create handles POST /signals. Most callers authenticate as a real
// user via JWT / PAT / MCP, in which case the actor must be an enabled
// member of the target workspace. The endpoint also accepts the
// flow-api signal token (NF_FLOW_API_SIGNAL_TOKEN) for trusted
// internal callers; service-token requests have no actor user id and
// resolve the workspace solely by the public id in the request body.
//
// Validation order (each step short-circuits):
//  1. workspace lookup (and membership when authenticated as a user);
//  2. `kind` must exist in the signalkinds registry — else
//     WS.SIGNAL.KIND_UNKNOWN;
//  3. when `taskId` and `(subjectType=task, subjectId)` are both present,
//     they must point at the same task — else WS.SIGNAL.SUBJECT_MISMATCH;
//  4. when `subjectId` is present, the referenced row must exist in the
//     workspace — else WS.SIGNAL.SUBJECT_NOT_FOUND (404 hides existence).
//
// The audit log records ActorID=0 for service-token requests so the
// downstream audit reader can distinguish system writes from user
// writes without leaking the worker's identity.
func Create(deps Deps) func(context.Context, *CreateInput) (*CreateOutput, error) {
	return func(ctx context.Context, in *CreateInput) (*CreateOutput, error) {
		mode, _ := middleware.AuthModeFromContext(ctx)
		var (
			actorID uint32
			wsID    uint32
			err     error
		)
		if mode == middleware.AuthModeServiceToken {
			// Service-token path: no actor user, but the workspace must
			// still exist (and be enabled). The lookup mirrors the
			// internal half of resolve.WorkspaceMember without the
			// member check.
			wsID, err = resolveWorkspaceByPublic(ctx, deps.DB, in.Body.WorkspaceID)
			if err != nil {
				return nil, err
			}
		} else {
			var ok bool
			actorID, ok = middleware.ActorFromContext(ctx)
			if !ok {
				return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
			}
			wsID, err = resolve.WorkspaceMember(ctx, deps.DB, in.Body.WorkspaceID, actorID)
			if err != nil {
				return nil, err
			}
		}

		// Step 2 — closed-enum kind validation against signal_kinds/*.yaml.
		// The registry is the single source of truth (ADR 0008 D2) and is
		// regenerated from YAML by `make gen-signal-kinds`.
		if _, known := signalkinds.Lookup(signalkinds.Kind(in.Body.Kind)); !known {
			return nil, httpErr(apierrors.WsSignalKindUnknown)
		}

		// Legacy task linkage.
		var taskFK sql.NullInt32
		var taskInternal int64
		var taskLinked bool
		if in.Body.TaskID != "" {
			id, found, terr := resolveTaskInWorkspace(ctx, deps.DB, wsID, in.Body.TaskID)
			if terr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			if !found {
				return nil, httpErr(apierrors.WsTaskNotFound)
			}
			taskFK = sql.NullInt32{Int32: int32(id), Valid: true} //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			taskInternal = id
			taskLinked = true
		}

		// Resolve subject_type: explicit override > kind default > workspace
		// fallback. resolveSubjectType always returns a non-empty enum value
		// so signals.subject_type NOT NULL is satisfied. The legacy taskId
		// fast path implicitly promotes subject_type to "task" when the
		// caller did not name a different subject — the two addressing
		// shapes describe the same row so they must agree.
		subjectType := resolveSubjectType(in.Body.Kind, in.Body.SubjectType)
		if taskLinked && in.Body.SubjectType == "" {
			subjectType = generated.SignalsSubjectTypeTask
		}

		// Step 4 — when SubjectID is present, resolve the internal row and
		// short-circuit with WS.SIGNAL.SUBJECT_NOT_FOUND on miss. The
		// resolver enforces workspace scope so cross-tenant ids leak nothing.
		var subjectInternal int64
		if in.Body.SubjectID != "" {
			var (
				found  bool
				resErr error
			)
			switch subjectType {
			case generated.SignalsSubjectTypeTask:
				subjectInternal, found, resErr = resolveTaskInWorkspace(ctx, deps.DB, wsID, in.Body.SubjectID)
			case generated.SignalsSubjectTypeUser:
				subjectInternal, found, resErr = resolveUserInWorkspace(ctx, deps.DB, wsID, in.Body.SubjectID)
			case generated.SignalsSubjectTypeCalendarEvent:
				subjectInternal, found, resErr = resolveCalendarEventInWorkspace(ctx, deps.DB, wsID, in.Body.SubjectID)
			case generated.SignalsSubjectTypeWorkspace:
				// subject_id is meaningless for workspace-scoped signals; ignore
				// it explicitly so an accidentally-supplied id does not cause
				// a confusing 404.
				subjectInternal = 0
			}
			if resErr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			if subjectType != generated.SignalsSubjectTypeWorkspace && !found {
				return nil, httpErr(apierrors.WsSignalSubjectNotFound)
			}
		}

		// Step 3 — when both addressing forms target a task, enforce
		// agreement so the legacy fast path and the new subject pair can
		// never disagree on a single row.
		if taskLinked && subjectType == generated.SignalsSubjectTypeTask && subjectInternal != 0 && subjectInternal != taskInternal {
			return nil, httpErr(apierrors.WsSignalSubjectMismatch)
		}

		// When SubjectType=task was selected but only the legacy taskId was
		// supplied, mirror the task into subject_id so the row is internally
		// consistent. Symmetric: legacy taskId implicitly satisfies the new
		// (subject_type=task, subject_id) shape.
		if subjectType == generated.SignalsSubjectTypeTask && subjectInternal == 0 && taskInternal != 0 {
			subjectInternal = taskInternal
		}

		subjectID := subjectIDFor(subjectType, subjectInternal)

		var expiresAt sql.NullTime
		if in.Body.ExpiresAt != nil {
			expiresAt = sql.NullTime{Time: time.Unix(*in.Body.ExpiresAt, 0).UTC(), Valid: true}
		}

		payload := in.Body.Payload
		if len(payload) == 0 {
			payload = json.RawMessage(`{}`)
		}
		ext := sql.NullString{String: in.Body.ExternalID, Valid: in.Body.ExternalID != ""}
		pub := types.New()
		now := time.Now().UTC()
		// Retry on transient FK deadlocks; signals shares FK lock
		// space with tasks/workspaces.
		var signalInternalID int64
		if err := dbretry.Do(ctx, "signals.Create", func(ctx context.Context) error {
			id, e := deps.Queries.InsertSignal(ctx, generated.InsertSignalParams{
				PublicID:    pub,
				WorkspaceID: wsID,
				TaskID:      taskFK,
				Source:      generated.SignalsSource(in.Body.Source),
				Kind:        in.Body.Kind,
				ExternalID:  ext,
				PayloadJson: payload,
				ReceivedAt:  now,
				SubjectType: subjectType,
				SubjectID:   subjectID,
				ExpiresAt:   expiresAt,
			})
			if e == nil {
				signalInternalID = id
			}
			return e
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// INSERT IGNORE returns LastInsertId()=0 when the row collided on
		// the workspace-scoped dedupe key (workspace_id, source,
		// external_id) and nothing was written. In that case the freshly
		// minted `pub` matches no persisted row, so emitting it would
		// mis-attribute the response and writing an audit entry would spam
		// the log on every duplicate redelivery. Short-circuit: resolve the
		// existing row's public id and return it honestly, skipping the
		// spurious audit write and the lifecycle/judge side effects (the
		// judge enqueuer is already 0-guarded). When no existing row can be
		// found (no dedupe key, e.g. NULL external_id), fall through to the
		// normal path with the minted id.
		if signalInternalID == 0 {
			existing, found, lerr := resolveSignalByDedupeKey(ctx, deps.DB, wsID, generated.SignalsSource(in.Body.Source), ext)
			if lerr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			if found {
				return &CreateOutput{Body: Signal{
					ID:          existing.String(),
					TaskID:      in.Body.TaskID,
					Source:      in.Body.Source,
					Kind:        in.Body.Kind,
					ExternalID:  in.Body.ExternalID,
					Payload:     payload,
					SubjectType: string(subjectType),
					SubjectID:   in.Body.SubjectID,
					ReceivedAt:  now.Unix(),
					ExpiresAt:   in.Body.ExpiresAt,
					CreatedAt:   now.Unix(),
				}}, nil
			}
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "signal.create",
			ActorID:      actorID,
			WorkspaceID:  wsID,
			ResourceType: "signal",
			ResourceID:   pub.String(),
		})

		if taskLinked {
			// Service-token writes have no human actor; omit the
			// ActorUserID pointer so the event row records the write
			// as system-originated rather than misattributing it to
			// user id 0.
			var actorPtr *int64
			if actorID != 0 {
				actor := int64(actorID)
				actorPtr = &actor
			}
			if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
				Type:        eventbus.SignalAttached,
				WorkspaceID: wsID,
				ActorUserID: actorPtr,
				TaskID:      &taskInternal,
				Payload: map[string]any{
					"signalId": pub.String(),
					"source":   in.Body.Source,
					"kind":     in.Body.Kind,
				},
			}); err != nil {
				slog.ErrorContext(ctx, "eventbus.Append failed",
					slog.Any("err", err),
					slog.String("handler", "signals.Create"),
					slog.String("event_type", string(eventbus.SignalAttached)),
					slog.String("workspace_public_id", in.Body.WorkspaceID),
					slog.String("task_public_id", in.Body.TaskID),
					slog.String("signal_public_id", pub.String()),
				)
			}
		}

		// Wake any matching signal_judge agents (ADR 0008 D3). The
		// enqueuer is best-effort: a non-nil error is logged but does
		// not fail the HTTP response — the signal row is the
		// canonical write. signalInternalID = 0 (duplicate INSERT
		// IGNORE) short-circuits inside EnqueueForSignal.
		if deps.JudgeEnqueuer != nil {
			if err := deps.JudgeEnqueuer.EnqueueForSignal(ctx, signalInternalID, wsID, signalkinds.Kind(in.Body.Kind)); err != nil {
				slog.WarnContext(ctx, "signaljudge enqueue failed",
					slog.Any("err", err),
					slog.String("handler", "signals.Create"),
					slog.String("workspace_public_id", in.Body.WorkspaceID),
					slog.String("signal_public_id", pub.String()),
					slog.String("kind", in.Body.Kind),
				)
			}
		}

		out := &CreateOutput{Body: Signal{
			ID:          pub.String(),
			TaskID:      in.Body.TaskID,
			Source:      in.Body.Source,
			Kind:        in.Body.Kind,
			ExternalID:  in.Body.ExternalID,
			Payload:     payload,
			SubjectType: string(subjectType),
			SubjectID:   in.Body.SubjectID,
			ReceivedAt:  now.Unix(),
			ExpiresAt:   in.Body.ExpiresAt,
			CreatedAt:   now.Unix(),
		}}
		return out, nil
	}
}
