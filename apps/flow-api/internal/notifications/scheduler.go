// Package notifications provides a lightweight background scheduler that
// checks for upcoming calendar events with notification offsets and
// dispatches reminder notifications through the shared
// [notification.Fanout] so they land in the same in-app notification
// store as task / comment / item events.
package notifications

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/notification"
)

// reminderEventType is the events.type (and notifications.event_type)
// value for a scheduler-driven calendar reminder.
const reminderEventType = "calendar.reminder"

// reminderSystemSource is the events.actor_system_source value stamped
// on reminder event rows. Reminders are time-driven — no user or agent
// actor exists — so the system-source column names the emitting loop.
const reminderSystemSource = "scheduler:calendar"

// ReminderDispatcher is the subset of [notification.Fanout] that the
// scheduler needs. It is declared as an interface so tests can stub the
// dispatch path without standing up a full Fanout instance.
//
// sourceEventID is the events.id of the calendar.reminder row appended
// by the scheduler for this claim. Implementations must thread it into
// notifications.source_event_id so the
// (recipient_user_id, source_event_id, channel) unique key dedupes
// concurrent or replayed fan-outs of the same reminder.
type ReminderDispatcher interface {
	DeliverCalendarReminder(
		ctx context.Context,
		workspaceID uint32,
		eventPublicID types.PublicID,
		title string,
		recipientUserIDs []uint32,
		sourceEventID int64,
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
// Each due event is first claimed atomically (notified_at NULL -> NOW()
// via a conditional UPDATE), so when multiple replicas run this loop
// only the claim winner dispatches. On a dispatch failure the claim is
// released (notified_at reset to NULL) so the next tick retries instead
// of silently dropping the reminder.
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
//
// Delivery is claim-first: for each candidate the scheduler runs the
// atomic [generated.Queries.ClaimReminderForDelivery] UPDATE and only
// dispatches when it affected exactly one row. Racing replicas (or
// overlapping ticks in one process) observe zero affected rows and skip
// the event, so each reminder is dispatched by at most one claimant.
// After winning the claim the scheduler appends a calendar.reminder row
// to the events log and threads its id into the dispatcher so the
// notifications unique key dedupes any concurrently replayed fan-out.
// If the event append or the dispatch fails, the claim is released so
// the next tick retries.
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

	queries := generated.New(db)
	delivered := 0
	for _, n := range notifications {
		// Load recipients before claiming so a read failure does not
		// consume the claim (nothing to release, next tick retries).
		recipients, err := reminderRecipients(ctx, db, n.ID, n.OwnerUserID)
		if err != nil {
			log.Error("notification scheduler: failed to load recipients",
				"eventId", n.PublicID.String(), "err", err)
			continue
		}

		// Atomic claim: only the caller whose UPDATE flipped notified_at
		// from NULL to NOW() (RowsAffected == 1) may dispatch. Zero
		// affected rows means another replica or tick won the race.
		affected, err := queries.ClaimReminderForDelivery(ctx, n.ID)
		if err != nil {
			log.Error("notification scheduler: failed to claim reminder",
				"eventId", n.PublicID.String(), "err", err)
			continue
		}
		if affected == 0 {
			log.Debug("notification scheduler: reminder already claimed elsewhere",
				"eventId", n.PublicID.String())
			continue
		}

		sourceEventID, err := appendReminderEvent(ctx, queries, n)
		if err != nil {
			log.Error("notification scheduler: failed to append reminder event; will retry next tick",
				"eventId", n.PublicID.String(), "err", err)
			releaseReminderClaim(ctx, db, n)
			continue
		}

		if err := dispatcher.DeliverCalendarReminder(
			ctx, n.WorkspaceID, n.PublicID, n.Title, recipients, sourceEventID,
		); err != nil {
			// Release the claim so the next tick re-claims and retries
			// the dispatch. Within one dispatch (and any concurrent
			// replay of the same source event) the notifications unique
			// key dedupes; a next-tick retry appends a fresh event row,
			// which trades a possible duplicate row after a partial
			// failure for never losing the reminder.
			log.Error("notification scheduler: dispatch failed; will retry next tick",
				"eventId", n.PublicID.String(), "err", err)
			releaseReminderClaim(ctx, db, n)
			continue
		}
		delivered++
	}

	if delivered > 0 {
		log.Info("notification scheduler: delivered reminders", "count", delivered)
	}
}

// appendReminderEvent appends a calendar.reminder row to the append-only
// events log for a claimed reminder and returns the new events.id. The
// id becomes notifications.source_event_id so the
// (recipient_user_id, source_event_id, channel) unique key dedupes
// concurrent fan-outs of the same reminder.
//
// It calls [generated.Queries.AppendEvent] directly instead of going
// through eventbus.Append because the fan-out needs the inserted
// events.id, which eventbus.Append does not surface. The trade-off is
// that eventbus notify hooks do not fire for this row; that is safe
// because calendar.reminder is not a classified fan-out type, so the
// hook path would be a no-op anyway.
func appendReminderEvent(
	ctx context.Context,
	queries *generated.Queries,
	n pendingNotification,
) (int64, error) {
	payload, err := json.Marshal(map[string]string{
		"calendarEventId": n.PublicID.String(),
	})
	if err != nil {
		return 0, err
	}
	return queries.AppendEvent(ctx, generated.AppendEventParams{
		PublicID:          types.New(),
		WorkspaceID:       n.WorkspaceID,
		ActorSystemSource: sql.NullString{String: reminderSystemSource, Valid: true},
		Type:              reminderEventType,
		PayloadJson:       payload,
		OccurredAt:        time.Now().UTC(),
	})
}

// releaseReminderClaim resets notified_at back to NULL for a claimed
// calendar event whose reminder could not be dispatched, so the next
// scheduler tick can re-claim and retry. A failure here is only logged:
// the row then stays claimed and the reminder is dropped, which is the
// same outcome as failing silently but with an audit trail.
func releaseReminderClaim(ctx context.Context, db *sql.DB, n pendingNotification) {
	if _, err := db.ExecContext(ctx,
		`UPDATE calendar_events SET notified_at = NULL WHERE id = ?`, n.ID); err != nil {
		schedLog().Error("notification scheduler: failed to release reminder claim",
			"eventId", n.PublicID.String(), "err", err)
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
