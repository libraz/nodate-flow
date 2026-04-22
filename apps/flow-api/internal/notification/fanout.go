// Package notification implements the notification fan-out service that
// subscribes to eventbus events and creates per-user notification rows
// for workspace members.
package notification

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/email"
)

// Fanout creates per-user notification rows in response to eventbus
// events. It subscribes via [eventbus.AddNotifyHook] and, for each
// event, determines the set of recipients (workspace members minus the
// actor) and inserts a notification row for each one.
type Fanout struct {
	db      *sql.DB
	queries *generated.Queries
	email   email.Sender
}

// NewFanout creates a Fanout backed by the given database and email
// transport. When emailSender is nil or a [email.NoopSender], email
// delivery is silently skipped.
func NewFanout(db *sql.DB, q *generated.Queries, emailSender email.Sender) *Fanout {
	return &Fanout{db: db, queries: q, email: emailSender}
}

// Hook returns an eventbus.NotifyHook that can be passed to
// [eventbus.AddNotifyHook]. The returned function is non-blocking:
// it spawns a goroutine so the eventbus append path is never delayed
// by notification fan-out.
func (f *Fanout) Hook() func(ctx context.Context, workspaceID uint32, eventType string) {
	return func(ctx context.Context, workspaceID uint32, eventType string) {
		// Fire-and-forget: fan-out must never block the eventbus
		// append path. Use a background context so the work
		// survives the request lifecycle.
		go f.fanout(context.Background(), workspaceID, eventType)
	}
}

// fanout performs the actual work of creating notification rows. It
// runs in a background goroutine and logs errors instead of returning
// them.
func (f *Fanout) fanout(ctx context.Context, workspaceID uint32, eventType string) {
	// Only fan out for event types that warrant user notifications.
	title, resourceType, severity := classifyEvent(eventType)
	if title == "" {
		return
	}

	// Look up the most recent event of this type for this workspace
	// to extract the actor and payload.
	row, err := f.latestEvent(ctx, workspaceID, eventType)
	if err != nil {
		slog.Warn("notification fanout: failed to fetch latest event",
			slog.String("event_type", eventType),
			slog.Uint64("workspace_id", uint64(workspaceID)),
			slog.String("err", err.Error()))
		return
	}

	// Determine recipients: all active workspace members except the actor.
	recipients, err := f.workspaceMemberUserIDs(ctx, workspaceID)
	if err != nil {
		slog.Warn("notification fanout: failed to list workspace members",
			slog.Uint64("workspace_id", uint64(workspaceID)),
			slog.String("err", err.Error()))
		return
	}

	actorUserID := uint32(0)
	if row.actorUserID.Valid {
		actorUserID = uint32(row.actorUserID.Int32)
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

		_, err := f.queries.CreateNotification(ctx, generated.CreateNotificationParams{
			PublicID:         pubID,
			WorkspaceID:      workspaceID,
			RecipientUserID:  recipientID,
			ActorUserID:      actorID,
			EventType:        eventType,
			ResourceType:     resourceType,
			ResourcePublicID: row.resourcePublicID,
			Title:            title,
			Body:             sql.NullString{},
			Severity:         severity,
			Channel:          generated.NotificationsChannelInApp,
		})
		if err != nil {
			slog.Warn("notification fanout: failed to create notification",
				slog.Uint64("workspace_id", uint64(workspaceID)),
				slog.Uint64("recipient_user_id", uint64(recipientID)),
				slog.String("event_type", eventType),
				slog.String("err", err.Error()))
		}
	}
}

// eventRow is a minimal representation of the latest event extracted
// from the events table.
type eventRow struct {
	actorUserID      sql.NullInt32
	resourcePublicID types.PublicID
}

// latestEvent fetches the most recent event of the given type for the
// workspace. It uses a raw query because sqlc does not generate a
// targeted single-row lookup for this pattern.
func (f *Fanout) latestEvent(ctx context.Context, workspaceID uint32, eventType string) (eventRow, error) {
	const q = `
		SELECT e.actor_user_id,
		       CASE
		         WHEN e.task_id IS NOT NULL THEN (SELECT t.public_id FROM tasks t WHERE t.id = e.task_id)
		         ELSE NULL
		       END AS resource_public_id
		FROM events e
		WHERE e.workspace_id = ?
		  AND e.type = ?
		ORDER BY e.id DESC
		LIMIT 1
	`
	var r eventRow
	err := f.db.QueryRowContext(ctx, q, workspaceID, eventType).Scan(
		&r.actorUserID,
		&r.resourcePublicID,
	)
	if err != nil {
		return r, fmt.Errorf("latestEvent: %w", err)
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

	// itemkit kinds (R5.1): task ↔ calendar_event atomic mutations.
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
