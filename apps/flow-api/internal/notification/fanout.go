// Package notification implements the notification fan-out service that
// subscribes to eventbus events and creates per-user notification rows
// for workspace members.
package notification

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/email"
)

// defaultFanoutTimeout caps how long a single fan-out goroutine may run
// when no explicit timeout is configured. The detached context keeps
// the trace span and logger values from the parent request but is no
// longer subject to request cancellation, so an upper bound is needed
// to prevent a misbehaving query from leaking goroutines indefinitely.
const defaultFanoutTimeout = 30 * time.Second

// Fanout creates per-user notification rows in response to eventbus
// events. It subscribes via [eventbus.AddNotifyHook] and, for each
// event, determines the set of recipients (workspace members minus the
// actor) and inserts a notification row for each one.
//
// Fan-out goroutines detach from the request context using
// [context.WithoutCancel] so the work is not aborted when the
// originating HTTP handler returns. Each goroutine is wrapped with
// [context.WithTimeout] (configurable, defaults to 30s) to bound the
// runaway risk and is tracked by [sync.WaitGroup] so [Fanout.Shutdown]
// can wait for in-flight work to drain at process exit.
type Fanout struct {
	db      *sql.DB
	queries *generated.Queries
	email   email.Sender

	timeout time.Duration

	wg       sync.WaitGroup
	stopMu   sync.RWMutex
	stopping bool

	// run is the function executed inside each fan-out goroutine.
	// It is overridable by tests so the goroutine plumbing
	// (detached cancel, timeout, shutdown wait) can be exercised
	// without a live database. Production code leaves this nil and
	// the hook routes to [Fanout.fanout].
	run func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint32)
}

// NewFanout creates a Fanout backed by the given database and email
// transport. When emailSender is nil or a [email.NoopSender], email
// delivery is silently skipped.
func NewFanout(db *sql.DB, q *generated.Queries, emailSender email.Sender) *Fanout {
	return &Fanout{
		db:      db,
		queries: q,
		email:   emailSender,
		timeout: defaultFanoutTimeout,
	}
}

// SetTimeout overrides the per-event fan-out timeout. Values <= 0
// reset to the package default. Safe to call once at start-up before
// any hook fires; not safe under concurrent fan-out.
func (f *Fanout) SetTimeout(d time.Duration) {
	if d <= 0 {
		f.timeout = defaultFanoutTimeout
		return
	}
	f.timeout = d
}

// Hook returns an eventbus.NotifyHook that can be passed to
// [eventbus.AddNotifyHook]. The returned function is non-blocking:
// it spawns a goroutine so the eventbus append path is never delayed
// by notification fan-out.
//
// The spawned goroutine inherits the parent context's values
// (trace span, logger, actor info) via [context.WithoutCancel] but
// is no longer cancelled when the parent request completes. A
// configurable timeout caps the maximum lifetime to prevent runaways.
//
// eventInternalID is the events.id of the row that triggered this
// fan-out. It is threaded into notifications.source_event_id so the
// (recipient, source_event, channel) unique key dedupes goroutine
// retries and replicated dispatches.
func (f *Fanout) Hook() func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint32) {
	return func(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint32) {
		// Refuse to start new work after Shutdown has been called.
		f.stopMu.RLock()
		if f.stopping {
			f.stopMu.RUnlock()
			return
		}
		f.wg.Add(1)
		f.stopMu.RUnlock()

		// Detach cancellation from the request context but keep the
		// values (trace span, slog attributes, etc.).
		detached := context.WithoutCancel(ctx)

		go func() {
			defer f.wg.Done()
			runCtx, cancel := context.WithTimeout(detached, f.timeout)
			defer cancel()
			fn := f.run
			if fn == nil {
				fn = f.fanout
			}
			fn(runCtx, workspaceID, eventType, eventInternalID)
		}()
	}
}

