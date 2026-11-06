// Package taskworkflow owns persisted task review and human handoff lifecycle
// work that runs outside an HTTP request.
package taskworkflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

const maxHandoffExpiryBatch = 100

// HandoffExpiryRunner expires due pending human handoffs. It claims rows with
// SKIP LOCKED and commits each status change together with its domain event.
type HandoffExpiryRunner struct {
	db      *sql.DB
	queries *generated.Queries
	events  *mutationlog.Recorder
}

// NewHandoffExpiryRunner builds the bounded expiry worker used by the API
// background loop and by integration tests.
func NewHandoffExpiryRunner(db *sql.DB, queries *generated.Queries, events *mutationlog.Recorder) *HandoffExpiryRunner {
	if queries == nil && db != nil {
		queries = generated.New(db)
	}
	if events == nil && db != nil {
		events = mutationlog.New(db, queries)
	}
	return &HandoffExpiryRunner{db: db, queries: queries, events: events}
}

// RunOnce expires at most limit due handoffs using the supplied clock. It
// returns the number committed by this run. Limits above the fixed maximum
// are clamped; a zero limit is a no-op and a negative limit is rejected.
func (r *HandoffExpiryRunner) RunOnce(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit < 0 {
		return 0, errors.New("taskworkflow: handoff expiry limit cannot be negative")
	}
	if limit == 0 {
		return 0, nil
	}
	if limit > maxHandoffExpiryBatch {
		limit = maxHandoffExpiryBatch
	}
	if r == nil || r.db == nil || r.queries == nil || r.events == nil {
		return 0, errors.New("taskworkflow: handoff expiry runner is not configured")
	}

	clock := now.UTC().Truncate(time.Millisecond)
	var processed int
	err := dbretry.InTx(ctx, r.db, "taskworkflow.HandoffExpiryRunner.RunOnce", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		processed = 0
		qtx := generated.New(tx.RawTx())
		handoffs, err := qtx.ListDueTaskHandoffsForUpdate(ctx, generated.ListDueTaskHandoffsForUpdateParams{
			Now:   sql.NullTime{Time: clock, Valid: true},
			Limit: int32(limit), //#nosec G115 -- limit is clamped to [1, maxHandoffExpiryBatch] above
		})
		if err != nil {
			return err
		}
		for _, handoff := range handoffs {
			expired, err := ExpireHandoffTx(ctx, tx, r.events, DueHandoff{
				ID:               handoff.ID,
				WorkspaceID:      handoff.WorkspaceID,
				TaskID:           handoff.TaskID,
				HandoffPublicID:  handoff.HandoffPublicID,
				TaskPublicID:     handoff.TaskPublicID,
				Role:             handoff.Role,
				FromUserPublicID: publicIDString(handoff.FromUserPublicID),
				ToUserPublicID:   publicIDString(handoff.ToUserPublicID),
			}, clock, "taskworkflow.HandoffExpiryRunner.RunOnce")
			if err != nil {
				return err
			}
			if expired {
				processed++
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return processed, nil
}

// DueHandoff is the pending handoff an expiry resolves.
type DueHandoff struct {
	ID               uint32
	WorkspaceID      uint32
	TaskID           uint32
	HandoffPublicID  string
	TaskPublicID     string
	Role             generated.TaskHandoffsRole
	FromUserPublicID string
	ToUserPublicID   string
}

// ExpireHandoffTx expires one due pending handoff and appends its system
// decision event in tx. It reports false, with nothing written, when the row
// is no longer pending and due at now.
func ExpireHandoffTx(
	ctx context.Context,
	tx *dbretry.Tx,
	events *mutationlog.Recorder,
	handoff DueHandoff,
	now time.Time,
	callSite string,
) (bool, error) {
	updated, err := generated.New(tx.RawTx()).ExpireTaskHandoffDue(ctx, generated.ExpireTaskHandoffDueParams{
		Now:         sql.NullTime{Time: now, Valid: true},
		WorkspaceID: handoff.WorkspaceID,
		TaskID:      handoff.TaskID,
		ID:          handoff.ID,
	})
	if err != nil {
		return false, err
	}
	if updated == 0 {
		return false, nil
	}
	if updated != 1 {
		return false, fmt.Errorf("taskworkflow: expiry update affected %d rows for handoff %s", updated, handoff.HandoffPublicID)
	}
	payload := map[string]any{
		"taskId":           handoff.TaskPublicID,
		"handoffId":        handoff.HandoffPublicID,
		"role":             string(handoff.Role),
		"decision":         "expired",
		"fromUserPublicId": handoff.FromUserPublicID,
		"toUserPublicId":   handoff.ToUserPublicID,
		"expiredAt":        now.Unix(),
	}
	if handoff.FromUserPublicID != "" {
		payload["targetUserPublicId"] = handoff.FromUserPublicID
	}
	taskInternalID := int64(handoff.TaskID)
	if err := events.RecordInTx(ctx, tx,
		mutationlog.Actor{WorkspaceID: handoff.WorkspaceID},
		mutationlog.Mutation{
			EventType:    eventbus.TaskHandoffDecided,
			AuditAction:  "task.handoff.expire",
			ResourceType: "task_handoff",
			ResourceID:   handoff.HandoffPublicID,
			TaskID:       &taskInternalID,
			Payload:      payload,
			CallSite:     callSite,
		},
	); err != nil {
		return false, err
	}
	return true, nil
}

func publicIDString(value any) string {
	switch id := value.(type) {
	case nil:
		return ""
	case string:
		return id
	case []byte:
		return string(id)
	default:
		return fmt.Sprint(id)
	}
}
