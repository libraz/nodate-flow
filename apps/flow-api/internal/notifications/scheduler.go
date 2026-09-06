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
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/notification"
	"github.com/libraz/nodate-flow/packages/go-shared/recurrence"
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
// Each due occurrence is first claimed atomically by inserting a
// calendar_event_reminders row, so when multiple replicas run this loop
// only the claim winner dispatches. On a dispatch failure the claim row
// is deleted so the next tick retries instead of silently dropping the
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

// pendingNotification is one due reminder: an event plus the specific
// occurrence of it the reminder is for. For a non-recurring event the
// occurrence is the event's own start; for a series it is one expansion
// of the rule.
type pendingNotification struct {
	ID                 uint32
	PublicID           types.PublicID
	Title              string
	StartAt            time.Time
	OccurrenceStart    time.Time
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
	now := time.Now().UTC()
	notifications, err := dueReminders(ctx, db, now)
	if err != nil {
		log.Error("notification scheduler: failed to collect due reminders", "err", err)
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

		// Atomic claim: only the caller whose INSERT created the
		// (event, occurrence) row (RowsAffected == 1) may dispatch. Zero
		// affected rows means another replica or tick won the race.
		affected, err := queries.ClaimReminderOccurrence(ctx, generated.ClaimReminderOccurrenceParams{
			WorkspaceID:     n.WorkspaceID,
			EventID:         n.ID,
			OccurrenceStart: n.OccurrenceStart,
		})
		if err != nil {
			log.Error("notification scheduler: failed to claim reminder",
				"eventId", n.PublicID.String(),
				"occurrence", n.OccurrenceStart.Format(time.RFC3339), "err", err)
			continue
		}
		if affected == 0 {
			log.Debug("notification scheduler: reminder already claimed elsewhere",
				"eventId", n.PublicID.String(),
				"occurrence", n.OccurrenceStart.Format(time.RFC3339))
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
	if err := generated.New(db).ReleaseReminderOccurrence(ctx, generated.ReleaseReminderOccurrenceParams{
		EventID:         n.ID,
		OccurrenceStart: n.OccurrenceStart,
	}); err != nil {
		schedLog().Error("notification scheduler: failed to release reminder claim",
			"eventId", n.PublicID.String(),
			"occurrence", n.OccurrenceStart.Format(time.RFC3339), "err", err)
	}
}

// reminderWindow is how far ahead the scheduler looks for occurrences.
// It bounds the recurrence expansion and the candidate scan; a reminder
// offset longer than this would never come due, which is why the query
// widens the lower bound by the offset rather than assuming it is small.
const reminderWindow = 24 * time.Hour

// dueReminders collects every occurrence whose reminder window has
// opened and which has not started yet, across both plain and recurring
// events.
//
// Recurring events are the reason this is not a single SQL predicate.
// The rows a series produces do not exist in the table — only the master
// row and its rule do — so the candidates are read broadly and the
// occurrences are computed here, with the same expander the calendar
// surfaces use. Reading only the master row is what made "every Monday,
// 15 minutes before" fire once and go quiet for a year.
//
// An occurrence a separate override row replaces is left to that row: the
// master must not also fire at the start the occurrence no longer has.
//
// Times are compared in UTC in Go rather than against NOW() in SQL. The
// stored values are UTC wall clocks, and NOW() answers in the server's
// session timezone, so on a deployment whose MySQL is not UTC the two
// disagree by the offset — silently, and in the direction that makes
// reminders early or late rather than absent.
func dueReminders(ctx context.Context, db *sql.DB, now time.Time) ([]pendingNotification, error) {
	horizon := now.Add(reminderWindow)
	rows, err := db.QueryContext(ctx, `
		SELECT ce.id, ce.public_id, ce.title, ce.start_at, ce.notification_offset,
		       ce.owner_user_id, ce.workspace_id, ce.timezone,
		       ce.recurrence_rule, ce.recurrence_end, ce.recurrence_exceptions
		FROM calendar_events ce
		WHERE ce.notification_offset IS NOT NULL
		  AND ce.start_at IS NOT NULL
		  AND ce.enabled = TRUE
		  AND (
		    (ce.recurrence_rule IS NULL AND ce.start_at > ? AND ce.start_at <= ?)
		    OR (ce.recurrence_rule IS NOT NULL
		        AND (ce.recurrence_end IS NULL OR ce.recurrence_end >= ?))
		  )
	`, now, horizon.Add(time.Duration(maxReminderOffsetMinutes)*time.Minute), now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Every candidate is read before any is expanded, so the overridden
	// starts below cost one query for the whole tick rather than one per
	// series.
	var candidates []reminderCandidate
	for rows.Next() {
		var (
			c          reminderCandidate
			ruleRaw    []byte
			seriesEnd  sql.NullTime
			exceptions []byte
		)
		if err := rows.Scan(
			&c.n.ID, &c.n.PublicID, &c.n.Title, &c.n.StartAt, &c.n.NotificationOffset,
			&c.n.OwnerUserID, &c.n.WorkspaceID, &c.timezone,
			&ruleRaw, &seriesEnd, &exceptions,
		); err != nil {
			return nil, err
		}

		rule, perr := recurrence.ParseRule(ruleRaw)
		if perr != nil {
			schedLog().Error("notification scheduler: unparseable recurrence rule; skipping",
				"eventId", c.n.PublicID.String(), "err", perr)
			continue
		}
		c.rule = rule
		c.exceptions = recurrence.ParseExceptions(exceptions)
		if seriesEnd.Valid {
			end := seriesEnd.Time
			c.seriesEnd = &end
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	overridden, err := overriddenReminderStarts(ctx, db, candidates)
	if err != nil {
		return nil, err
	}

	var out []pendingNotification
	for _, c := range candidates {
		var claimedRows []pendingNotification
		if c.rule == nil {
			if occ, ok := dueOccurrence(c.n, c.n.StartAt, now); ok {
				claimedRows = append(claimedRows, occ)
			}
			out = append(out, claimedRows...)
			continue
		}

		// Expand a little past the horizon so an occurrence whose
		// reminder is due now but whose start is beyond it is still
		// found; dueOccurrence applies the exact bounds.
		for _, inst := range recurrence.Expand(recurrence.Event{
			StartAt:          c.n.StartAt,
			EndAt:            c.n.StartAt,
			Timezone:         c.timezone,
			Rule:             c.rule,
			Exceptions:       c.exceptions,
			OverriddenStarts: overridden[overrideKey{workspaceID: c.n.WorkspaceID, parentID: c.n.ID}],
			RecurrenceEnd:    c.seriesEnd,
		}, now, horizon.Add(time.Duration(maxReminderOffsetMinutes)*time.Minute)) {
			if occ, ok := dueOccurrence(c.n, inst.StartAt, now); ok {
				claimedRows = append(claimedRows, occ)
			}
		}
		out = append(out, claimedRows...)
	}
	return out, nil
}

// reminderCandidate is one scanned calendar_events row after its
// recurrence columns are decoded and before it is expanded into the
// occurrences whose reminders are due.
type reminderCandidate struct {
	n          pendingNotification
	timezone   string
	rule       *recurrence.Rule
	exceptions []string
	seriesEnd  *time.Time
}

// overrideKey names one master row. The scan spans every workspace, so
// the workspace id stays part of the key and part of the predicate rather
// than being left to the parent id alone.
type overrideKey struct {
	workspaceID uint32
	parentID    uint32
}

// overriddenReminderStarts returns, per master row, the occurrence starts
// a live override already stands in for, spelled as RFC 3339 UTC so the
// expander's exception parser resolves them to the instants it generates.
//
// The read is not filtered by date: an override may be moved anywhere,
// including past the reminder horizon, and it still replaces the
// occurrence it names. A range predicate would let the master fire a
// reminder for an occurrence that moved away.
//
// Nor is it filtered by visibility, and must not be. This read decides
// whether a meeting happens at an instant, not what to show a person: the
// recipients are the event's attendees plus its owner, a set, so there is
// no viewer to scope to. Withholding a confidential override would leave
// the master firing a reminder at a time nothing is happening, and would
// do it exactly when the replacement is confidential — so it must read
// every live override of these masters, whoever owns them. The reminder is
// not lost: an override carries its own start_at and notification_offset
// with no rule of its own, so the same scan picks it up through the
// non-recurring branch and fires at the moved time. The viewer-scoped
// query on the calendar read paths answers a different question and does
// not belong here.
func overriddenReminderStarts(
	ctx context.Context,
	db *sql.DB,
	candidates []reminderCandidate,
) (map[overrideKey][]string, error) {
	var (
		workspaceIDs []any
		parentIDs    []any
	)
	seen := map[overrideKey]bool{}
	for _, c := range candidates {
		// Only a row carrying a rule can be overridden; an override names
		// its master, never another override.
		if c.rule == nil {
			continue
		}
		key := overrideKey{workspaceID: c.n.WorkspaceID, parentID: c.n.ID}
		if seen[key] {
			continue
		}
		seen[key] = true
		workspaceIDs = append(workspaceIDs, key.workspaceID)
		parentIDs = append(parentIDs, key.parentID)
	}
	if len(parentIDs) == 0 {
		return nil, nil
	}

	//#nosec G202 -- the interpolated text is a generated ?-placeholder list sized from the id slices; every id travels as a bound argument
	q := `
		SELECT ov.workspace_id, ov.recurrence_parent_id, ov.recurrence_original_start
		FROM calendar_events ov
		WHERE ov.enabled = TRUE
		  AND ov.recurrence_original_start IS NOT NULL
		  AND ov.workspace_id IN (?` + strings.Repeat(",?", len(workspaceIDs)-1) + `)
		  AND ov.recurrence_parent_id IN (?` + strings.Repeat(",?", len(parentIDs)-1) + `)`

	args := make([]any, 0, len(workspaceIDs)+len(parentIDs))
	args = append(args, workspaceIDs...)
	args = append(args, parentIDs...)

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[overrideKey][]string{}
	for rows.Next() {
		var (
			key   overrideKey
			start time.Time
		)
		if err := rows.Scan(&key.workspaceID, &key.parentID, &start); err != nil {
			return nil, err
		}
		// The two IN lists are independent, so a row may pair a scanned
		// workspace with another workspace's master. Keeping only pairs
		// the scan actually produced makes the scoping exact.
		if !seen[key] {
			continue
		}
		out[key] = append(out[key], start.UTC().Format(time.RFC3339))
	}
	return out, rows.Err()
}

// maxReminderOffsetMinutes bounds how far before an occurrence a
// reminder may be configured, and therefore how far past the horizon the
// candidate scan has to look. A week is generous for "remind me before
// this" and keeps the scan bounded.
const maxReminderOffsetMinutes = 7 * 24 * 60

// dueOccurrence decides whether one occurrence's reminder is due now:
// the occurrence has not started, and its reminder time has passed.
func dueOccurrence(base pendingNotification, occurrenceStart, now time.Time) (pendingNotification, bool) {
	if !occurrenceStart.After(now) {
		return pendingNotification{}, false
	}
	remindAt := occurrenceStart.Add(-time.Duration(base.NotificationOffset) * time.Minute)
	if remindAt.After(now) {
		return pendingNotification{}, false
	}
	out := base
	out.OccurrenceStart = occurrenceStart.UTC()
	return out, true
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