// Shutdown waits for all in-flight fan-out goroutines to finish or
// for the supplied context to be cancelled, whichever happens first.
// After Shutdown returns, new events delivered through the hook are
// dropped (a debug log line records the drop).
//
// It is safe to call Shutdown multiple times; subsequent calls only
// wait, they do not toggle state.
func (f *Fanout) Shutdown(ctx context.Context) error {
	f.stopMu.Lock()
	f.stopping = true
	f.stopMu.Unlock()

	done := make(chan struct{})
	go func() {
		f.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// fanout performs the actual work of creating notification rows. It
// runs in a background goroutine and logs errors instead of returning
// them.
//
// eventInternalID anchors the at-least-once dedupe contract: the
// underlying INSERT IGNORE collides on
// (recipient_user_id, source_event_id, channel), so re-firing the
// same hook (e.g. retry, replicated dispatch) yields zero new rows.
func (f *Fanout) fanout(ctx context.Context, workspaceID uint32, eventType string, eventInternalID uint32) {
	// Only fan out for event types that warrant user notifications.
	title, resourceType, severity := classifyEvent(eventType)
	if title == "" {
		return
	}

	// Resolve actor + resource for the exact event row identified by
	// eventInternalID. Anchoring on events.id (rather than "latest of
	// type") removes the race where two same-type events for the same
	// workspace land back-to-back and the second hook reads the first
	// hook's row.
	row, err := f.eventByID(ctx, workspaceID, eventInternalID)
	if err != nil {
		slog.ErrorContext(ctx, "notification fanout: failed to fetch event by id",
			slog.String("event_type", eventType),
			slog.Uint64("workspace_id", uint64(workspaceID)),
			slog.Uint64("event_id", uint64(eventInternalID)),
			slog.String("err", err.Error()))
		return
	}

	// Determine recipients: all active workspace members except the actor.
	recipients, err := f.workspaceMemberUserIDs(ctx, workspaceID)
	if err != nil {
		slog.ErrorContext(ctx, "notification fanout: failed to list workspace members",
			slog.Uint64("workspace_id", uint64(workspaceID)),
			slog.String("err", err.Error()))
		return
	}

	actorUserID := uint32(0)
	if row.actorUserID.Valid {
		actorUserID = uint32(row.actorUserID.Int32) //#nosec G115 -- actor_user_id is users.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
	}

	sourceEventID := sql.NullInt32{}
	if eventInternalID != 0 {
		sourceEventID = sql.NullInt32{Int32: int32(eventInternalID), Valid: true} //#nosec G115 -- event id is events.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	}

	for _, recipientID := range recipients {
		// Exclude the actor from their own notifications.
		if recipientID == actorUserID {
			continue
		}

		pubID := types.New()
		actorID := sql.NullInt32{}
		if row.actorUserID.Valid {
			actorID = row.actorUserID
		}

		affected, err := f.queries.CreateNotification(ctx, generated.CreateNotificationParams{
			PublicID:         pubID,
			WorkspaceID:      workspaceID,
			RecipientUserID:  recipientID,
			ActorUserID:      actorID,
			SourceEventID:    sourceEventID,
			EventType:        eventType,
			ResourceType:     resourceType,
			ResourcePublicID: row.resourcePublicID,
			Title:            title,
			Body:             sql.NullString{},
			Severity:         severity,
			Channel:          generated.NotificationsChannelInApp,
		})
		if err != nil {
			slog.ErrorContext(ctx, "notification fanout: failed to create notification",
				slog.Uint64("workspace_id", uint64(workspaceID)),
				slog.Uint64("recipient_user_id", uint64(recipientID)),
				slog.String("event_type", eventType),
				slog.Uint64("event_id", uint64(eventInternalID)),
				slog.String("err", err.Error()))
			continue
		}
		if affected == 0 {
			// INSERT IGNORE collided with the (recipient, source_event,
			// channel) unique key — the notification already exists.
			// This is the at-least-once happy path; record it as
			// debug, not an error.
			slog.DebugContext(ctx, "notification fanout: deduplicated",
				slog.Uint64("workspace_id", uint64(workspaceID)),
				slog.Uint64("recipient_user_id", uint64(recipientID)),
				slog.Uint64("event_id", uint64(eventInternalID)),
				slog.String("event_type", eventType),
				slog.String("channel", string(generated.NotificationsChannelInApp)))
			// TODO: increment a Prometheus counter
			// `nf_notification_dedup_total{channel,event_type}` once the
			// notification metrics file exists in apps/flow-api/internal/obs.
		}
	}
}

// eventRow is a minimal representation of an event extracted from the
// events table for fan-out enrichment.
type eventRow struct {
	actorUserID      sql.NullInt32
	resourcePublicID types.PublicID
}

// eventByID fetches the row identified by (workspaceID, eventInternalID).
// The workspace_id predicate is defence-in-depth — events.id is globally
// unique already, but anchoring on workspace prevents cross-tenant reads
// if a caller ever passes a stale id.
func (f *Fanout) eventByID(ctx context.Context, workspaceID uint32, eventInternalID uint32) (eventRow, error) {
	const q = `
		SELECT e.actor_user_id,
		       CASE
		         WHEN e.task_id IS NOT NULL THEN (SELECT t.public_id FROM tasks t WHERE t.id = e.task_id)
		         ELSE NULL
		       END AS resource_public_id
		FROM events e
		WHERE e.id = ?
		  AND e.workspace_id = ?
		LIMIT 1
	`
	var r eventRow
	err := f.db.QueryRowContext(ctx, q, eventInternalID, workspaceID).Scan(
		&r.actorUserID,
		&r.resourcePublicID,
	)
	if err != nil {
		return r, fmt.Errorf("eventByID: %w", err)
	}
	return r, nil
}

// workspaceMemberUserIDs returns the internal user IDs of all active
// members in a workspace.
func (f *Fanout) workspaceMemberUserIDs(ctx context.Context, workspaceID uint32) ([]uint32, error) {
	const q = `
		SELECT user_id
		FROM workspace_members
		WHERE workspace_id = ?
		  AND enabled = TRUE
	`
	rows, err := f.db.QueryContext(ctx, q, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspaceMemberUserIDs: %w", err)
	}
	defer rows.Close()

	var ids []uint32
	for rows.Next() {
		var id uint32
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// classifyEvent maps an event type to a human-readable notification
// title template, resource type, and severity. Returns an empty title
// when the event type should not generate notifications.
func classifyEvent(eventType string) (title string, resourceType string, severity generated.NotificationsSeverity) {
	switch eventType {
	case "task.created":
		return "A new task was created", "task", generated.NotificationsSeverityNormal
	case "task.updated":
		return "A task was updated", "task", generated.NotificationsSeverityLow
	case "task.disabled":
		return "A task was deleted", "task", generated.NotificationsSeverityNormal
	case "task.comment.added":
		return "A new comment was added", "comment", generated.NotificationsSeverityNormal
	case "task.comment.edited":
		return "A comment was edited", "comment", generated.NotificationsSeverityLow
	case "task.comment.removed":
		return "A comment was removed", "comment", generated.NotificationsSeverityLow
	case "task.actor.added":
		return "You were added to a task", "task", generated.NotificationsSeverityNormal
	case "task.actor.removed":
		return "You were removed from a task", "task", generated.NotificationsSeverityNormal
	case "task.transition.start":
		return "A task was started", "task", generated.NotificationsSeverityNormal
	case "task.transition.complete":
		return "A task was completed", "task", generated.NotificationsSeverityNormal
	case "task.transition.block":
		return "A task was blocked", "task", generated.NotificationsSeverityHigh
	case "task.transition.unblock":
		return "A task was unblocked", "task", generated.NotificationsSeverityNormal
	case "task.transition.submit":
		return "A task was submitted for review", "task", generated.NotificationsSeverityNormal
	case "task.transition.reopen":
		return "A task was reopened", "task", generated.NotificationsSeverityNormal
	case "task.transition.cancel":
		return "A task was cancelled", "task", generated.NotificationsSeverityNormal

	// itemkit kinds: task ↔ calendar_event atomic mutations.
	// The reader for these events is the "item" (task + its projections);
	// resourceType stays "task" because downstream routing is the same.
	case "item.scheduled":
		return "An item was placed on a calendar", "task", generated.NotificationsSeverityNormal
	case "item.unscheduled":
		return "An item was removed from a calendar", "task", generated.NotificationsSeverityLow
	case "item.rescheduled":
		return "An item was rescheduled", "task", generated.NotificationsSeverityNormal
	case "item.renamed":
		return "An item was renamed", "task", generated.NotificationsSeverityLow
	case "item.deleted":
		return "An item was deleted", "task", generated.NotificationsSeverityNormal
	case "item.reconciled":
		return "An item was auto-reconciled", "task", generated.NotificationsSeverityLow
	case "item.actor.added":
		return "You were added to an item", "task", generated.NotificationsSeverityNormal
	case "item.actor.removed":
		return "You were removed from an item", "task", generated.NotificationsSeverityNormal
	case "item.visibility.changed":
		return "An item's visibility changed", "task", generated.NotificationsSeverityLow
	case "item.milestone.link.added":
		return "An item was linked to a milestone", "task", generated.NotificationsSeverityLow
	case "item.milestone.link.removed":
		return "An item was unlinked from a milestone", "task", generated.NotificationsSeverityLow

	default:
		return "", "", ""
	}
}
