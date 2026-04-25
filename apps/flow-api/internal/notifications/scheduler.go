// Package notifications provides a lightweight background scheduler that
// checks for upcoming calendar events with notification offsets and logs
// reminders. Real push/email delivery can be added later.
package notifications

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
)

// schedLog returns a logger pinned to component=calendar so log queries
// can isolate the relocated calendar reminder loop from task / AI / auth
// surfaces that share the same flow-api process.
func schedLog() *slog.Logger {
	return slog.Default().With("component", "calendar")
}

// StartNotificationScheduler runs a ticker that checks for events needing
// notifications. For now it logs reminders. Real push/email delivery can
// be added later.
//
// The scheduler marks each event's notified_at column after sending so that
// duplicate notifications are never emitted across ticks or restarts.
func StartNotificationScheduler(ctx context.Context, db *sql.DB, interval time.Duration) {
	log := schedLog()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("notification scheduler shutting down")
			return
		case <-ticker.C:
			CheckAndNotify(ctx, db)
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
}

// CheckAndNotify queries for events whose notification window has opened
// and logs reminders. After processing each event it marks notified_at so
// the event is not picked up again on the next tick.
func CheckAndNotify(ctx context.Context, db *sql.DB) {
	log := schedLog()
	// Find events where the notification window has opened but the event
	// has not started yet and no notification has been sent. Limited to
	// events starting within the next 24 hours to avoid scanning the
	// entire table.
	rows, err := db.QueryContext(ctx, `
		SELECT ce.id, ce.public_id, ce.title, ce.start_at, ce.notification_offset, ce.owner_user_id
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
		if err := rows.Scan(&n.ID, &n.PublicID, &n.Title, &n.StartAt, &n.NotificationOffset, &n.OwnerUserID); err != nil {
			log.Error("notification scheduler: failed to scan row", "err", err)
			continue
		}
		notifications = append(notifications, n)
	}
	if err := rows.Err(); err != nil {
		log.Error("notification scheduler: row iteration error", "err", err)
		return
	}

	for _, n := range notifications {
		// TODO: Replace with real push/email delivery. Currently just logs.
		log.Info("notification reminder",
			"eventId", n.PublicID.String(),
			"title", n.Title,
			"startAt", n.StartAt.Format(time.RFC3339),
			"offsetMinutes", n.NotificationOffset,
			"ownerUserId", n.OwnerUserID,
		)

		// Mark the event as notified so it is not picked up again.
		if _, err := db.ExecContext(ctx, `UPDATE calendar_events SET notified_at = NOW() WHERE id = ?`, n.ID); err != nil {
			log.Error("notification scheduler: failed to mark event as notified", "eventId", n.PublicID.String(), "err", err)
		}
	}

	if len(notifications) > 0 {
		log.Info("notification scheduler: processed reminders", "count", len(notifications))
	}
}
