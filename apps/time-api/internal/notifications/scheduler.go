// Package notifications provides a lightweight background scheduler that
// checks for upcoming calendar events with notification offsets and logs
// reminders. Real push/email delivery can be added later.
package notifications

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
)

// StartNotificationScheduler runs a ticker that checks for events needing
// notifications. For now it logs reminders. Real push/email delivery can
// be added later.
//
// TODO: Add deduplication via a calendar_notifications_sent table or a
// notified_at column on calendar_events to avoid sending duplicate
// notifications across scheduler restarts.
func StartNotificationScheduler(ctx context.Context, db *sql.DB, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("notification scheduler shutting down")
			return
		case <-ticker.C:
			checkAndNotify(ctx, db)
		}
	}
}

type pendingNotification struct {
	PublicID           types.PublicID
	Title              string
	StartAt            time.Time
	NotificationOffset int32
	OwnerUserID        uint32
}

func checkAndNotify(ctx context.Context, db *sql.DB) {
	// Find events where the notification window has opened but the event
	// has not started yet. Limited to events starting within the next 24
	// hours to avoid scanning the entire table.
	rows, err := db.QueryContext(ctx, `
		SELECT ce.public_id, ce.title, ce.start_at, ce.notification_offset, ce.owner_user_id
		FROM calendar_events ce
		WHERE ce.notification_offset IS NOT NULL
		  AND ce.enabled = TRUE
		  AND ce.start_at > NOW()
		  AND DATE_SUB(ce.start_at, INTERVAL ce.notification_offset MINUTE) <= NOW()
		  AND ce.start_at <= DATE_ADD(NOW(), INTERVAL 24 HOUR)
	`)
	if err != nil {
		slog.Error("notification scheduler: failed to query events", "err", err)
		return
	}
	defer rows.Close()

	var notifications []pendingNotification
	for rows.Next() {
		var n pendingNotification
		if err := rows.Scan(&n.PublicID, &n.Title, &n.StartAt, &n.NotificationOffset, &n.OwnerUserID); err != nil {
			slog.Error("notification scheduler: failed to scan row", "err", err)
			continue
		}
		notifications = append(notifications, n)
	}
	if err := rows.Err(); err != nil {
		slog.Error("notification scheduler: row iteration error", "err", err)
		return
	}

	for _, n := range notifications {
		// TODO: Replace with real push/email delivery. Currently just logs.
		slog.Info("notification reminder",
			"eventId", n.PublicID.String(),
			"title", n.Title,
			"startAt", n.StartAt.Format(time.RFC3339),
			"offsetMinutes", n.NotificationOffset,
			"ownerUserId", n.OwnerUserID,
		)
	}

	if len(notifications) > 0 {
		slog.Info("notification scheduler: processed reminders", "count", len(notifications))
	}
}
