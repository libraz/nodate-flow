package signals

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// resolveWorkspace loads the internal workspace id from a public UUID
// and verifies that the actor is an enabled member of it.
func resolveWorkspace(ctx context.Context, db *sql.DB, wsPublic string, actorID uint32) (uint32, error) {
	pub, err := types.Parse(wsPublic)
	if err != nil {
		return 0, httpErr(apierrors.WsWorkspaceNotFound)
	}
	const wsLookup = `SELECT id FROM workspaces WHERE public_id = ? AND enabled = TRUE LIMIT 1`
	var wsID uint32
	if err := db.QueryRowContext(ctx, wsLookup, pub).Scan(&wsID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, httpErr(apierrors.WsWorkspaceNotFound)
		}
		return 0, httpErr(apierrors.InternalUnexpected)
	}
	const wsMemQuery = `SELECT 1 FROM workspace_members
WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE LIMIT 1`
	var one int
	if err := db.QueryRowContext(ctx, wsMemQuery, wsID, actorID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, httpErr(apierrors.WsWorkspaceAccessDenied)
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

// Create handles POST /signals. The actor must be a member of the target
// workspace. When taskId is provided and resolves, a signal.attached event
// is appended to the task timeline.
func Create(deps Deps) func(context.Context, *CreateInput) (*CreateOutput, error) {
	return func(ctx context.Context, in *CreateInput) (*CreateOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsID, err := resolveWorkspace(ctx, deps.DB, in.Body.WorkspaceID, actorID)
		if err != nil {
			return nil, err
		}

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
			taskFK = sql.NullInt32{Int32: int32(id), Valid: true}
			taskInternal = id
			taskLinked = true
		}

		payload := in.Body.Payload
		if len(payload) == 0 {
			payload = json.RawMessage(`{}`)
		}
		ext := sql.NullString{String: in.Body.ExternalID, Valid: in.Body.ExternalID != ""}
		pub := types.New()
		now := time.Now().UTC()
		if _, err := deps.Queries.InsertSignal(ctx, generated.InsertSignalParams{
			PublicID:    pub,
			WorkspaceID: wsID,
			TaskID:      taskFK,
			Source:      generated.SignalsSource(in.Body.Source),
			Kind:        in.Body.Kind,
			ExternalID:  ext,
			PayloadJson: payload,
			ReceivedAt:  now,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if taskLinked {
			actor := int64(actorID)
			_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
				Type:        eventbus.SignalAttached,
				WorkspaceID: wsID,
				ActorUserID: &actor,
				TaskID:      &taskInternal,
				Payload: map[string]any{
					"signalId": pub.String(),
					"source":   in.Body.Source,
					"kind":     in.Body.Kind,
				},
			})
		}

		out := &CreateOutput{Body: Signal{
			ID:         pub.String(),
			TaskID:     in.Body.TaskID,
			Source:     in.Body.Source,
			Kind:       in.Body.Kind,
			ExternalID: in.Body.ExternalID,
			Payload:    payload,
			ReceivedAt: now,
			CreatedAt:  now,
		}}
		return out, nil
	}
}
