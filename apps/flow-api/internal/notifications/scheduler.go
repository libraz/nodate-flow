// Package notifications provides a lightweight background scheduler that
// checks for upcoming calendar events with notification offsets and
// dispatches reminder notifications through the shared
// [notification.Fanout] so they land in the same in-app notification
// store as task / comment / item events.
package notifications

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/notification"
)

// ReminderDispatcher is the subset of [notification.Fanout] that the
// scheduler needs. It is declared as an interface so tests can stub the
// dispatch path without standing up a full Fanout instance.
type ReminderDispatcher interface {
	DeliverCalendarReminder(
		ctx context.Context,
		workspaceID uint32,
		eventPublicID types.PublicID,
		title string,
		recipientUserIDs []uint32,
	) error
}

// schedLog returns a logger pinned to component=calendar so log queries
// can isolate the relocated calendar reminder loop from task / AI / auth
// surfaces that share the same flow-api process.
func schedLog() *slog.Logger {
	return slog.Default().With("component", "calendar")
}

// StartNotificationScheduler runs a ticker that checks for events needing
// notifications and dispatches them to the [notification.Fanout] so each
// recipient receives an in-app notification row.
//
// The scheduler marks each event's notified_at column only after the
// dispatch returns without error, so a transient DB failure causes the
// event to be retried on the next tick instead of silently dropping the
// reminder.
func StartNotificationScheduler(
	ctx context.Context,
	db *sql.DB,
	dispatcher ReminderDispatcher,
	interval time.Duration,
) {
	log := schedLog()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("notification scheduler shutting down")
			return
		case <-ticker.C:
			CheckAndNotify(ctx, db, dispatcher)
		}
	}
}

type pendingNotification struct {
	ID                 uint32
	PublicID           types.PublicID
	Title              string
	StartAt            time.Time
	NotificationOffset int32
	OwnerUserID        uint32
	WorkspaceID        uint32
}

// CheckAndNotify queries for events whose notification window has opened
// and dispatches a reminder for each recipient through the dispatcher.
// After a successful dispatch it marks notified_at so the event is not
// picked up again on the next tick.
func CheckAndNotify(ctx context.Context, db *sql.DB, dispatcher ReminderDispatcher) {
	log := schedLog()
	// Find events where the notification window has opened but the event
	// has not started yet and no notification has been sent. Limited to
	// events starting within the next 24 hours to avoid scanning the
	// entire table.
	rows, err := db.QueryContext(ctx, `
		SELECT ce.id, ce.public_id, ce.title, ce.start_at, ce.notification_offset, ce.owner_user_id, ce.workspace_id
		FROM calendar_events ce
		WHERE ce.notification_offset IS NOT NULL
		  AND ce.start_at IS NOT NULL
		  AND ce.notified_at IS NULL
		  AND ce.enabled = TRUE
		  AND ce.start_at > NOW()
		  AND DATE_SUB(ce.start_at, INTERVAL ce.notification_offset MINUTE) <= NOW()
		  AND ce.start_at <= DATE_ADD(NOW(), INTERVAL 24 HOUR)
	`)
	if err != nil {
		log.Error("notification scheduler: failed to query events", "err", err)
		return
	}
	defer rows.Close()

	var notifications []pendingNotification
	for rows.Next() {
		var n pendingNotification
		if err := rows.Scan(
			&n.ID,
			&n.PublicID,
			&n.Title,
			&n.StartAt,
			&n.NotificationOffset,
			&n.OwnerUserID,
			&n.WorkspaceID,
		); err != nil {
			log.Error("notification scheduler: failed to scan row", "err", err)
			continue
		}
		notifications = append(notifications, n)
	}
	if err := rows.Err(); err != nil {
		log.Error("notification scheduler: row iteration error", "err", err)
		return
	}

	delivered := 0
	for _, n := range notifications {
		recipients, err := reminderRecipients(ctx, db, n.ID, n.OwnerUserID)
		if err != nil {
			log.Error("notification scheduler: failed to load recipients",
				"eventId", n.PublicID.String(), "err", err)
			continue
		}
		if err := dispatcher.DeliverCalendarReminder(
			ctx, n.WorkspaceID, n.PublicID, n.Title, recipients,
		); err != nil {
			// Surface the error and leave notified_at NULL so the next
			// tick retries the dispatch.
			log.Error("notification scheduler: dispatch failed; will retry next tick",
				"eventId", n.PublicID.String(), "err", err)
			continue
		}

		if _, err := db.ExecContext(ctx,
			`UPDATE calendar_events SET notified_at = NOW() WHERE id = ?`, n.ID); err != nil {
			log.Error("notification scheduler: failed to mark event as notified",
				"eventId", n.PublicID.String(), "err", err)
			continue
		}
		delivered++
	}

	if delivered > 0 {
		log.Info("notification scheduler: delivered reminders", "count", delivered)
	}
}

// reminderRecipients returns the set of user ids that should receive a
// reminder for eventID: every enabled attendee plus the event owner.
// Duplicates are removed so an owner who is also listed as an attendee
// receives one notification row, not two.
func reminderRecipients(
	ctx context.Context,
	db *sql.DB,
	eventID uint32,
	ownerUserID uint32,
) ([]uint32, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT user_id
		FROM calendar_event_attendees
		WHERE event_id = ?
		  AND enabled = TRUE
	`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := make(map[uint32]struct{})
	var ids []uint32
	for rows.Next() {
		var uid uint32
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		if _, dup := seen[uid]; dup {
			continue
		}
		seen[uid] = struct{}{}
		ids = append(ids, uid)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, dup := seen[ownerUserID]; !dup {
		seen[ownerUserID] = struct{}{}
		ids = append(ids, ownerUserID)
	}
	return ids, nil
}

// Compile-time guard: *notification.Fanout must satisfy
// ReminderDispatcher so main.go can wire it into the scheduler without
// an explicit adapter.
var _ ReminderDispatcher = (*notification.Fanout)(nil)
